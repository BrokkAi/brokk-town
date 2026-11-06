// Package auth signs people into Town with GitHub or Google.
//
// A person signs in through the provider's authorization-code flow with PKCE,
// Town checks the identity against the configured allowlist, and the browser
// then holds an HMAC-signed session cookie. Sessions are stateless: the
// cookie carries the identity and its expiry, and every request re-checks the
// allowlist, so a person removed from it loses access at the next restart.
// Rotating the session key file signs everyone out.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SessionLifetime is how long a sign-in lasts before the person signs in again.
const SessionLifetime = 7 * 24 * time.Hour

// loginLifetime bounds the round trip to the provider and back.
const loginLifetime = 10 * time.Minute

// Identity is a signed-in person. It is what the session cookie carries.
type Identity struct {
	Provider string `json:"provider"`
	// Subject is the provider's stable account ID.
	Subject string `json:"sub"`
	// Login is the GitHub login. Google accounts have none.
	Login string `json:"login,omitempty"`
	// Email is a verified address, when the provider reported one.
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	// Domain is the Google Workspace domain (the hd claim). Consumer Google
	// accounts have none.
	Domain string `json:"hd,omitempty"`
	// Org is the allowed GitHub organization that admitted this person, when
	// membership is what admitted them.
	Org     string `json:"org,omitempty"`
	Expires int64  `json:"exp"`
}

// Display is how Town names the person in Activity and the Assistant.
func (i Identity) Display() string {
	handle := i.Email
	if i.Login != "" {
		handle = "@" + i.Login
	}
	if i.Name != "" && i.Name != i.Login && handle != "" {
		return i.Name + " (" + handle + ")"
	}
	if handle != "" {
		return handle
	}
	return i.Name
}

// Public is the identity as the browser sees it.
func (i Identity) Public() map[string]string {
	out := map[string]string{"provider": i.Provider, "display": i.Display()}
	if i.Login != "" {
		out["login"] = i.Login
	}
	if i.Email != "" {
		out["email"] = i.Email
	}
	if i.Name != "" {
		out["name"] = i.Name
	}
	return out
}

// Allow lists who may sign in. A person is admitted if any rule matches.
// Matching is case-insensitive.
type Allow struct {
	GitHubUsers []string
	// GitHubOrgs admits active members of these organizations. Membership is
	// checked when a person signs in.
	GitHubOrgs []string
	// GoogleDomains admits Google Workspace accounts of these domains, by the
	// verified hd claim, never by the email's suffix.
	GoogleDomains []string
	// Emails admits these verified addresses from either provider.
	Emails []string
}

func (a Allow) empty() bool {
	return len(a.GitHubUsers)+len(a.GitHubOrgs)+len(a.GoogleDomains)+len(a.Emails) == 0
}

// Provider holds one OAuth client. The endpoint fields default to the real
// provider; tests point them at a fake.
type Provider struct {
	ClientID     string
	ClientSecret string
	AuthorizeURL string
	TokenURL     string
	// APIURL is GitHub's REST API root, or Google's userinfo endpoint.
	APIURL string
}

// Config is sign-in for one Town.
type Config struct {
	// Origin is the browser address, such as https://town.example.com.
	Origin string
	GitHub *Provider
	Google *Provider
	Allow  Allow
	// Key signs session and login cookies. At least 32 bytes.
	Key []byte
}

// Service runs sign-in. Create it with New.
type Service struct {
	cfg    Config
	secure bool
	client *http.Client
	now    func() time.Time
}

// New validates the configuration. It refuses an empty allowlist: a Town open
// to every GitHub or Google account is never what an operator meant.
func New(cfg Config) (*Service, error) {
	origin, err := url.Parse(cfg.Origin)
	if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" || origin.Path != "" {
		return nil, errors.New("sign-in needs the public URL browsers use, such as https://town.example.com")
	}
	if cfg.GitHub == nil && cfg.Google == nil {
		return nil, errors.New("sign-in needs a GitHub or Google OAuth client: set BROKK_TOWN_GITHUB_CLIENT_ID or BROKK_TOWN_GOOGLE_CLIENT_ID and its secret file")
	}
	for name, p := range map[string]*Provider{"GitHub": cfg.GitHub, "Google": cfg.Google} {
		if p != nil && (p.ClientID == "" || p.ClientSecret == "") {
			return nil, fmt.Errorf("the %s OAuth client needs both a client ID and a client secret", name)
		}
	}
	if cfg.Allow.empty() {
		return nil, errors.New("sign-in needs an allowlist: set allowed GitHub users or organizations, Google Workspace domains, or emails")
	}
	if cfg.GitHub == nil && len(cfg.Allow.GitHubUsers)+len(cfg.Allow.GitHubOrgs) > 0 {
		return nil, errors.New("GitHub users or organizations are allowed, but no GitHub OAuth client is configured")
	}
	if cfg.Google == nil && len(cfg.Allow.GoogleDomains) > 0 {
		return nil, errors.New("Google domains are allowed, but no Google OAuth client is configured")
	}
	if len(cfg.Key) < 32 {
		return nil, errors.New("the session key must be at least 32 bytes")
	}
	if p := cfg.GitHub; p != nil {
		p.AuthorizeURL = or(p.AuthorizeURL, "https://github.com/login/oauth/authorize")
		p.TokenURL = or(p.TokenURL, "https://github.com/login/oauth/access_token")
		p.APIURL = strings.TrimSuffix(or(p.APIURL, "https://api.github.com"), "/")
	}
	if p := cfg.Google; p != nil {
		p.AuthorizeURL = or(p.AuthorizeURL, "https://accounts.google.com/o/oauth2/v2/auth")
		p.TokenURL = or(p.TokenURL, "https://oauth2.googleapis.com/token")
		p.APIURL = or(p.APIURL, "https://openidconnect.googleapis.com/v1/userinfo")
	}
	cfg.Origin = origin.Scheme + "://" + origin.Host
	cfg.Allow = Allow{GitHubUsers: lower(cfg.Allow.GitHubUsers), GitHubOrgs: lower(cfg.Allow.GitHubOrgs), GoogleDomains: lower(cfg.Allow.GoogleDomains), Emails: lower(cfg.Allow.Emails)}
	return &Service{cfg: cfg, secure: origin.Scheme == "https", client: &http.Client{Timeout: 15 * time.Second}, now: time.Now}, nil
}

func or(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func lower(values []string) []string {
	out := []string{}
	for _, v := range values {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Providers names the configured sign-in providers, in a fixed order.
func (s *Service) Providers() []string {
	out := []string{}
	if s.cfg.GitHub != nil {
		out = append(out, "github")
	}
	if s.cfg.Google != nil {
		out = append(out, "google")
	}
	return out
}

func (s *Service) provider(name string) *Provider {
	switch name {
	case "github":
		return s.cfg.GitHub
	case "google":
		return s.cfg.Google
	}
	return nil
}

func (s *Service) cookieName(base string) string {
	// __Host- binds the cookie to this exact origin; browsers accept it only
	// over HTTPS.
	if s.secure {
		return "__Host-brokk-town-" + base
	}
	return "brokk-town-" + base
}

// Routes registers the sign-in endpoints on mux.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login/{provider}", s.begin)
	mux.HandleFunc("GET /auth/callback/{provider}", s.callback)
	mux.HandleFunc("POST /auth/logout", s.logout)
}

type loginState struct {
	Provider string `json:"provider"`
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Expires  int64  `json:"exp"`
}

func (s *Service) begin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	p := s.provider(name)
	if p == nil {
		http.Error(w, "unknown sign-in provider", http.StatusNotFound)
		return
	}
	login := loginState{Provider: name, State: random(), Verifier: random(), Expires: s.now().Add(loginLifetime).Unix()}
	value, err := s.seal(login)
	if err != nil {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	// Lax: the provider's redirect back is a top-level navigation, which must
	// carry this cookie so the callback can match its state.
	http.SetCookie(w, &http.Cookie{Name: s.cookieName("login"), Value: value, Path: "/", MaxAge: int(loginLifetime.Seconds()), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	challenge := sha256.Sum256([]byte(login.Verifier))
	query := url.Values{
		"client_id":             {p.ClientID},
		"redirect_uri":          {s.redirect(name)},
		"state":                 {login.State},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	if name == "github" {
		scopes := "read:user"
		if len(s.cfg.Allow.Emails) > 0 {
			scopes += " user:email"
		}
		if len(s.cfg.Allow.GitHubOrgs) > 0 {
			scopes += " read:org"
		}
		query.Set("scope", scopes)
		query.Set("allow_signup", "false")
	} else {
		query.Set("response_type", "code")
		query.Set("scope", "openid email profile")
		query.Set("prompt", "select_account")
		if len(s.cfg.Allow.GoogleDomains) == 1 && len(s.cfg.Allow.Emails) == 0 {
			// A hint only; the callback enforces the domain.
			query.Set("hd", s.cfg.Allow.GoogleDomains[0])
		}
	}
	http.Redirect(w, r, p.AuthorizeURL+"?"+query.Encode(), http.StatusFound)
}

func (s *Service) redirect(provider string) string {
	return s.cfg.Origin + "/auth/callback/" + provider
}

// ErrNotAllowed means the provider vouched for the person, but no allowlist
// rule admits them.
var ErrNotAllowed = errors.New("this account is not allowed to use this Town")

func (s *Service) callback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	p := s.provider(name)
	if p == nil {
		http.Error(w, "unknown sign-in provider", http.StatusNotFound)
		return
	}
	clear := &http.Cookie{Name: s.cookieName("login"), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode}
	var login loginState
	cookie, err := r.Cookie(s.cookieName("login"))
	if err != nil || s.open(cookie.Value, &login) != nil || login.Provider != name || login.Expires < s.now().Unix() {
		http.SetCookie(w, clear)
		s.fail(w, r, "Sign-in expired or was started in another browser. Try again.")
		return
	}
	http.SetCookie(w, clear)
	query := r.URL.Query()
	if !subtleEqual(query.Get("state"), login.State) {
		s.fail(w, r, "Sign-in could not be verified. Try again.")
		return
	}
	if query.Get("error") != "" {
		s.fail(w, r, "Sign-in was cancelled.")
		return
	}
	code := query.Get("code")
	if code == "" {
		s.fail(w, r, "Sign-in could not be verified. Try again.")
		return
	}
	ctx := r.Context()
	token, err := s.exchange(ctx, name, p, code, login.Verifier)
	if err != nil {
		s.fail(w, r, "The sign-in provider did not accept the request. Try again.")
		return
	}
	var identity Identity
	if name == "github" {
		identity, err = s.github(ctx, p, token)
	} else {
		identity, err = s.google(ctx, p, token)
	}
	if errors.Is(err, ErrNotAllowed) {
		s.fail(w, r, "This account is not allowed to use this Town. Ask its operator to add you.")
		return
	}
	if err != nil {
		s.fail(w, r, "The sign-in provider could not describe this account. Try again.")
		return
	}
	cookie, err = s.Session(identity)
	if err != nil {
		s.fail(w, r, "Sign-in could not be saved. Try again.")
		return
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/", http.StatusFound)
}

// Session is the cookie that signs identity in for SessionLifetime. The
// callback sets it once the provider and the allowlist have vouched for the
// person; tests use it to stand in for that round trip.
func (s *Service) Session(identity Identity) (*http.Cookie, error) {
	identity.Expires = s.now().Add(SessionLifetime).Unix()
	value, err := s.seal(identity)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{Name: s.cookieName("session"), Value: value, Path: "/", MaxAge: int(SessionLifetime.Seconds()), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode}, nil
}

// fail returns to the app with a message it shows on the sign-in screen. The
// message is one of the fixed strings above, never provider text.
func (s *Service) fail(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/#signin-error="+url.QueryEscape(message), http.StatusFound)
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName("session"), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
}

// Identity returns the signed-in person for a request, if the session cookie
// is valid, unexpired and still admitted by the allowlist.
func (s *Service) Identity(r *http.Request) (Identity, bool) {
	cookie, err := r.Cookie(s.cookieName("session"))
	if err != nil {
		return Identity{}, false
	}
	var identity Identity
	if s.open(cookie.Value, &identity) != nil || identity.Expires < s.now().Unix() || s.provider(identity.Provider) == nil || !s.stillAllowed(identity) {
		return Identity{}, false
	}
	return identity, true
}

// stillAllowed re-checks a session against the current allowlist. Org
// membership was verified at sign-in; here the org must still be listed.
func (s *Service) stillAllowed(i Identity) bool {
	a := s.cfg.Allow
	switch i.Provider {
	case "github":
		if i.Login != "" && slices.Contains(a.GitHubUsers, strings.ToLower(i.Login)) {
			return true
		}
		if i.Org != "" && slices.Contains(a.GitHubOrgs, strings.ToLower(i.Org)) {
			return true
		}
	case "google":
		if i.Domain != "" && slices.Contains(a.GoogleDomains, strings.ToLower(i.Domain)) {
			return true
		}
	}
	return i.Email != "" && slices.Contains(a.Emails, strings.ToLower(i.Email))
}

func (s *Service) exchange(ctx context.Context, name string, p *Provider, code, verifier string) (string, error) {
	form := url.Values{
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"code":          {code},
		"redirect_uri":  {s.redirect(name)},
		"code_verifier": {verifier},
	}
	if name == "google" {
		form.Set("grant_type", "authorization_code")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := s.do(req, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("token exchange returned no access token")
	}
	return out.AccessToken, nil
}

// do sends a request and decodes a bounded JSON answer. It never includes the
// response body in an error: it may echo a token.
func (s *Service) do(req *http.Request, out any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: request failed", req.Method, req.URL.Host)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &statusError{resp.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("provider answered %d", e.code) }

func (s *Service) get(ctx context.Context, address, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	return s.do(req, out)
}

func (s *Service) github(ctx context.Context, p *Provider, token string) (Identity, error) {
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := s.get(ctx, p.APIURL+"/user", token, &user); err != nil {
		return Identity{}, err
	}
	if user.ID == 0 || !validLogin(user.Login) {
		return Identity{}, errors.New("GitHub returned no account")
	}
	identity := Identity{Provider: "github", Subject: fmt.Sprint(user.ID), Login: user.Login, Name: clean(user.Name)}
	a := s.cfg.Allow
	if slices.Contains(a.GitHubUsers, strings.ToLower(user.Login)) {
		return identity, nil
	}
	for _, org := range a.GitHubOrgs {
		var membership struct {
			State string `json:"state"`
		}
		err := s.get(ctx, p.APIURL+"/user/memberships/orgs/"+url.PathEscape(org), token, &membership)
		var status *statusError
		if errors.As(err, &status) && (status.code == http.StatusNotFound || status.code == http.StatusForbidden) {
			continue
		}
		if err != nil {
			return Identity{}, err
		}
		if membership.State == "active" {
			identity.Org = org
			return identity, nil
		}
	}
	if len(a.Emails) > 0 {
		var emails []struct {
			Email    string `json:"email"`
			Verified bool   `json:"verified"`
			Primary  bool   `json:"primary"`
		}
		if err := s.get(ctx, p.APIURL+"/user/emails", token, &emails); err != nil {
			return Identity{}, err
		}
		for _, e := range emails {
			if e.Verified && slices.Contains(a.Emails, strings.ToLower(e.Email)) {
				identity.Email = strings.ToLower(e.Email)
				return identity, nil
			}
		}
	}
	return Identity{}, ErrNotAllowed
}

func (s *Service) google(ctx context.Context, p *Provider, token string) (Identity, error) {
	var info struct {
		Subject  string `json:"sub"`
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
		Name     string `json:"name"`
		Domain   string `json:"hd"`
	}
	if err := s.get(ctx, p.APIURL, token, &info); err != nil {
		return Identity{}, err
	}
	if info.Subject == "" || len(info.Subject) > 255 {
		return Identity{}, errors.New("Google returned no account")
	}
	if !info.Verified || info.Email == "" || len(info.Email) > 254 {
		return Identity{}, ErrNotAllowed
	}
	identity := Identity{Provider: "google", Subject: info.Subject, Email: strings.ToLower(info.Email), Name: clean(info.Name), Domain: strings.ToLower(info.Domain)}
	if !s.stillAllowed(identity) {
		return Identity{}, ErrNotAllowed
	}
	return identity, nil
}

func validLogin(login string) bool {
	if login == "" || len(login) > 39 {
		return false
	}
	return strings.Trim(strings.ToLower(login), "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

// clean keeps a display name printable and short.
func clean(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	for len(name) > 80 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func subtleEqual(a, b string) bool {
	return a != "" && hmac.Equal([]byte(a), []byte(b))
}

// seal signs a value as base64url(JSON) "." base64url(HMAC-SHA256).
func (s *Service) seal(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(data)
	return payload + "." + s.sign(payload), nil
}

func (s *Service) open(value string, v any) error {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok || len(value) > 4096 || !hmac.Equal([]byte(signature), []byte(s.sign(payload))) {
		return errors.New("invalid signature")
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (s *Service) sign(payload string) string {
	mac := hmac.New(sha256.New, s.cfg.Key)
	mac.Write([]byte("brokk-town-session\x00"))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SessionKey reads the session key from dir/session-key, creating a random one
// on first use. Delete the file to sign everyone out.
func SessionKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, "session-key")
	if b, err := os.ReadFile(path); err == nil {
		if key, e := hex.DecodeString(strings.TrimSpace(string(b))); e == nil && len(key) >= 32 {
			return key, nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0600); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	return key, nil
}

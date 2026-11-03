// Package auth signs people in to a shared Town with their GitHub account.
//
// Town runs GitHub's OAuth web flow itself. A person is admitted when their
// login is listed or they are an active member of a listed organization. The
// GitHub token from sign-in is used only to read who they are and which of the
// listed organizations they belong to; Town keeps none of it. What it keeps is
// a session cookie that carries the login and an expiry, signed with a key
// held in the state directory, so sessions survive a restart and deleting the
// key ends every one of them.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Config is the config file's auth object.
type Config struct {
	GitHub *GitHubConfig `json:"github"`
	// AllowInsecureHTTP permits sign-in on a plain http address that is not
	// loopback, such as an internal host without TLS. The session cookie then
	// crosses the network unencrypted, so anyone who can read that traffic
	// can act as the person it belongs to.
	AllowInsecureHTTP bool `json:"allow_insecure_http,omitempty"`
}

// GitHubConfig names the GitHub OAuth app Town signs people in with and who
// may sign in. The client secret is never written into the config file itself:
// client_secret_file names a file, such as a mounted secret, that holds it.
type GitHubConfig struct {
	ClientID         string   `json:"client_id"`
	ClientSecretFile string   `json:"client_secret_file"`
	Users            []string `json:"users"`
	Organizations    []string `json:"organizations"`
	// URL is GitHub's web address and APIURL its REST API, for GitHub
	// Enterprise Server. They default to github.com.
	URL    string `json:"url,omitempty"`
	APIURL string `json:"api_url,omitempty"`
	// CAFile names a PEM file of the certificate authorities that sign
	// GitHub's certificate, such as an Enterprise Server's own or a
	// self-signed one. Sign-in then trusts only those, as a bot's ca_file
	// does; the rest of Town is unaffected.
	CAFile string `json:"ca_file,omitempty"`
}

// SessionLifetime is how long one sign-in lasts.
const SessionLifetime = 12 * time.Hour

const (
	stateLifetime = 10 * time.Minute
	maxListed     = 1000
)

// Service runs sign-in for one Town.
type Service struct {
	clientID, clientSecret string
	webURL, apiURL         string
	users, orgs            []string
	key                    []byte
	// origin is the address browsers use, such as https://town.example.com.
	origin string
	secure bool
	// Client reaches GitHub. New sets one with a 15-second timeout.
	Client *http.Client
	now    func() time.Time
}

// Validate checks the auth object without reading the secret file.
func (c Config) Validate() error {
	g := c.GitHub
	if g == nil {
		return errors.New("auth: github is required")
	}
	if strings.TrimSpace(g.ClientID) == "" || len(g.ClientID) > 256 {
		return errors.New("auth.github: client_id is required")
	}
	if g.ClientSecretFile == "" {
		return errors.New("auth.github: client_secret_file is required")
	}
	if len(g.Users) == 0 && len(g.Organizations) == 0 {
		return errors.New("auth.github: list the users or organizations who may sign in")
	}
	if len(g.Users) > maxListed || len(g.Organizations) > maxListed {
		return fmt.Errorf("auth.github: at most %d users and %d organizations", maxListed, maxListed)
	}
	for _, name := range append(slices.Clone(g.Users), g.Organizations...) {
		if !ValidLogin(name) {
			return fmt.Errorf("auth.github: %q is not a GitHub login or organization", name)
		}
	}
	for field, value := range map[string]string{"url": g.URL, "api_url": g.APIURL} {
		if value == "" {
			continue
		}
		if u, err := url.Parse(value); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("auth.github.%s must be an https URL", field)
		}
	}
	return nil
}

// New prepares sign-in for the Town browsers reach at origin. stateDir holds
// the session key, which New creates on first use.
func New(c Config, origin, stateDir string) (*Service, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("sign-in needs the address browsers use")
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) && !c.AllowInsecureHTTP {
		return nil, errors.New("sign-in needs an https --public-url, or a loopback address for local testing; set auth.allow_insecure_http to accept plain http on a trusted network")
	}
	client, err := githubClient(c.GitHub.CAFile)
	if err != nil {
		return nil, err
	}
	secret, err := os.ReadFile(c.GitHub.ClientSecretFile)
	if err != nil {
		return nil, fmt.Errorf("auth.github: read client_secret_file: %w", err)
	}
	key, err := sessionKey(stateDir)
	if err != nil {
		return nil, err
	}
	g := c.GitHub
	s := &Service{
		clientID:     strings.TrimSpace(g.ClientID),
		clientSecret: strings.TrimSpace(string(secret)),
		webURL:       strings.TrimRight(g.URL, "/"),
		apiURL:       strings.TrimRight(g.APIURL, "/"),
		key:          key,
		origin:       u.Scheme + "://" + u.Host,
		secure:       u.Scheme == "https",
		Client:       client,
		now:          time.Now,
	}
	if s.clientSecret == "" {
		return nil, errors.New("auth.github: client_secret_file is empty")
	}
	if s.webURL == "" {
		s.webURL = "https://github.com"
	}
	if s.apiURL == "" {
		s.apiURL = "https://api.github.com"
	}
	for _, name := range g.Users {
		s.users = append(s.users, strings.ToLower(name))
	}
	for _, name := range g.Organizations {
		s.orgs = append(s.orgs, strings.ToLower(name))
	}
	return s, nil
}

// githubClient reaches GitHub through the environment's proxy settings,
// trusting the system's certificate authorities or only those in caFile.
func githubClient(caFile string) (*http.Client, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	if caFile == "" {
		return client, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("auth.github: read ca_file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("auth.github: ca_file holds no PEM certificate")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	client.Transport = transport
	return client, nil
}

func loopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// ValidLogin accepts a GitHub login or organization name, including an
// Enterprise Managed User's underscore suffix.
func ValidLogin(name string) bool {
	if name == "" || len(name) > 64 || name[0] == '-' || name[0] == '_' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// sessionKey reads the session signing key, creating it on first use.
// Deleting the file signs everyone out at the next start.
func sessionKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, "session-key")
	if b, err := os.ReadFile(path); err == nil {
		if key, e := hex.DecodeString(strings.TrimSpace(string(b))); e == nil && len(key) == 32 {
			return key, nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0600); err != nil {
		return nil, fmt.Errorf("write session key: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	return key, nil
}

// The cookie names. A secure Town uses the __Host- prefix, which a browser
// accepts only from that exact host over https.
func (s *Service) sessionCookie() string {
	if s.secure {
		return "__Host-brokk-town-session"
	}
	return "brokk-town-session"
}
func (s *Service) stateCookie() string {
	if s.secure {
		return "__Host-brokk-town-sign-in"
	}
	return "brokk-town-sign-in"
}

// session is what the cookie carries.
type session struct {
	Login   string `json:"l"`
	Expires int64  `json:"e"`
	// Org records that the login was admitted as a member of a listed
	// organization rather than by name.
	Org bool `json:"o,omitempty"`
}

func (s *Service) sign(payload []byte) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) verify(value string) ([]byte, bool) {
	body, sig, ok := strings.Cut(value, ".")
	if !ok || len(value) > 1024 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, false
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return payload, hmac.Equal(got, mac.Sum(nil))
}

// Login returns the signed-in person's GitHub login for a request, or false
// when it carries no valid, unexpired session from someone still admitted.
func (s *Service) Login(r *http.Request) (string, bool) {
	c, err := r.Cookie(s.sessionCookie())
	if err != nil {
		return "", false
	}
	payload, ok := s.verify(c.Value)
	if !ok {
		return "", false
	}
	var v session
	if json.Unmarshal(payload, &v) != nil || !ValidLogin(v.Login) || s.now().Unix() >= v.Expires {
		return "", false
	}
	if slices.Contains(s.users, strings.ToLower(v.Login)) || (v.Org && len(s.orgs) > 0) {
		return v.Login, true
	}
	return "", false
}

// Handler serves /auth/login, /auth/callback and /auth/logout.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", s.login)
	mux.HandleFunc("GET /auth/callback", s.callback)
	mux.HandleFunc("POST /auth/logout", s.logout)
	return mux
}

func (s *Service) redirectURI() string { return s.origin + "/auth/callback" }

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(nonce)
	// Lax, not Strict: the browser must send it on the top-level return
	// from GitHub, which is a cross-site navigation.
	http.SetCookie(w, &http.Cookie{Name: s.stateCookie(), Value: state, Path: "/", MaxAge: int(stateLifetime / time.Second), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	query := url.Values{"client_id": {s.clientID}, "redirect_uri": {s.redirectURI()}, "state": {state}, "allow_signup": {"false"}}
	if len(s.orgs) > 0 {
		query.Set("scope", "read:org")
	}
	http.Redirect(w, r, s.webURL+"/login/oauth/authorize?"+query.Encode(), http.StatusFound)
}

func (s *Service) callback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(s.stateCookie())
	http.SetCookie(w, &http.Cookie{Name: s.stateCookie(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	query := r.URL.Query()
	if query.Get("error") != "" {
		s.refuse(w, "GitHub did not authorize Brokk Town.", http.StatusForbidden)
		return
	}
	if err != nil || stateCookie.Value == "" || subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(query.Get("state"))) != 1 {
		s.refuse(w, "This sign-in link has expired or was opened in another browser.", http.StatusBadRequest)
		return
	}
	code := query.Get("code")
	if code == "" || len(code) > 512 {
		s.refuse(w, "GitHub returned no authorization code.", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	token, err := s.exchange(ctx, code)
	if err != nil {
		s.refuse(w, "Brokk Town could not complete sign-in with GitHub.", http.StatusBadGateway)
		return
	}
	login, err := s.user(ctx, token)
	if err != nil {
		s.refuse(w, "Brokk Town could not read your GitHub account.", http.StatusBadGateway)
		return
	}
	v := session{Login: login, Expires: s.now().Add(SessionLifetime).Unix()}
	if !slices.Contains(s.users, strings.ToLower(login)) {
		member, err := s.member(ctx, token)
		if err != nil {
			s.refuse(w, "Brokk Town could not check your GitHub organizations.", http.StatusBadGateway)
			return
		}
		if !member {
			s.refuse(w, "Your GitHub account ("+login+") is not allowed to use this Brokk Town.", http.StatusForbidden)
			return
		}
		v.Org = true
	}
	payload, _ := json.Marshal(v)
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookie(), Value: s.sign(payload), Path: "/", MaxAge: int(SessionLifetime / time.Second), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.origin {
		http.Error(w, "sign out from Brokk Town itself", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookie(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true}`+"\n")
}

// refuse answers a failed sign-in with a short page that offers another try.
func (s *Service) refuse(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><title>Sign-in failed · Brokk Town</title><body style=\"font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem\"><h1>Sign-in failed</h1><p>%s</p><p><a href=\"/auth/login\">Try again</a></p></body></html>\n", html.EscapeString(message))
}

// exchange trades the authorization code for a GitHub token.
func (s *Service) exchange(ctx context.Context, code string) (string, error) {
	form := url.Values{"client_id": {s.clientID}, "client_secret": {s.clientSecret}, "code": {code}, "redirect_uri": {s.redirectURI()}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
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
		return "", fmt.Errorf("exchange code: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("exchange code: GitHub answered %q", out.Error)
	}
	return out.AccessToken, nil
}

// user reads the signed-in person's login.
func (s *Service) user(ctx context.Context, token string) (string, error) {
	var out struct {
		Login string `json:"login"`
	}
	if _, err := s.get(ctx, token, "/user", &out); err != nil {
		return "", err
	}
	if !ValidLogin(out.Login) {
		return "", errors.New("GitHub returned no valid login")
	}
	return out.Login, nil
}

// member reports whether the person is an active member of a listed
// organization. GitHub answers 404 or 403 for an organization they are not in
// or that has not approved the app; neither admits them.
func (s *Service) member(ctx context.Context, token string) (bool, error) {
	for _, org := range s.orgs {
		var out struct {
			State string `json:"state"`
		}
		status, err := s.get(ctx, token, "/user/memberships/orgs/"+url.PathEscape(org), &out)
		if err != nil {
			return false, err
		}
		if status == http.StatusOK && out.State == "active" {
			return true, nil
		}
	}
	return false, nil
}

// get reads one API resource. A 404 or 403 is an answer, reported by status;
// any other failure is an error.
func (s *Service) get(ctx context.Context, token, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, nil
	case resp.StatusCode != http.StatusOK:
		return resp.StatusCode, fmt.Errorf("GitHub %s answered %s", path, resp.Status)
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (s *Service) do(req *http.Request, out any) error {
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

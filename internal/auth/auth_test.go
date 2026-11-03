package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProvider plays GitHub's or Google's OAuth and user endpoints. It checks
// the PKCE verifier against the challenge the authorize step sent.
type fakeProvider struct {
	t          *testing.T
	server     *httptest.Server
	challenge  string
	user       map[string]any
	orgs       map[string]string // org -> membership state
	emails     []map[string]any
	exchanged  int
	tokenCheck func(url.Values)
}

func newFake(t *testing.T) *fakeProvider {
	f := &fakeProvider{t: t, orgs: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.challenge = q.Get("code_challenge")
		if q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || q.Get("client_id") != "client" {
			t.Errorf("authorize query %v", q)
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=the-code&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.PostForm.Get("code") != "the-code" || r.PostForm.Get("client_secret") != "secret" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			http.Error(w, "bad grant", http.StatusBadRequest)
			return
		}
		if f.tokenCheck != nil {
			f.tokenCheck(r.PostForm)
		}
		f.exchanged++
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access"})
	})
	authorized := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer access" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if authorized(w, r) {
			_ = json.NewEncoder(w).Encode(f.user)
		}
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		if authorized(w, r) {
			_ = json.NewEncoder(w).Encode(f.user)
		}
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) {
		if authorized(w, r) {
			_ = json.NewEncoder(w).Encode(f.emails)
		}
	})
	mux.HandleFunc("GET /user/memberships/orgs/{org}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		state, ok := f.orgs[r.PathValue("org")]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"state": state})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeProvider) provider(api string) *Provider {
	return &Provider{ClientID: "client", ClientSecret: "secret", AuthorizeURL: f.server.URL + "/authorize", TokenURL: f.server.URL + "/token", APIURL: f.server.URL + api}
}

// town serves the sign-in routes plus a page that reports the identity.
func town(t *testing.T, cfg Config) (*Service, *httptest.Server) {
	t.Helper()
	var service *Service
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	cfg.Origin = server.URL
	cfg.Key = []byte(strings.Repeat("k", 32))
	var err error
	if service, err = New(cfg); err != nil {
		t.Fatal(err)
	}
	service.Routes(mux)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		identity, ok := service.Identity(r)
		if !ok {
			_, _ = w.Write([]byte("anonymous"))
			return
		}
		_, _ = w.Write([]byte(identity.Display()))
	})
	return service, server
}

// signIn walks the browser through the whole flow and returns what the app
// page shows afterwards, and the address it landed on.
func signIn(t *testing.T, server *httptest.Server, provider string) (string, *url.URL, *http.Client) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(server.URL + "/auth/login/" + provider)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, e := resp.Body.Read(buf)
		body.WriteString(string(buf[:n]))
		if e != nil {
			break
		}
	}
	return body.String(), resp.Request.URL, client
}

func TestAnAllowedGitHubUserSignsIn(t *testing.T) {
	fake := newFake(t)
	fake.user = map[string]any{"id": 42, "login": "ada", "name": "Ada Lovelace"}
	_, server := town(t, Config{GitHub: fake.provider(""), Allow: Allow{GitHubUsers: []string{"ADA"}}})
	page, landed, _ := signIn(t, server, "github")
	if page != "Ada Lovelace (@ada)" || landed.Fragment != "" {
		t.Fatalf("page %q at %s", page, landed)
	}
}

func TestAGitHubOrgMemberSignsInAndAnInvitedOneDoesNot(t *testing.T) {
	for _, tc := range []struct {
		state, want string
	}{{"active", "@grace"}, {"pending", "anonymous"}} {
		fake := newFake(t)
		fake.user = map[string]any{"id": 7, "login": "grace"}
		fake.orgs["brokkai"] = tc.state
		_, server := town(t, Config{GitHub: fake.provider(""), Allow: Allow{GitHubOrgs: []string{"BrokkAi"}}})
		page, landed, _ := signIn(t, server, "github")
		if page != tc.want {
			t.Fatalf("%s membership: page %q at %s", tc.state, page, landed)
		}
		if tc.state == "pending" && !strings.Contains(landed.Fragment, "not+allowed") {
			t.Fatalf("a refused person is not told why: %s", landed)
		}
	}
}

func TestAGitHubUserIsAdmittedOnlyByAVerifiedEmail(t *testing.T) {
	for _, verified := range []bool{true, false} {
		fake := newFake(t)
		fake.user = map[string]any{"id": 9, "login": "lin"}
		fake.emails = []map[string]any{{"email": "Lin@Example.com", "verified": verified, "primary": true}}
		_, server := town(t, Config{GitHub: fake.provider(""), Allow: Allow{Emails: []string{"lin@example.com"}}})
		page, _, _ := signIn(t, server, "github")
		if (page == "@lin") != verified {
			t.Fatalf("verified %v: page %q", verified, page)
		}
	}
}

func TestGoogleAdmitsAWorkspaceDomainByItsClaimNotTheEmailSuffix(t *testing.T) {
	for _, tc := range []struct {
		name string
		user map[string]any
		want string
	}{
		{"workspace account", map[string]any{"sub": "1", "email": "kim@example.com", "email_verified": true, "hd": "example.com", "name": "Kim"}, "Kim (kim@example.com)"},
		{"consumer account with the domain's address", map[string]any{"sub": "2", "email": "kim@example.com", "email_verified": true}, "anonymous"},
		{"unverified address", map[string]any{"sub": "3", "email": "kim@example.com", "email_verified": false, "hd": "example.com"}, "anonymous"},
		{"another domain", map[string]any{"sub": "4", "email": "kim@other.com", "email_verified": true, "hd": "other.com"}, "anonymous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFake(t)
			fake.user = tc.user
			fake.tokenCheck = func(form url.Values) {
				if form.Get("grant_type") != "authorization_code" {
					t.Errorf("grant type %q", form.Get("grant_type"))
				}
			}
			_, server := town(t, Config{Google: fake.provider("/userinfo"), Allow: Allow{GoogleDomains: []string{"example.com"}}})
			if page, landed, _ := signIn(t, server, "google"); page != tc.want {
				t.Fatalf("page %q at %s", page, landed)
			}
		})
	}
}

func TestACallbackWithoutItsLoginCookieIsRefused(t *testing.T) {
	fake := newFake(t)
	fake.user = map[string]any{"id": 42, "login": "ada"}
	_, server := town(t, Config{GitHub: fake.provider(""), Allow: Allow{GitHubUsers: []string{"ada"}}})
	// An attacker's own code and state, delivered to a victim's browser that
	// never started a sign-in, must not sign the victim in as the attacker.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(server.URL + "/auth/callback/github?code=the-code&state=attacker")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if fake.exchanged != 0 || !strings.Contains(resp.Request.URL.Fragment, "signin-error") {
		t.Fatalf("exchanged %d, landed %s", fake.exchanged, resp.Request.URL)
	}
}

func TestASessionIsRefusedWhenTamperedExpiredOrNoLongerAllowed(t *testing.T) {
	service, _ := town(t, Config{GitHub: &Provider{ClientID: "c", ClientSecret: "s"}, Allow: Allow{GitHubUsers: []string{"ada"}}})
	now := time.Unix(1_800_000_000, 0)
	service.now = func() time.Time { return now }
	request := func(value string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: service.cookieName("session"), Value: value})
		return r
	}
	valid, _ := service.seal(Identity{Provider: "github", Subject: "42", Login: "ada", Expires: now.Add(time.Hour).Unix()})
	if _, ok := service.Identity(request(valid)); !ok {
		t.Fatal("a valid session was refused")
	}
	forged, _ := service.seal(Identity{Provider: "github", Subject: "43", Login: "eve", Expires: now.Add(time.Hour).Unix()})
	payload, _, _ := strings.Cut(forged, ".")
	_, signature, _ := strings.Cut(valid, ".")
	expired, _ := service.seal(Identity{Provider: "github", Subject: "42", Login: "ada", Expires: now.Add(-time.Second).Unix()})
	removed, _ := service.seal(Identity{Provider: "github", Subject: "44", Login: "bob", Expires: now.Add(time.Hour).Unix()})
	for name, value := range map[string]string{"tampered": payload + "." + signature, "expired": expired, "not allowed": removed, "garbage": "x"} {
		if _, ok := service.Identity(request(value)); ok {
			t.Errorf("%s session accepted", name)
		}
	}
}

func TestNewRefusesATownOpenToEveryone(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	github := &Provider{ClientID: "c", ClientSecret: "s"}
	for name, cfg := range map[string]Config{
		"no allowlist":          {Origin: "https://town.example.com", GitHub: github, Key: key},
		"no provider":           {Origin: "https://town.example.com", Allow: Allow{Emails: []string{"a@b.c"}}, Key: key},
		"org without github":    {Origin: "https://town.example.com", Google: github, Allow: Allow{GitHubOrgs: []string{"x"}}, Key: key},
		"domain without google": {Origin: "https://town.example.com", GitHub: github, Allow: Allow{GoogleDomains: []string{"x"}}, Key: key},
		"no origin":             {GitHub: github, Allow: Allow{GitHubUsers: []string{"a"}}, Key: key},
		"short key":             {Origin: "https://town.example.com", GitHub: github, Allow: Allow{GitHubUsers: []string{"a"}}, Key: []byte("short")},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSecureOriginsUseHostPrefixedCookies(t *testing.T) {
	service, err := New(Config{Origin: "https://town.example.com", GitHub: &Provider{ClientID: "c", ClientSecret: "s"}, Allow: Allow{GitHubUsers: []string{"a"}}, Key: []byte(strings.Repeat("k", 32))})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/auth/login/github", nil)
	request.SetPathValue("provider", "github")
	service.begin(recorder, request)
	cookie := recorder.Result().Cookies()[0]
	if cookie.Name != "__Host-brokk-town-login" || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" {
		t.Fatalf("cookie %+v", cookie)
	}
	location, _ := url.Parse(recorder.Header().Get("Location"))
	if location.Query().Get("redirect_uri") != "https://town.example.com/auth/callback/github" || location.Query().Get("scope") != "read:user" {
		t.Fatalf("authorize %s", location)
	}
}

func TestEnvironmentReadsSecretsOnlyFromFiles(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "github")
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{EnvGitHubClientID: "id", EnvGitHubClientSecretFile: secret, EnvAllowGitHubOrgs: "brokkai, other", EnvAllowEmails: "a@b.c"}
	cfg, err := FromEnvironment("https://t.example.com", nil, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHub.ClientSecret != "s3cret" || cfg.Google != nil || len(cfg.Allow.GitHubOrgs) != 2 || cfg.Allow.Emails[0] != "a@b.c" {
		t.Fatalf("config %+v", cfg)
	}
	env["BROKK_TOWN_GITHUB_CLIENT_SECRET"] = "inline"
	if _, err := FromEnvironment("https://t.example.com", nil, func(k string) string { return env[k] }); err == nil {
		t.Fatal("a secret in the environment was accepted")
	}
	delete(env, "BROKK_TOWN_GITHUB_CLIENT_SECRET")
	delete(env, EnvGitHubClientSecretFile)
	if _, err := FromEnvironment("https://t.example.com", nil, func(k string) string { return env[k] }); err == nil {
		t.Fatal("a client ID without a secret was accepted")
	}
}

func TestSessionKeyPersists(t *testing.T) {
	dir := t.TempDir()
	first, err := SessionKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SessionKey(dir)
	if err != nil || string(first) != string(second) || len(first) != 32 {
		t.Fatalf("keys differ or short: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "session-key")); info.Mode().Perm() != 0600 && os.PathSeparator == '/' {
		t.Fatalf("mode %v", info.Mode())
	}
}

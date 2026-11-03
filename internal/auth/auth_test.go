package auth

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeGitHub answers the three requests sign-in makes. tokens maps an
// authorization code to the login it signs in; members lists who is an active
// member of which organization.
type fakeGitHub struct {
	logins  map[string]string
	members map[string][]string
	secret  string
}

func (f *fakeGitHub) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("client_secret") != f.secret || r.FormValue("client_id") != "client-1" {
			http.Error(w, "bad client", http.StatusUnauthorized)
			return
		}
		login, ok := f.logins[r.FormValue("code")]
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_" + login})
	})
	mux.HandleFunc("GET /api/user", func(w http.ResponseWriter, r *http.Request) {
		login, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer gho_")
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
	})
	mux.HandleFunc("GET /api/user/memberships/orgs/{org}", func(w http.ResponseWriter, r *http.Request) {
		login := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer gho_")
		for _, member := range f.members[r.PathValue("org")] {
			if member == login {
				_ = json.NewEncoder(w).Encode(map[string]string{"state": "active"})
				return
			}
		}
		http.NotFound(w, r)
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

func service(t *testing.T, gh *httptest.Server, users, orgs []string) *Service {
	t.Helper()
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("shh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{GitHub: &GitHubConfig{ClientID: "client-1", ClientSecretFile: secret, Users: users, Organizations: orgs, URL: gh.URL, APIURL: gh.URL + "/api"}}, "https://town.example", dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Client = gh.Client()
	return s
}

// signIn runs the whole flow for one authorization code and returns the
// callback's response.
func signIn(t *testing.T, s *Service, code string) *httptest.ResponseRecorder {
	t.Helper()
	h := s.Handler()
	start := httptest.NewRecorder()
	h.ServeHTTP(start, httptest.NewRequest("GET", "https://town.example/auth/login", nil))
	if start.Code != http.StatusFound {
		t.Fatalf("login answered %d", start.Code)
	}
	to, _ := url.Parse(start.Header().Get("Location"))
	if to.Query().Get("redirect_uri") != "https://town.example/auth/callback" || to.Query().Get("client_id") != "client-1" {
		t.Fatalf("authorize URL %s", to)
	}
	back := httptest.NewRequest("GET", "https://town.example/auth/callback?code="+code+"&state="+to.Query().Get("state"), nil)
	for _, c := range start.Result().Cookies() {
		back.AddCookie(c)
	}
	done := httptest.NewRecorder()
	h.ServeHTTP(done, back)
	return done
}

func sessionOf(t *testing.T, s *Service, w *httptest.ResponseRecorder) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", "https://town.example/api/state", nil)
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-brokk-town-session" && c.MaxAge > 0 {
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
				t.Fatalf("session cookie %+v", c)
			}
			r.AddCookie(c)
		}
	}
	return r
}

func TestAListedUserSignsInAndStaysSignedIn(t *testing.T) {
	gh := (&fakeGitHub{logins: map[string]string{"c1": "Alice"}, secret: "shh"}).serve(t)
	s := service(t, gh, []string{"alice"}, nil)
	done := signIn(t, s, "c1")
	if done.Code != http.StatusFound || done.Header().Get("Location") != "/" {
		t.Fatalf("callback answered %d: %s", done.Code, done.Body)
	}
	login, ok := s.Login(sessionOf(t, s, done))
	if !ok || login != "Alice" {
		t.Fatalf("login %q %v", login, ok)
	}
	s.now = func() time.Time { return time.Now().Add(SessionLifetime + time.Minute) }
	if _, ok := s.Login(sessionOf(t, s, done)); ok {
		t.Fatal("an expired session was accepted")
	}
}

func TestOrganizationMembershipAdmitsAndOutsidersAreRefused(t *testing.T) {
	gh := (&fakeGitHub{logins: map[string]string{"c1": "bob", "c2": "mallory"}, members: map[string][]string{"brokkai": {"bob"}}, secret: "shh"}).serve(t)
	s := service(t, gh, nil, []string{"BrokkAi"})
	if login, ok := s.Login(sessionOf(t, s, signIn(t, s, "c1"))); !ok || login != "bob" {
		t.Fatalf("member refused: %q %v", login, ok)
	}
	refused := signIn(t, s, "c2")
	if refused.Code != http.StatusForbidden || !strings.Contains(refused.Body.String(), "mallory") {
		t.Fatalf("outsider answered %d: %s", refused.Code, refused.Body)
	}
	if _, ok := s.Login(sessionOf(t, s, refused)); ok {
		t.Fatal("an outsider got a session")
	}
}

func TestSignInRejectsAForeignStateAndATamperedSession(t *testing.T) {
	gh := (&fakeGitHub{logins: map[string]string{"c1": "alice"}, secret: "shh"}).serve(t)
	s := service(t, gh, []string{"alice"}, nil)
	forged := httptest.NewRequest("GET", "https://town.example/auth/callback?code=c1&state=guess", nil)
	forged.AddCookie(&http.Cookie{Name: "__Host-brokk-town-sign-in", Value: "other"})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, forged)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a mismatched state answered %d", w.Code)
	}
	r := sessionOf(t, s, signIn(t, s, "c1"))
	c, _ := r.Cookie("__Host-brokk-town-session")
	body, sig, _ := strings.Cut(c.Value, ".")
	payload, _ := json.Marshal(session{Login: "admin", Expires: time.Now().Add(time.Hour).Unix()})
	for _, value := range []string{strings.ToUpper(body) + "." + sig, base64.RawURLEncoding.EncodeToString(payload) + "." + sig, "garbage"} {
		tampered := httptest.NewRequest("GET", "https://town.example/api/state", nil)
		tampered.AddCookie(&http.Cookie{Name: "__Host-brokk-town-session", Value: value})
		if login, ok := s.Login(tampered); ok {
			t.Fatalf("tampered session %q accepted as %q", value, login)
		}
	}
	// A login no longer listed is signed out at its next request.
	s.users = nil
	if _, ok := s.Login(r); ok {
		t.Fatal("a removed user kept their session")
	}
}

func TestLogoutNeedsTownsOwnPage(t *testing.T) {
	gh := (&fakeGitHub{secret: "shh"}).serve(t)
	s := service(t, gh, []string{"alice"}, nil)
	for origin, want := range map[string]int{"": http.StatusForbidden, "https://evil.example": http.StatusForbidden, "https://town.example": http.StatusOK} {
		r := httptest.NewRequest("POST", "https://town.example/auth/logout", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("origin %q answered %d", origin, w.Code)
		}
	}
}

func TestSignInConfiguration(t *testing.T) {
	good := GitHubConfig{ClientID: "id", ClientSecretFile: "/run/secret", Users: []string{"alice"}}
	for name, tc := range map[string]struct {
		edit func(*GitHubConfig)
		ok   bool
	}{
		"valid":        {func(*GitHubConfig) {}, true},
		"no client":    {func(g *GitHubConfig) { g.ClientID = "" }, false},
		"no secret":    {func(g *GitHubConfig) { g.ClientSecretFile = "" }, false},
		"nobody":       {func(g *GitHubConfig) { g.Users = nil }, false},
		"bad login":    {func(g *GitHubConfig) { g.Users = []string{"a b"} }, false},
		"org only":     {func(g *GitHubConfig) { g.Users, g.Organizations = nil, []string{"BrokkAi"} }, true},
		"http GitHub":  {func(g *GitHubConfig) { g.URL = "http://github.example" }, false},
		"https GitHub": {func(g *GitHubConfig) { g.URL, g.APIURL = "https://github.example", "https://github.example/api/v3" }, true},
	} {
		g := good
		tc.edit(&g)
		if err := (Config{GitHub: &g}).Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := (Config{Admins: []string{"not a login"}, GitHub: &good}).Validate(); err == nil || !strings.Contains(err.Error(), "auth.admins") {
		t.Errorf("an invalid admin: %v", err)
	}
	if (Config{}).Validate() == nil {
		t.Error("an auth object without github was accepted")
	}
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	_ = os.WriteFile(secret, []byte("shh"), 0600)
	good.ClientSecretFile = secret
	for origin, ok := range map[string]bool{"https://town.example": true, "http://127.0.0.1:8080": true, "http://town.example": false} {
		if _, err := New(Config{GitHub: &good}, origin, dir); (err == nil) != ok {
			t.Errorf("origin %s: %v", origin, err)
		}
	}
	key, err := os.Stat(filepath.Join(dir, "session-key"))
	if err != nil || (runtime.GOOS != "windows" && key.Mode().Perm() != 0600) {
		t.Fatalf("session key %v %v", key, err)
	}
}

// An Enterprise Server with a self-signed certificate is trusted through
// ca_file alone; without it sign-in cannot reach GitHub.
func TestCAFileTrustsASelfSignedGitHub(t *testing.T) {
	gh := (&fakeGitHub{logins: map[string]string{"c1": "alice"}, secret: "shh"}).serve(t)
	dir := t.TempDir()
	secret, ca := filepath.Join(dir, "secret"), filepath.Join(dir, "ca.pem")
	_ = os.WriteFile(secret, []byte("shh"), 0600)
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: gh.Certificate().Raw}), 0600)
	for _, trusted := range []bool{false, true} {
		g := &GitHubConfig{ClientID: "client-1", ClientSecretFile: secret, Users: []string{"alice"}, URL: gh.URL, APIURL: gh.URL + "/api"}
		if trusted {
			g.CAFile = ca
		}
		s, err := New(Config{GitHub: g}, "https://town.example", dir)
		if err != nil {
			t.Fatal(err)
		}
		done := signIn(t, s, "c1")
		if _, ok := s.Login(sessionOf(t, s, done)); ok != trusted {
			t.Fatalf("ca_file set %v: signed in %v (%d)", trusted, ok, done.Code)
		}
	}
	_ = os.WriteFile(ca, []byte("not a certificate"), 0600)
	if _, err := New(Config{GitHub: &GitHubConfig{ClientID: "id", ClientSecretFile: secret, Users: []string{"alice"}, CAFile: ca}}, "https://town.example", dir); err == nil || !strings.Contains(err.Error(), "no PEM certificate") {
		t.Fatalf("a bad ca_file: %v", err)
	}
}

// Plain http on a non-loopback address needs the explicit opt-in, and its
// cookies are then sent without the Secure attribute or the __Host- prefix,
// which a browser would refuse over http.
func TestPlainHTTPNeedsTheOptIn(t *testing.T) {
	gh := (&fakeGitHub{logins: map[string]string{"c1": "alice"}, secret: "shh"}).serve(t)
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	_ = os.WriteFile(secret, []byte("shh"), 0600)
	c := Config{GitHub: &GitHubConfig{ClientID: "client-1", ClientSecretFile: secret, Users: []string{"alice"}, URL: gh.URL, APIURL: gh.URL + "/api"}}
	if _, err := New(c, "http://town.internal:8080", dir); err == nil || !strings.Contains(err.Error(), "allow_insecure_http") {
		t.Fatalf("plain http without the opt-in: %v", err)
	}
	c.AllowInsecureHTTP = true
	s, err := New(c, "http://town.internal:8080", dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Client = gh.Client()
	h := s.Handler()
	start := httptest.NewRecorder()
	h.ServeHTTP(start, httptest.NewRequest("GET", "http://town.internal:8080/auth/login", nil))
	to, _ := url.Parse(start.Header().Get("Location"))
	if to.Query().Get("redirect_uri") != "http://town.internal:8080/auth/callback" {
		t.Fatalf("redirect_uri %s", to.Query().Get("redirect_uri"))
	}
	back := httptest.NewRequest("GET", "http://town.internal:8080/auth/callback?code=c1&state="+to.Query().Get("state"), nil)
	for _, cookie := range start.Result().Cookies() {
		back.AddCookie(cookie)
	}
	done := httptest.NewRecorder()
	h.ServeHTTP(done, back)
	r := httptest.NewRequest("GET", "http://town.internal:8080/api/state", nil)
	for _, cookie := range done.Result().Cookies() {
		if cookie.Secure || strings.HasPrefix(cookie.Name, "__Host-") {
			t.Fatalf("cookie %+v cannot be set over http", cookie)
		}
		if cookie.Name == "brokk-town-session" {
			r.AddCookie(cookie)
		}
	}
	if login, ok := s.Login(r); !ok || login != "alice" {
		t.Fatalf("login %q %v", login, ok)
	}
}

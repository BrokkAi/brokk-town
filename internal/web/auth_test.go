package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/auth"
	"github.com/BrokkAi/brokk-town/internal/town"
)

const publicOrigin = "https://town.example"

// fakeAccess answers GitHub's role for repo and login, keyed "repo\x00login";
// anything unlisted has no access. A key mapped to "error" fails the lookup.
type fakeAccess map[string]string

func (f fakeAccess) Access(_ context.Context, repo, login string) (town.RepoAccess, error) {
	role := f[repo+"\x00"+login]
	if role == "error" {
		return town.RepoAccess{}, errors.New("GitHub is unavailable")
	}
	if role == "" {
		return town.RepoAccess{Permission: "none"}, nil
	}
	return town.RepoAccess{RoleName: role}, nil
}

// signedInServer is a Town reached at an https address that admits alice,
// an administrator of acme/a on GitHub, and her session cookie.
func signedInServer(t *testing.T) (*Server, *http.Cookie) {
	t.Helper()
	s, session := townWithSignIn(t, fakeAccess{"acme/a\x00alice": "admin"}, nil, "alice")
	return s, session("alice")
}

// townWithSignIn is a Town reached at an https address that admits users
// through a fake GitHub, reads their repository access from access and has
// admins as its administrators. session signs one of them in.
func townWithSignIn(t *testing.T, access fakeAccess, admins []string, users ...string) (*Server, func(login string) *http.Cookie) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_" + r.FormValue("code")})
	})
	mux.HandleFunc("GET /api/user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"login": strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer gho_")})
	})
	gh := httptest.NewTLSServer(mux)
	t.Cleanup(gh.Close)
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("shh"), 0600); err != nil {
		t.Fatal(err)
	}
	signIn, err := auth.New(auth.Config{Admins: admins, GitHub: &auth.GitHubConfig{ClientID: "id", ClientSecretFile: secret, Users: users, URL: gh.URL, APIURL: gh.URL + "/api"}}, publicOrigin, dir)
	if err != nil {
		t.Fatal(err)
	}
	signIn.Client = gh.Client()
	store, err := town.Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := &Server{Store: store, Token: "test-key", Supervisor: town.NewSupervisor(store, nil, nil), Origin: publicOrigin, Auth: signIn, Access: access}
	return s, func(login string) *http.Cookie {
		t.Helper()
		start := serve(s, "GET", "/auth/login", "", nil)
		to, _ := url.Parse(start.Header().Get("Location"))
		back := httptest.NewRequest("GET", publicOrigin+"/auth/callback?code="+login+"&state="+to.Query().Get("state"), nil)
		for _, c := range start.Result().Cookies() {
			back.AddCookie(c)
		}
		done := httptest.NewRecorder()
		s.Handler().ServeHTTP(done, back)
		for _, c := range done.Result().Cookies() {
			if c.Name == "__Host-brokk-town-session" && c.Value != "" {
				return c
			}
		}
		t.Fatalf("no session for %s: %d %s", login, done.Code, done.Body)
		return nil
	}
}

// serve sends one request to the handler as a browser at the public address
// would. headers holds Authorization, Origin and the session cookie.
func serve(s *Server, method, path, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, publicOrigin+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestASignedInPersonActsAsThemselves(t *testing.T) {
	s, session := signedInServer(t)
	page := map[string]string{"Origin": publicOrigin}

	anonymous := serve(s, "GET", "/api/state", "", nil)
	if anonymous.Code != http.StatusUnauthorized || !strings.Contains(anonymous.Body.String(), `"sign_in":"/auth/login"`) {
		t.Fatalf("anonymous request answered %d: %s", anonymous.Code, anonymous.Body)
	}
	me := serve(s, "GET", "/api/session", "", nil, session)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"login":"alice"`) {
		t.Fatalf("session answered %d: %s", me.Code, me.Body)
	}
	// The cookie rides on any request; a write must come from Town's page.
	for _, origin := range []map[string]string{nil, {"Origin": "https://evil.example"}} {
		if w := serve(s, "POST", "/api/towns", `{"repo":"acme/a"}`, origin, session); w.Code != http.StatusForbidden {
			t.Fatalf("a write from origin %v answered %d", origin, w.Code)
		}
	}
	if w := serve(s, "POST", "/api/towns", `{"repo":"acme/a"}`, page, session); w.Code != http.StatusCreated {
		t.Fatalf("add answered %d: %s", w.Code, w.Body)
	}
	if w := serve(s, "POST", "/api/control", `{"town":"acme/a","role":"bug","action":"start"}`, page, session); w.Code != http.StatusOK {
		t.Fatalf("control answered %d: %s", w.Code, w.Body)
	}
	// The access key still works, and records no one.
	if w := serve(s, "POST", "/api/control", `{"town":"acme/a","role":"bug","action":"pause"}`, map[string]string{"Authorization": "Bearer test-key"}); w.Code != http.StatusOK {
		t.Fatalf("access key answered %d: %s", w.Code, w.Body)
	}
	if w := serve(s, "GET", "/api/state", "", map[string]string{"Authorization": "Bearer wrong"}, session); w.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong key with a valid session answered %d", w.Code)
	}
	if w := serve(s, "GET", "/api/state", "", map[string]string{"Authorization": "Basic cHJveHk6cHJveHk="}, session); w.Code != http.StatusOK {
		t.Fatalf("a proxy's own credentials hid a valid session: %d", w.Code)
	}
	var actors []string
	for _, e := range s.Store.Snapshot().Events {
		actors = append(actors, e.Title+"="+e.Actor)
	}
	want := []string{"Repository added; Repo Bot is checking it=alice", "start bug=alice", "pause bug="}
	if strings.Join(actors, "|") != strings.Join(want, "|") {
		t.Fatalf("actors %q, want %q", actors, want)
	}
	public, _ := json.Marshal(s.publicState(httptest.NewRequest("GET", "/", nil))["events"])
	if !strings.Contains(string(public), `"actor":"alice"`) {
		t.Fatalf("public events omit the actor: %s", public)
	}
}

func TestAnHTTPSAddressIsReachedThroughItsProxy(t *testing.T) {
	s, session := signedInServer(t)
	r := httptest.NewRequest("GET", "http://town.example/api/state", nil)
	r.AddCookie(session)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("a proxied request answered %d", w.Code)
	}
	r = httptest.NewRequest("GET", "http://other.example/api/state", nil)
	r.AddCookie(session)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unknown host answered %d", w.Code)
	}
	body, _ := io.ReadAll(serve(s, "GET", "/", "", nil).Result().Body)
	if !strings.Contains(string(body), `href="/auth/login"`) {
		t.Fatal("the page offers no sign-in")
	}
}

func TestWithoutSignInTheAccessKeyIsTheOnlyWayIn(t *testing.T) {
	_, h := fixture(t)
	r := call(t, h.URL, "GET", "/auth/login", "", "", "")
	if r.StatusCode == http.StatusFound {
		t.Fatal("a Town without sign-in started one")
	}
	r = call(t, h.URL, "GET", "/api/session", "", "test-key", "")
	var session struct {
		Login  string `json:"login"`
		SignIn bool   `json:"sign_in"`
	}
	if err := json.NewDecoder(r.Body).Decode(&session); err != nil || session.Login != "" || session.SignIn {
		t.Fatalf("session %+v %v", session, err)
	}
}

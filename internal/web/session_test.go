package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/auth"
	"github.com/BrokkAi/brokk-town/internal/town"
)

// sharedFixture is a Town with one repository that admits @ada and @grace.
func sharedFixture(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, httpServer := fixture(t)
	if err := s.Store.Update(func(st *town.State) error { _, e := st.Add(town.DefaultConfig("acme/project")); return e }); err != nil {
		t.Fatal(err)
	}
	return s, httpServer
}

// as sends a request with a signed-in person's session cookie.
func as(t *testing.T, s *Server, login, method, path, body, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, "http://"+strings.TrimPrefix(s.Origin, "http://")+path, strings.NewReader(body))
	if login != "" {
		cookie, err := s.Auth.Session(auth.Identity{Provider: "github", Subject: login, Login: login, Name: strings.ToUpper(login[:1]) + login[1:]})
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	return r
}

func status(t *testing.T, r *http.Response, want int) {
	t.Helper()
	if r.StatusCode != want {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("%s %s: got %d want %d: %s", r.Request.Method, r.Request.URL.Path, r.StatusCode, want, b)
	}
}

func TestSessionEndpointTellsTheBrowserHowToSignIn(t *testing.T) {
	s, _ := sharedFixture(t)
	var anonymous struct {
		Providers []string          `json:"providers"`
		User      map[string]string `json:"user"`
	}
	r := as(t, s, "", "GET", "/api/session", "", "")
	status(t, r, 200)
	if json.NewDecoder(r.Body).Decode(&anonymous) != nil || len(anonymous.Providers) != 1 || anonymous.Providers[0] != "github" || anonymous.User != nil {
		t.Fatalf("anonymous session %+v", anonymous)
	}
	r = as(t, s, "ada", "GET", "/api/session", "", "")
	var signed struct {
		User map[string]string `json:"user"`
	}
	if json.NewDecoder(r.Body).Decode(&signed) != nil || signed.User["display"] != "Ada (@ada)" {
		t.Fatalf("signed-in session %+v", signed)
	}
}

func TestSignedInPeopleShareOneTownAndAreNamedInActivity(t *testing.T) {
	s, _ := sharedFixture(t)
	status(t, as(t, s, "", "GET", "/api/state", "", ""), 401)
	status(t, as(t, s, "mallory", "GET", "/api/state", "", ""), 401)
	status(t, as(t, s, "grace", "GET", "/api/state", "", ""), 200)

	pause := `{"town":"acme/project","role":"bug","action":"start"}`
	// A cookie rides along with any request the browser makes; a write must
	// carry this page's Origin.
	status(t, as(t, s, "ada", "POST", "/api/control", pause, ""), 403)
	status(t, as(t, s, "ada", "POST", "/api/control", pause, "http://evil.example"), 403)
	status(t, as(t, s, "ada", "POST", "/api/control", pause, s.Origin), 200)

	var started *town.Event
	for _, e := range s.Store.Snapshot().Events {
		if e.Kind == "control" {
			started = &e
		}
	}
	if started == nil || started.Actor != "Ada (@ada)" {
		t.Fatalf("the change is not attributed: %+v", started)
	}

	// The access key keeps working for the bt CLI, unattributed.
	status(t, call(t, "http://"+strings.TrimPrefix(s.Origin, "http://"), "POST", "/api/control", `{"town":"acme/project","role":"bug","action":"pause"}`, "test-key", ""), 200)
	events := s.Store.Snapshot().Events
	if last := events[len(events)-1]; last.Kind != "control" || last.Actor != "" {
		t.Fatalf("operator change attributed to %q", last.Actor)
	}
}

func TestSharedAssistantConversationNamesWhoAsked(t *testing.T) {
	s, _ := sharedFixture(t)
	ask := func(login, id string, sequence int) *http.Response {
		body := `{"town":"acme/project","action":"ask","id":"` + id + `","sequence":` + string(rune('0'+sequence)) + `,"question":"What is blocked?"}`
		return as(t, s, login, "POST", "/api/guide", body, s.Origin)
	}
	status(t, ask("ada", "question-ada", 0), 200)
	// Grace asked from a page that had not yet seen Ada's question.
	r := ask("grace", "question-grace", 0)
	status(t, r, 400)
	status(t, as(t, s, "ada", "POST", "/api/guide", `{"town":"acme/project","action":"cancel","id":"question-ada"}`, s.Origin), 200)
	r = ask("grace", "question-grace", 1)
	status(t, r, 200)
	var conversation town.GuideConversation
	if json.NewDecoder(r.Body).Decode(&conversation) != nil || len(conversation.Turns) != 2 {
		t.Fatalf("conversation %+v", conversation)
	}
	if conversation.Turns[0].Author != "Ada (@ada)" || conversation.Turns[1].Author != "Grace (@grace)" {
		t.Fatalf("authors %q, %q", conversation.Turns[0].Author, conversation.Turns[1].Author)
	}
}

func TestSignedInPeopleCannotRunCommandsOnTownsHosts(t *testing.T) {
	s, _ := sharedFixture(t)
	for _, tc := range []struct{ path, body string }{
		{"/api/settings", `{"town":"acme/project","agent":{"command":["sh","-c","id"]}}`},
		{"/api/choices", `{"town":"acme/project","role":"bug","agent":{"command":["sh","-c","id"]}}`},
		{"/api/towns", `{"repo":"acme/other","agent":{"command":["sh","-c","id"]}}`},
		{"/api/attention-hook", `{"enabled":true,"command":["sh","-c","id"]}`},
	} {
		status(t, as(t, s, "ada", "POST", tc.path, tc.body, s.Origin), 403)
	}
	if hook := s.Store.Snapshot().ServiceConfig.AttentionHook; hook.Enabled {
		t.Fatal("a session set the attention hook")
	}
	// Choosing settings without a command stays open to everyone.
	status(t, as(t, s, "ada", "POST", "/api/settings", `{"town":"acme/project","merge_policy":"manual"}`, s.Origin), 200)
}

func TestAPublishedHTTPSOriginAcceptsItsProxiedHost(t *testing.T) {
	store, err := town.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := &Server{Store: store, Token: "test-key", Supervisor: town.NewSupervisor(store, nil, nil), Origin: "https://town.example.com", Auth: testAuth(t, "https://town.example.com")}
	handler := s.Handler()
	for host, want := range map[string]int{"town.example.com": 200, "evil.example.com": 403} {
		req := httptest.NewRequest("GET", "http://"+host+"/api/state", nil)
		req.Header.Set("Authorization", "Bearer test-key")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != want {
			t.Fatalf("host %s: %d", host, recorder.Code)
		}
	}
}

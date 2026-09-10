package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func fixture(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	store, e := town.Open(t.TempDir(), false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	s := &Server{Store: store, Token: "test-key", Supervisor: town.NewSupervisor(store, nil, nil)}
	httpServer := httptest.NewUnstartedServer(s.Handler())
	s.Origin = "http://" + httpServer.Listener.Addr().String()
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return s, httpServer
}
func call(t *testing.T, base, method, path, body, token, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", "application/json")
	r, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { r.Body.Close() })
	return r
}
func TestLocalAPIAuthenticationOriginAndStrictInput(t *testing.T) {
	s, h := fixture(t)
	for _, tc := range []struct {
		path, method, token, origin, body string
		status                            int
	}{
		{"/api/state", "GET", "", "", "", 401},
		{"/api/state", "GET", "wrong", "", "", 401},
		{"/api/state", "GET", "test-key", "http://evil.example", "", 403},
		{"/api/state", "GET", "test-key", "", "", 200},
		{"/api/towns", "POST", "test-key", "", `{"repo":"acme/a","injected":true}`, 400},
		{"/api/towns", "POST", "test-key", "", `{"repo":"acme/a"} {}`, 400},
		{"/api/towns", "POST", "test-key", "", `{"repo":"acme/a"}`, 201},
		{"/api/towns", "POST", "test-key", "", `{"repo":"acme/b"}`, 201},
		{"/api/control", "POST", "test-key", "", `{"town":"acme/a","role":"bug","action":"start"}`, 200},
		{"/api/control", "POST", "test-key", "", `{"town":"acme/a","role":"wat","action":"start"}`, 400},
		{"/api/control", "POST", "test-key", "", `{"town":"acme/a","role":"bug","action":"launch"}`, 400},
	} {
		r := call(t, h.URL, tc.method, tc.path, tc.body, tc.token, tc.origin)
		if r.StatusCode != tc.status {
			b, _ := io.ReadAll(r.Body)
			t.Fatalf("%s got %d want %d: %s", tc.path, r.StatusCode, tc.status, b)
		}
	}
	state := s.Store.Snapshot()
	if !state.Towns["acme/a"].Workers[town.Bug].Enabled || state.Towns["acme/b"].Workers[town.Bug].Enabled {
		t.Fatal("town control not isolated")
	}
	req := httptest.NewRequest("GET", h.URL+"/api/state", nil)
	req.Host = "evil.example"
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("DNS rebinding host accepted")
	}
	for _, path := range []string{"/", "/app.js", "/town.js", "/tools.js", "/manage.js", "/scenery.js", "/assets/buildings-atlas.png", "/assets/actors-atlas.png"} {
		r := call(t, h.URL, "GET", path, "", "", "")
		if r.StatusCode != 200 {
			t.Fatal("missing embedded asset", path)
		}
		if r.Header.Get("Content-Security-Policy") == "" {
			t.Fatal("missing CSP")
		}
	}
}

func TestManagementAPIsAreAuthenticatedStrictAndPersisted(t *testing.T) {
	s, h := fixture(t)
	for _, path := range []string{"/api/settings", "/api/choices", "/api/requests", "/api/requests/check"} {
		if r := call(t, h.URL, "POST", path, `{}`, "", ""); r.StatusCode != 401 {
			t.Fatal(path, "missing authorization")
		}
		if r := call(t, h.URL, "POST", path, `{"injected":true}`, "test-key", ""); r.StatusCode != 400 {
			t.Fatal(path, "not strict")
		}
	}
	r := call(t, h.URL, "POST", "/api/towns", `{"repo":"acme/managed","agent":{"harness":"custom","command":["fake","private-argument"],"model":"chosen","effort":"high"}}`, "test-key", "")
	if r.StatusCode != 201 {
		t.Fatal(r.Status)
	}
	r = call(t, h.URL, "POST", "/api/settings", `{"town":"acme/managed","agent":{"effort":"low"}}`, "test-key", "")
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
	config := s.Store.Snapshot().Towns["acme/managed"].Config
	if config.Agent.Model != "chosen" || config.Agent.Effort != "low" || config.Agent.Command[1] != "private-argument" {
		t.Fatal(config)
	}
	r = call(t, h.URL, "GET", "/api/state", "", "test-key", "")
	data, _ := io.ReadAll(r.Body)
	if strings.Contains(string(data), "private-argument") || !strings.Contains(string(data), `"model":"chosen"`) {
		t.Fatal("incorrect public settings", string(data))
	}
	r = call(t, h.URL, "POST", "/api/control", `{"town":"acme/managed","role":"all","action":"delete"}`, "test-key", "")
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
	r = call(t, h.URL, "GET", "/api/state", "", "test-key", "")
	data, _ = io.ReadAll(r.Body)
	if strings.Contains(string(data), "acme/managed") {
		t.Fatal("deleted town or events still visible", string(data))
	}
	r = call(t, h.URL, "POST", "/api/settings", `{"town":"acme/managed","agent":{"model":"new"}}`, "test-key", "")
	if r.StatusCode != 400 {
		t.Fatal("updated deleted town")
	}
	if !s.Store.Snapshot().Towns["acme/managed"].Deleted {
		t.Fatal("deleted recovery record not retained")
	}
}

type issuePublisher struct{}

func (issuePublisher) CreateIssue(context.Context, string, string, string) (town.RemoteIssue, error) {
	panic("HTTP handler must enqueue, not post")
}
func (issuePublisher) FindRequest(context.Context, string, string) (*town.RemoteIssue, error) {
	panic("HTTP handler must enqueue, not reconcile")
}

func TestRequestAPIQueuesExactlyOneIntentBeforeResponding(t *testing.T) {
	s, h := fixture(t)
	s.Supervisor.Publisher = issuePublisher{}
	if r := call(t, h.URL, "POST", "/api/towns", `{"repo":"acme/managed"}`, "test-key", ""); r.StatusCode != 201 {
		t.Fatal(r.Status)
	}
	body := `{"town":"acme/managed","id":"12345678123456781234567812345678","kind":"bug","title":"Escape <html>","body":"Steps:\n1. Paste literal $(echo test) and ` + "`code`" + `.\n2. Observe the issue."}`
	for range 2 {
		r := call(t, h.URL, "POST", "/api/requests", body, "test-key", "")
		if r.StatusCode != 202 {
			data, _ := io.ReadAll(r.Body)
			t.Fatal(r.Status, string(data))
		}
		var request town.IssueRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Status != "queued" {
			t.Fatal(request, err)
		}
	}
	st := s.Store.Snapshot().Towns["acme/managed"]
	if len(st.Requests) != 1 || st.Workers[town.Issue].Enabled {
		t.Fatal("duplicate submission or implicit automation")
	}
	if r := call(t, h.URL, "POST", "/api/requests", strings.Replace(body, `"kind":"bug"`, `"kind":"invalid"`, 1), "test-key", ""); r.StatusCode != 400 {
		t.Fatal("invalid kind accepted")
	}
}
func TestEventStreamSnapshotAndCommittedUpdate(t *testing.T) {
	s, h := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	r, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	reader := bufio.NewReader(r.Body)
	read := func() town.State {
		t.Helper()
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(line, "data: ") {
				var st town.State
				if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &st); e != nil {
					t.Fatal(e)
				}
				return st
			}
		}
	}
	if read().Seq != 0 {
		t.Fatal("unexpected initial cursor")
	}
	e = s.Store.Update(func(st *town.State) error {
		x, e := st.Add(town.DefaultConfig("acme/a"))
		if e != nil {
			return e
		}
		st.Event(x.ID, "town", "operator", "repo", "", "Created", time.Now())
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	next := read()
	if next.Seq != 1 || next.Towns["acme/a"] == nil {
		t.Fatal("stream did not reflect committed update")
	}
	cancel()
}

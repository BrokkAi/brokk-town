package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BrokkAi/brokk-town/internal/harness"
	"github.com/BrokkAi/brokk-town/internal/town"
)

func fixture(t *testing.T) (*Server, *httptest.Server) {
	return fixtureMode(t, false)
}

func fixtureMode(t *testing.T, demo bool) (*Server, *httptest.Server) {
	t.Helper()
	store, e := town.Open(t.TempDir(), demo)
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
	for _, path := range []string{"/", "/app.js", "/town.js", "/tools.js", "/manage.js", "/scenery.js", "/skins.js", "/frontline.js", "/assets/buildings-atlas.png", "/assets/actors-atlas.png", "/assets/feature-study.png", "/assets/feature-reader.png", "/assets/simplifier-clarifier.png"} {
		r := call(t, h.URL, "GET", path, "", "", "")
		if r.StatusCode != 200 {
			t.Fatal("missing embedded asset", path)
		}
		if r.Header.Get("Content-Security-Policy") == "" {
			t.Fatal("missing CSP")
		}
	}
}

func TestOutcomeReportExportAndExplicitJudgment(t *testing.T) {
	s, h := fixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.Store.Update(func(state *town.State) error {
		current, err := state.Add(town.DefaultConfig("acme/outcomes"))
		if err != nil {
			return err
		}
		current.RecordOutcome(town.OutcomeRecord{ID: "finding-filed:issue:8", At: now, Class: "artifact", Kind: "finding_filed", Status: "confirmed", TaskID: "issue:8", URL: "https://github.com/acme/outcomes/issues/8", Detail: "=2+2"})
		current.RecordOutcome(town.OutcomeRecord{ID: "implementation-pr:pr:9", At: now, Class: "artifact", Kind: "implementation_pr", Status: "submitted", TaskID: "pr:9", RelatedTaskID: "issue:8", Revision: strings.Repeat("a", 40)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	from := now.Add(-time.Minute).Format(time.RFC3339)
	jsonResponse := call(t, h.URL, http.MethodGet, "/api/outcomes?town=acme%2Foutcomes&from="+from, "", "test-key", "")
	if jsonResponse.StatusCode != http.StatusOK {
		t.Fatalf("report status %d", jsonResponse.StatusCode)
	}
	var report town.OutcomeReport
	if err := json.NewDecoder(jsonResponse.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.FindingsFiled != 1 || report.Summary.PRsSubmitted != 1 || report.Summary.MergesConfirmed != 0 || report.Records[0].Town != "acme/outcomes" || report.Records[0].Usage != nil || report.Records[0].CostUSD != nil {
		t.Fatalf("incorrect outcome report: %+v", report)
	}

	csvResponse := call(t, h.URL, http.MethodGet, "/api/outcomes?town=acme%2Foutcomes&from="+from+"&format=csv", "", "test-key", "")
	body, _ := io.ReadAll(csvResponse.Body)
	if csvResponse.StatusCode != http.StatusOK || !strings.Contains(string(body), "town,id,at,class,kind,status,role") || !strings.Contains(string(body), "unknown,unknown,unknown,unjudged") || !strings.Contains(string(body), "'=2+2") {
		t.Fatalf("CSV export missing explicit unknowns: %s", body)
	}

	judged := call(t, h.URL, http.MethodPost, "/api/outcomes/judgment", `{"town":"acme/outcomes","outcome":"finding-filed:issue:8","value":"useful","explanation":"Prevented a production regression"}`, "test-key", "")
	if judged.StatusCode != http.StatusOK {
		t.Fatalf("judgment status %d", judged.StatusCode)
	}
	saved := s.Store.Snapshot().Towns["acme/outcomes"].Outcomes[0].Judgment
	if saved == nil || saved.Value != "useful" || saved.Explanation != "Prevented a production regression" {
		t.Fatalf("judgment not persisted: %+v", saved)
	}
	bad := call(t, h.URL, http.MethodPost, "/api/outcomes/judgment", `{"town":"acme/outcomes","outcome":"finding-filed:issue:8","value":"false_positive","explanation":""}`, "test-key", "")
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty explanation status %d", bad.StatusCode)
	}
}

func TestPublicStateIncludesServiceVersion(t *testing.T) {
	s, h := fixture(t)
	s.Version = "v0.1.2"
	response := call(t, h.URL, http.MethodGet, "/api/state", "", "test-key", "")
	var state struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Version != "v0.1.2" {
		t.Fatalf("service version missing from public state: %+v", state)
	}
}

func TestTaskDetailReadsOneLiveIssueOrPR(t *testing.T) {
	s, h := fixture(t)
	fake := &fakeTaskGitHub{
		issue: town.GitHubIssue{Number: 7, Title: "Outside issue", Body: "What should change.", URL: "https://github.com/acme/managed/issues/7", State: "open", Author: "octo", Comments: 4, UpdatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)},
	}
	pull := town.Pull{Number: 8, Title: "Outside PR", Body: "Ready for review.", URL: "https://github.com/acme/managed/pull/8", State: "open", Comments: 2, ReviewComments: 3, Updated: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)}
	pull.User.Login = "contrib"
	fake.pull = pull
	s.TaskGitHub = fake
	if r := call(t, h.URL, "POST", "/api/towns", `{"repo":"acme/managed"}`, "test-key", ""); r.StatusCode != 201 {
		t.Fatal(r.Status)
	}
	addTask := func(id, kind string, number int) {
		t.Helper()
		if err := s.Store.Update(func(st *town.State) error {
			st.Towns["acme/managed"].Tasks[id] = &town.Task{ID: id, Kind: kind, Number: number, Title: id, House: town.Hall, Stage: "awaiting_mayor", MayoralDecision: "pending"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	addTask("issue:7", "issue", 7)
	addTask("pr:8", "pr", 8)
	if r := call(t, h.URL, "POST", "/api/task-detail", `{}`, "", ""); r.StatusCode != 401 {
		t.Fatal("missing authorization")
	}
	if r := call(t, h.URL, "POST", "/api/task-detail", `{"town":"acme/managed","task":"issue:7","injected":true}`, "test-key", ""); r.StatusCode != 400 {
		t.Fatal("not strict")
	}
	issueResponse := call(t, h.URL, "POST", "/api/task-detail", `{"town":"ACME/managed","task":"issue:7"}`, "test-key", "")
	var issue taskDetailResponse
	if err := json.NewDecoder(issueResponse.Body).Decode(&issue); err != nil {
		t.Fatal(err)
	}
	if issueResponse.StatusCode != 200 || issue.Author != "octo" || issue.Comments != 4 || issue.Body != "What should change." || issue.Truncated {
		t.Fatalf("live issue detail wrong: status=%d %+v", issueResponse.StatusCode, issue)
	}
	prResponse := call(t, h.URL, "POST", "/api/task-detail", `{"town":"acme/managed","task":"pr:8"}`, "test-key", "")
	var pr taskDetailResponse
	if err := json.NewDecoder(prResponse.Body).Decode(&pr); err != nil {
		t.Fatal(err)
	}
	if prResponse.StatusCode != 200 || pr.Author != "contrib" || pr.Comments != 2 || pr.ReviewComments != 3 {
		t.Fatalf("live PR detail wrong: status=%d %+v", prResponse.StatusCode, pr)
	}
	if len(fake.calls) != 2 || fake.calls[0] != "issue:acme/managed:7" || fake.calls[1] != "pr:acme/managed:8" {
		t.Fatalf("unexpected GitHub calls: %v", fake.calls)
	}
	for _, tc := range []struct{ body string }{
		{`{"town":"nope","task":"issue:7"}`},
		{`{"town":"acme/managed","task":"issue:9"}`},
		{`{"town":"acme/managed","task":""}`},
	} {
		if r := call(t, h.URL, "POST", "/api/task-detail", tc.body, "test-key", ""); r.StatusCode < 400 {
			t.Fatalf("%s was accepted", tc.body)
		}
	}
	s.TaskGitHub = nil
	if r := call(t, h.URL, "POST", "/api/task-detail", `{"town":"acme/managed","task":"issue:7"}`, "test-key", ""); r.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("missing GitHub handle was not reported")
	}
}

func TestTaskDetailRefusesDemoTowns(t *testing.T) {
	s, h := fixtureMode(t, true)
	s.TaskGitHub = &fakeTaskGitHub{}
	if r := call(t, h.URL, "POST", "/api/task-detail", `{"town":"acme/demo","task":"issue:1"}`, "test-key", ""); r.StatusCode != http.StatusConflict {
		t.Fatal("demo live details were not refused")
	}
}

func TestTruncateTaskDetailBodyKeepsRunesIntact(t *testing.T) {
	long := strings.Repeat("é", maxTaskDetailBody)
	body, truncated := truncateTaskDetailBody(long)
	if !truncated || len(body) > maxTaskDetailBody || !utf8.ValidString(body) {
		t.Fatalf("multibyte body truncated wrong: bytes=%d truncated=%v", len(body), truncated)
	}
	if body, truncated := truncateTaskDetailBody("short"); truncated || body != "short" {
		t.Fatal("short body changed")
	}
}

type fakeTaskGitHub struct {
	issue town.GitHubIssue
	pull  town.Pull
	calls []string
}

func (f *fakeTaskGitHub) Issue(_ context.Context, repo string, number int) (town.GitHubIssue, error) {
	f.calls = append(f.calls, "issue:"+repo+":"+strconv.Itoa(number))
	return f.issue, nil
}

func (f *fakeTaskGitHub) Pull(_ context.Context, repo string, number int) (town.Pull, error) {
	f.calls = append(f.calls, "pr:"+repo+":"+strconv.Itoa(number))
	return f.pull, nil
}

func TestManagementAPIsAreAuthenticatedStrictAndPersisted(t *testing.T) {
	s, h := fixture(t)
	for _, path := range []string{"/api/settings", "/api/choices", "/api/requests", "/api/requests/check", "/api/harnesses/refresh", "/api/task-detail"} {
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
	r = call(t, h.URL, "POST", "/api/settings", `{"town":"acme/managed","agent":{},"merge_policy":"all","simplifier_mode":"auto"}`, "test-key", "")
	updatedConfig := s.Store.Snapshot().Towns["acme/managed"].Config
	if r.StatusCode != http.StatusOK || updatedConfig.MergePolicy != "all" || updatedConfig.SimplifierModeOrDefault() != "auto" {
		t.Fatal("merge policy was not updated")
	}
	r = call(t, h.URL, "POST", "/api/settings", `{"town":"acme/managed","agent":{},"merge_policy":"unsafe"}`, "test-key", "")
	if r.StatusCode != http.StatusBadRequest || s.Store.Snapshot().Towns["acme/managed"].Config.MergePolicy != "all" {
		t.Fatal("invalid merge policy changed persisted settings")
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

func TestBotProfileAPIsIsolateSettingsAndValidateDiscoveryRole(t *testing.T) {
	s, h := fixture(t)
	if r := call(t, h.URL, "POST", "/api/towns", `{"repo":"acme/team","agent":{"harness":"custom","command":["fake","private-default"],"model":"default"}}`, "test-key", ""); r.StatusCode != 201 {
		t.Fatal(r.Status)
	}
	for _, body := range []string{
		`{"town":"acme/team","role":"review","agent":{"harness":"claude","model":"review-model","effort":"xhigh"}}`,
		`{"town":"acme/team","role":"issue","agent":{"model":"issue-model"}}`,
		`{"town":"acme/team","role":"release","agent":{"harness":"custom","command":["fake-release","private-release"],"model":"release-model"}}`,
		`{"town":"acme/team","agent":{"model":"new-default"}}`,
	} {
		r := call(t, h.URL, "POST", "/api/settings", body, "test-key", "")
		if r.StatusCode != 200 {
			data, _ := io.ReadAll(r.Body)
			t.Fatal(r.Status, string(data))
		}
	}
	c := s.Store.Snapshot().Towns["acme/team"].Config
	if c.Agent.Model != "new-default" || c.ForRole(town.Review).Agent.Model != "review-model" || c.ForRole(town.Issue).Agent.Model != "issue-model" || c.ForRole(town.Release).Agent.Model != "release-model" || c.ForRole(town.Feature).Agent.Model != "new-default" {
		t.Fatal("API changed another bot's settings")
	}
	r := call(t, h.URL, "GET", "/api/state", "", "test-key", "")
	data, _ := io.ReadAll(r.Body)
	if strings.Contains(string(data), "private-") || !strings.Contains(string(data), `"bot_agents"`) || !strings.Contains(string(data), `"inherited":false`) {
		t.Fatal("public profiles omitted or leaked private values", string(data))
	}
	// Invalid roles must fail before a discovery session can start the fake command.
	for _, path := range []string{"/api/settings", "/api/choices"} {
		for _, role := range []string{"outside", "all", "wat"} {
			body := `{"town":"acme/team","role":"` + role + `","agent":{}}`
			if r := call(t, h.URL, "POST", path, body, "test-key", ""); r.StatusCode != 400 {
				t.Fatal(path, "accepted unsupported role", role)
			}
		}
	}
	r = call(t, h.URL, "POST", "/api/settings", `{"town":"acme/team","role":"review","agent":{"inherit":true}}`, "test-key", "")
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
	c = s.Store.Snapshot().Towns["acme/team"].Config
	if c.ForRole(town.Review).Agent.Model != "new-default" || c.ForRole(town.Issue).Agent.Model != "issue-model" {
		t.Fatal("reset did not isolate its target bot")
	}
	t.Run("demo choice discovery", func(t *testing.T) {
		demo, server := fixtureMode(t, true)
		if err := demo.Store.Update(func(st *town.State) error { _, err := st.Add(town.DefaultConfig("acme/team")); return err }); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"review", "issue", "release", "feature", "bug"} {
			r := call(t, server.URL, "POST", "/api/choices", `{"town":"acme/team","role":"`+role+`","agent":{}}`, "test-key", "")
			var choices town.AgentChoices
			if r.StatusCode != 200 || json.NewDecoder(r.Body).Decode(&choices) != nil || len(choices.Models) == 0 {
				t.Fatal("demo role discovery failed", role, r.Status)
			}
		}
	})
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

func TestHarnessCatalogAPIIncludesRegistryAndSupplements(t *testing.T) {
	s, h := fixture(t)
	s.Supervisor.Harnesses = harness.New(t.TempDir(), true)
	if r := call(t, h.URL, "GET", "/api/harnesses", "", "", ""); r.StatusCode != 401 {
		t.Fatal("unauthenticated catalog")
	}
	for _, method := range []string{"GET", "POST"} {
		path, body := "/api/harnesses", ""
		if method == "POST" {
			path, body = path+"/refresh", "{}"
		}
		r := call(t, h.URL, method, path, body, "test-key", "")
		if r.StatusCode != 200 {
			t.Fatal(r.Status)
		}
		var list harness.Listing
		if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, entry := range list.Agents {
			found[entry.ID] = true
		}
		if len(found) < 33 || !list.Demo || list.Source != harness.RegistryURL {
			t.Fatal(list)
		}
		for _, id := range []string{"codex-acp", "brokkai/anvil", "brokkai/muse-acp", "foundev/draupnir"} {
			if !found[id] {
				t.Fatal("missing harness", id)
			}
		}
	}
}

func TestOversizedBodyIsRejectedAsTooLarge(t *testing.T) {
	_, h := fixture(t)
	body := `{"town":"acme/managed","id":"x","kind":"bug","title":"t","body":"` + strings.Repeat("a", 70<<10) + `"}`
	r := call(t, h.URL, "POST", "/api/requests", body, "test-key", "")
	var v struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&v)
	if r.StatusCode != http.StatusRequestEntityTooLarge || v.Error != "request body exceeds the 64 KiB limit" {
		t.Fatal(r.Status, v.Error)
	}
}

func TestCheckRequestDistinguishesUnknownFromSettled(t *testing.T) {
	s, h := fixture(t)
	s.Supervisor.Publisher = issuePublisher{}
	if r := call(t, h.URL, "POST", "/api/towns", `{"repo":"acme/managed"}`, "test-key", ""); r.StatusCode != 201 {
		t.Fatal(r.Status)
	}
	id := "12345678123456781234567812345678"
	if r := call(t, h.URL, "POST", "/api/requests", `{"town":"acme/managed","id":"`+id+`","kind":"bug","title":"t","body":"b"}`, "test-key", ""); r.StatusCode != 202 {
		t.Fatal(r.Status)
	}
	check := func(request string) (int, string) {
		r := call(t, h.URL, "POST", "/api/requests/check", `{"town":"acme/managed","id":"`+request+`"}`, "test-key", "")
		var v struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(r.Body).Decode(&v)
		return r.StatusCode, v.Error
	}
	if status, message := check("ffffffffffffffffffffffffffffffff"); status != http.StatusNotFound || message != "unknown request" {
		t.Fatal(status, message)
	}
	if status, message := check(id); status != http.StatusBadRequest || message != "request does not need reconciliation" {
		t.Fatal(status, message)
	}
}

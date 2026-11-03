package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
)

// A Town with two repositories and one person at each level of acme/a. root
// administers Town and has no access to either repository on GitHub.
func rolesTown(t *testing.T) (*Server, func(string) *http.Cookie) {
	t.Helper()
	access := fakeAccess{
		"acme/a\x00vic":   "triage",
		"acme/a\x00olga":  "maintain",
		"acme/a\x00ada":   "admin",
		"acme/new\x00ada": "admin",
	}
	s, session := townWithSignIn(t, access, []string{"root"}, "vic", "olga", "ada", "root", "nobody")
	key := map[string]string{"Authorization": "Bearer test-key"}
	for _, repo := range []string{"acme/a", "acme/b"} {
		if w := serve(s, "POST", "/api/towns", `{"repo":"`+repo+`"}`, key); w.Code != http.StatusCreated {
			t.Fatalf("add %s: %d %s", repo, w.Code, w.Body)
		}
	}
	return s, session
}

func stateFor(t *testing.T, s *Server, session *http.Cookie) (towns []string, events []string, access map[string]any) {
	t.Helper()
	w := serve(s, "GET", "/api/state", "", nil, session)
	var state struct {
		Towns  map[string]any `json:"towns"`
		Events []town.Event   `json:"events"`
		Access map[string]any `json:"access"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &state) != nil {
		t.Fatalf("state %d %s", w.Code, w.Body)
	}
	for id := range state.Towns {
		towns = append(towns, id)
	}
	for _, e := range state.Events {
		events = append(events, e.Town)
	}
	return towns, events, state.Access
}

func TestEachPersonSeesOnlyTheRepositoriesTheyCanReach(t *testing.T) {
	s, session := rolesTown(t)
	for login, want := range map[string]string{"vic": "acme/a", "olga": "acme/a", "ada": "acme/a", "nobody": "", "root": "acme/a acme/b"} {
		towns, events, access := stateFor(t, s, session(login))
		got := strings.Join(sorted(towns), " ")
		if got != want {
			t.Errorf("%s sees %q, want %q", login, got, want)
		}
		for _, id := range events {
			if !strings.Contains(want, id) {
				t.Errorf("%s sees an event of %s", login, id)
			}
		}
		if (login == "root") != access["admin"] {
			t.Errorf("%s admin = %v", login, access["admin"])
		}
	}
	_, _, access := stateFor(t, s, session("olga"))
	if access["towns"].(map[string]any)["acme/a"] != "operator" {
		t.Fatalf("olga's access %v", access)
	}
}

func TestWhatEachLevelMayDo(t *testing.T) {
	s, session := rolesTown(t)
	page := map[string]string{"Origin": publicOrigin}
	for _, tc := range []struct {
		login, path, body string
		want              int
	}{
		// A viewer reads but does not act; a repository they cannot reach is unknown.
		{"vic", "/api/history", `{"town":"acme/a"}`, http.StatusOK},
		{"vic", "/api/control", `{"town":"acme/a","role":"bug","action":"start"}`, http.StatusForbidden},
		{"vic", "/api/guide", `{"town":"acme/a","action":"read"}`, http.StatusOK},
		{"vic", "/api/history", `{"town":"acme/b"}`, http.StatusNotFound},
		{"vic", "/api/control", `{"town":"acme/b","role":"bug","action":"start"}`, http.StatusNotFound},
		// An operator runs bots but does not change settings or delete.
		{"olga", "/api/control", `{"town":"acme/a","role":"bug","action":"start"}`, http.StatusOK},
		{"olga", "/api/settings", `{"town":"acme/a","agent":{},"merge_policy":"manual"}`, http.StatusForbidden},
		{"olga", "/api/control", `{"town":"acme/a","role":"all","action":"delete"}`, http.StatusForbidden},
		{"olga", "/api/storage", `{"town":"acme/a"}`, http.StatusForbidden},
		// A repository administrator manages that repository, not Town.
		{"ada", "/api/settings", `{"town":"acme/a","agent":{},"merge_policy":"manual"}`, http.StatusOK},
		{"ada", "/api/capacity", `{"max_workers":2}`, http.StatusForbidden},
		{"ada", "/api/quiet-hours", `{"windows":[]}`, http.StatusForbidden},
		{"ada", "/api/towns", `{"repo":"acme/other"}`, http.StatusForbidden},
		{"ada", "/api/towns", `{"repo":"acme/new"}`, http.StatusCreated},
		// A Town administrator does everything.
		{"root", "/api/capacity", `{"max_workers":2}`, http.StatusOK},
		{"root", "/api/control", `{"town":"acme/b","role":"bug","action":"start"}`, http.StatusOK},
	} {
		w := serve(s, "POST", tc.path, tc.body, page, session(tc.login))
		if w.Code != tc.want {
			t.Errorf("%s %s %s: %d %s, want %d", tc.login, tc.path, tc.body, w.Code, strings.TrimSpace(w.Body.String()), tc.want)
		}
	}
	w := serve(s, "GET", "/api/outcomes?format=json", "", nil, session("vic"))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "acme/b") {
		t.Fatalf("vic's outcomes %d %s", w.Code, w.Body)
	}
}

// The live stream is filtered the same way as the snapshot.
func TestTheStreamShowsOnlyReachableRepositories(t *testing.T) {
	s, session := rolesTown(t)
	server := httptest.NewServer(s.Handler())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	req.Host = "town.example"
	req.AddCookie(session("vic"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	lines.Buffer(make([]byte, 1<<20), 1<<20)
	for lines.Scan() {
		data, ok := strings.CutPrefix(lines.Text(), "data: ")
		if !ok {
			continue
		}
		var state struct {
			Towns map[string]any `json:"towns"`
		}
		if err := json.Unmarshal([]byte(data), &state); err != nil || len(state.Towns) != 1 || state.Towns["acme/a"] == nil {
			t.Fatalf("vic's stream %s", data)
		}
		return
	}
	t.Fatal("no snapshot on the stream")
}

func TestGitHubRolesMapToLevels(t *testing.T) {
	for access, want := range map[town.RepoAccess]level{
		{RoleName: "admin"}:                         repoAdmin,
		{RoleName: "maintain"}:                      operator,
		{RoleName: "write"}:                         operator,
		{RoleName: "triage"}:                        viewer,
		{RoleName: "read"}:                          viewer,
		{RoleName: "security", Permission: "write"}: operator,
		{RoleName: "auditor", Permission: "read"}:   viewer,
		{Permission: "none"}:                        noAccess,
		{}:                                          noAccess,
	} {
		if got := levelOf(access); got != want {
			t.Errorf("%+v: %v, want %v", access, got, want)
		}
	}
}

// A level is reused while fresh; when GitHub fails, a recent answer stands for
// a while and then no access does.
func TestAccessIsCachedAndSurvivesABriefOutage(t *testing.T) {
	answers := fakeAccess{"acme/a\x00olga": "write"}
	now := time.Now()
	calls := 0
	c := &accessCache{read: countingAccess{answers, &calls}, now: func() time.Time { return now }, entries: map[string]accessEntry{}}
	ctx := context.Background()
	if c.level(ctx, "acme/a", "Olga") != operator || c.level(ctx, "ACME/A", "olga") != operator || calls != 1 {
		t.Fatalf("not cached: %d calls", calls)
	}
	answers["acme/a\x00olga"] = "error"
	now = now.Add(accessFresh + time.Minute)
	if c.level(ctx, "acme/a", "olga") != operator {
		t.Fatal("a brief outage dropped a recent answer")
	}
	now = now.Add(accessStale)
	if c.level(ctx, "acme/a", "olga") != noAccess {
		t.Fatal("a stale answer outlived the outage limit")
	}
	answers["acme/a\x00olga"] = "read"
	if c.level(ctx, "acme/a", "olga") != viewer {
		t.Fatal("a changed role on GitHub was not picked up")
	}
}

type countingAccess struct {
	answers fakeAccess
	calls   *int
}

func (c countingAccess) Access(ctx context.Context, repo, login string) (town.RepoAccess, error) {
	*c.calls++
	return c.answers.Access(ctx, strings.ToLower(repo), strings.ToLower(login))
}

func sorted(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}

// When two people decide the same task, the second learns who decided first
// instead of overwriting it.
func TestASecondDecisionIsAConflict(t *testing.T) {
	s, session := rolesTown(t)
	if err := s.Store.Update(func(st *town.State) error {
		a := st.Towns["acme/a"]
		a.Initialized = true
		a.Tasks["issue:1"] = &town.Task{ID: "issue:1", Kind: "issue", Number: 1, Title: "Finding", House: town.Hall, Stage: "awaiting_mayor", MayoralDecision: "pending", Updated: time.Now()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page := map[string]string{"Origin": publicOrigin}
	if w := serve(s, "POST", "/api/control", `{"town":"acme/a","role":"hall","action":"admit","task":"issue:1"}`, page, session("olga")); w.Code != http.StatusOK {
		t.Fatalf("olga's decision: %d %s", w.Code, w.Body)
	}
	w := serve(s, "POST", "/api/control", `{"town":"acme/a","role":"hall","action":"decline","task":"issue:1"}`, page, session("ada"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "olga accepted this task.") {
		t.Fatalf("ada's late decision: %d %s", w.Code, w.Body)
	}
	if got := s.Store.Snapshot().Towns["acme/a"].Tasks["issue:1"].MayoralDecision; got != "admitted" {
		t.Fatalf("decision %q", got)
	}
}

package town

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var baseSHA = strings.Repeat("a", 40)
var headSHA = strings.Repeat("b", 40)
var fixSHA = strings.Repeat("c", 40)

func testStore(t *testing.T, demo bool) *Store {
	t.Helper()
	s, e := Open(t.TempDir(), demo)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func update(t *testing.T, s *Store, fn func(*State)) {
	t.Helper()
	if e := s.Update(func(st *State) error { fn(st); return nil }); e != nil {
		t.Fatal(e)
	}
}
func addTown(t *testing.T, s *Store) *Town {
	t.Helper()
	update(t, s, func(st *State) {
		c := DefaultConfig("acme/orchard")
		c.Branch = "main"
		_, e := st.Add(c)
		if e != nil {
			t.Fatal(e)
		}
	})
	return s.Snapshot().Towns["acme/orchard"]
}
func pull(n int) Pull {
	p := Pull{Number: n, Title: fmt.Sprint("Change ", n), State: "open", Head: Ref{Ref: fmt.Sprint("issue-", n), SHA: headSHA}, Base: Ref{Ref: "main", SHA: baseSHA}}
	p.Head.Repo.FullName = "acme/orchard"
	return p
}
func clean() *Audit {
	return &Audit{Base: baseSHA, Head: headSHA, Discussion: Digest([]Discussion{}), Description: description(pull(1)), Verdict: "clean", Complete: true, Summary: "Reviewed entire diff", Checks: []string{"regression passed"}, Findings: []Finding{}, At: time.Now()}
}
func inventory(pulls ...Pull) RepoSnapshot {
	return RepoSnapshot{Branch: "main", Head: baseSHA, Pulls: pulls}
}
func setupPR(t *testing.T, s *Store, n int) *Town {
	addTown(t, s)
	update(t, s, func(st *State) {
		x := st.Towns["acme/orchard"]
		x.Owned[n] = Ownership{pull(n).Head.Ref, n}
		Reconcile(st, x, inventory(pull(n)), time.Now())
		x.Tasks[fmt.Sprint("pr:", n)].Audit = clean()
		x.Tasks[fmt.Sprint("pr:", n)].Stage = "ready"
	})
	return s.Snapshot().Towns["acme/orchard"]
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestAuditMustCoverOldFindingsAndNewConcerns(t *testing.T) {
	evidence := map[string]string{"finding:old": "Previously posted concern", "comment:7": "Author response"}
	a := clean()
	a.Findings = []Finding{{"finding:old", "resolved", "fixed by guard"}, {"comment:7", "dismissed", "question answered"}}
	if e := validateAudit(a, evidence); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Audit){func(a *Audit) { a.Findings = a.Findings[:1] }, func(a *Audit) { a.Findings[0].State = "open" }, func(a *Audit) { a.Findings[0].State = "uncertain" }, func(a *Audit) { a.Findings[1].ID = a.Findings[0].ID }, func(a *Audit) { a.Complete = false }, func(a *Audit) { a.Checks = []string{""} }} {
		b := clone(a)
		mutate(b)
		if validateAudit(b, evidence) == nil {
			t.Fatalf("accepted incomplete or unclean result: %+v", b)
		}
	}
	a.Findings = append(a.Findings, Finding{"new:reconnect", "open", "new defect found in the full diff"})
	a.Verdict = "changes_needed"
	if e := validateAudit(a, evidence); e != nil {
		t.Fatal(e)
	}
	if a.Clean(baseSHA, headSHA) {
		t.Fatal("changes needed cannot merge")
	}
	if clean().Clean(baseSHA, fixSHA) {
		t.Fatal("stale review allowed")
	}
}
func TestReceiptRejectsMissingOrTrailingStructuredResult(t *testing.T) {
	var v struct {
		Summary string `json:"summary"`
	}
	for _, s := range []string{`TOWN_REPAIR {"summary":"ok"} garbage`, `TOWN_REPAIR {"summary":"ok","invented":true}`, "no receipt", "TOWN_REPAIR {\"summary\":\"ok\"}\nmore commentary"} {
		if receipt(s, "TOWN_REPAIR", &v) == nil {
			t.Fatal(s)
		}
	}
	if e := receipt("Commentary\nTOWN_REPAIR {\"summary\":\"ok\"}", "TOWN_REPAIR", &v); e != nil || v.Summary != "ok" {
		t.Fatal(e)
	}
}
func TestReconcileArrivalsRevisionAndReleaseAncestry(t *testing.T) {
	s := NewState(false)
	x, _ := s.Add(DefaultConfig("acme/orchard"))
	now := time.Now()
	remote := inventory(pull(2))
	remote.Issues = []RemoteIssue{{Number: 1, Title: "Existing", State: "open"}}
	Reconcile(&s, x, remote, now)
	for _, e := range s.Events {
		if e.Kind == "delivery" {
			t.Fatal("baseline animated as arrival")
		}
	}
	seq := s.Seq
	Reconcile(&s, x, remote, now)
	if s.Seq != seq {
		t.Fatal("poll replayed inventory")
	}
	remote.Issues = append(remote.Issues, RemoteIssue{Number: 3, Title: "External", State: "open"}, RemoteIssue{Number: 4, Title: "Found bug", State: "open", Body: "<!-- bug-bot:123 -->"}, RemoteIssue{Number: 6, Title: "New feature", State: "open", Body: "<!-- feature-bot:456 -->"})
	x.Owned[5] = Ownership{"issue-5", 4}
	remote.Pulls = append(remote.Pulls, pull(5))
	Reconcile(&s, x, remote, now)
	routes := map[string]string{}
	for _, e := range s.Events {
		if e.Seq > seq && e.Kind == "delivery" {
			routes[e.Cargo] = e.From + ">" + e.To
		}
	}
	if routes["issue:3"] != "outside>issue" || routes["issue:4"] != "bug>issue" || routes["issue:6"] != "feature>issue" || routes["pr:5"] != "issue>review" || x.Tasks["issue:6"].External {
		t.Fatal(routes)
	}
	task := x.Tasks["pr:5"]
	task.Audit = clean()
	task.Stage = "fixes"
	task.House = Issue
	task.Concerns = map[string]string{"new:old": "open defect"}
	remote.Pulls[1].Head.SHA = fixSHA
	Reconcile(&s, x, remote, now)
	if task.Audit != nil || task.House != Review || task.Stage != "queued" || len(task.Concerns) != 1 {
		t.Fatalf("bad revision routing: %+v", task)
	}
	remote.Pulls[1].MergedAt = &now
	remote.Pulls[1].MergeCommit = fixSHA
	remote.Pulls[1].State = "closed"
	remote.Releases = []RemoteRelease{{Tag: "v1.0.0", At: now.Add(time.Hour)}}
	Reconcile(&s, x, remote, now)
	if x.Tasks["commit:"+fixSHA].Stage != "unreleased" {
		t.Fatal("timestamp inferred shipment")
	}
	remote.Released = map[string]bool{fixSHA: true}
	Reconcile(&s, x, remote, now)
	if x.Tasks["commit:"+fixSHA].Stage != "shipped" {
		t.Fatal("proven ancestry not shipped")
	}
	seq = s.Seq
	Reconcile(&s, x, remote, now)
	if seq != s.Seq {
		t.Fatal("repeated shipping")
	}
}
func TestStoreRestartLockSecretsAndUnknownIntent(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir, false)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	x := setupPR(t, s, 1)
	update(t, s, func(st *State) {
		t := st.Towns[x.ID]
		t.Config.Agent.Environment = map[string]string{"SUPER_SECRET": "do-not-show"}
		t.Intents[1] = &Intent{Kind: "merge", PR: 1, Head: headSHA, Base: baseSHA, Status: "uncertain"}
	})
	raw, _ := json.Marshal(s.Snapshot().Public())
	if strings.Contains(string(raw), "do-not-show") {
		t.Fatal("agent environment leaked")
	}
	if _, e := Open(dir, false); e == nil {
		t.Fatal("second writer acquired lock")
	}
	s.Close()
	if e = s.Update(func(*State) error { return nil }); e == nil {
		t.Fatal("closed store wrote state")
	}
	if _, e := Open(dir, true); e == nil {
		t.Fatal("demo opened live state")
	}
	s, e = Open(dir, false)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Snapshot().Towns[x.ID].Intents[1].Status != "uncertain" {
		t.Fatal("restart lost intent")
	}
	before := s.Snapshot().Seq
	watch := s.Watch()
	e = s.Update(func(st *State) error { st.Seq++; return errors.New("abort") })
	if e == nil || s.Snapshot().Seq != before {
		t.Fatal("partial transaction committed")
	}
	select {
	case <-watch:
		t.Fatal("aborted update signaled")
	default:
	}
	state := s.Snapshot()
	state.Towns[x.ID].Intents[1].Head = "bad"
	if validateState(state, false) == nil {
		t.Fatal("corrupt intent accepted")
	}
	info, _ := os.Stat(filepath.Join(dir, "state.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}

type fakeGH struct {
	mu         sync.Mutex
	p          Pull
	discussion []Discussion
	gate       MergeGate
	merged     int
	mergeError error
	snapshot   RepoSnapshot
	onMerge    func()
	contains   bool
}

func newGH(n int) *fakeGH {
	return &fakeGH{p: pull(n), discussion: []Discussion{}, gate: MergeGate{Base: baseSHA, Head: headSHA, State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN"}, snapshot: inventory(pull(n))}
}
func (f *fakeGH) Snapshot(context.Context, Config) (RepoSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(f.snapshot), nil
}
func (f *fakeGH) Pull(context.Context, string, int) (Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(f.p), nil
}
func (f *fakeGH) Discussion(context.Context, string, int) ([]Discussion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(f.discussion), nil
}
func (f *fakeGH) Gate(context.Context, string, int) (MergeGate, error) { return f.gate, nil }
func (f *fakeGH) Actor(context.Context) (string, error)                { return "operator", nil }
func (f *fakeGH) Contains(context.Context, string, string, string) (bool, error) {
	return f.contains, nil
}
func (f *fakeGH) Merge(_ context.Context, _ string, n int, sha string) (string, error) {
	if f.onMerge != nil {
		f.onMerge()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if sha != f.p.Head.SHA || n != f.p.Number {
		return "", errors.New("unexpected merge revision")
	}
	f.merged++
	if f.mergeError != nil {
		return "", f.mergeError
	}
	now := time.Now()
	f.p.MergedAt = &now
	f.p.MergeCommit = fixSHA
	f.p.State = "closed"
	f.snapshot.Pulls = []Pull{f.p}
	return fixSHA, nil
}

type workerFunc func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error)

func (f workerFunc) Run(c context.Context, t *Town, r Role, p func(Progress), l *slog.Logger) (RunResult, error) {
	return f(c, t, r, p, l)
}

func TestMergePersistsIntentAndChecksFreshEvidence(t *testing.T) {
	for _, scenario := range []string{"clean", "stale", "checks", "discussion", "description", "external", "manual", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t, false)
			x := setupPR(t, s, 1)
			gh := newGH(1)
			sup := NewSupervisor(s, gh, nil)
			switch scenario {
			case "stale":
				gh.p.Head.SHA = fixSHA
			case "checks":
				gh.gate.MergeState = "BLOCKED"
			case "discussion":
				gh.discussion = []Discussion{{ID: "comment:3", Body: "new concern"}}
			case "description":
				gh.p.Body = "Changed requirements"
			case "external":
				x.Tasks["pr:1"].External = true
			case "manual":
				x.Config.MergePolicy = "manual"
			case "uncertain":
				gh.mergeError = errors.New("network disconnected after send")
			}
			gh.onMerge = func() {
				if s.Snapshot().Towns[x.ID].Intents[1].Status != "uncertain" {
					t.Fatal("write preceded intent")
				}
			}
			_, err := sup.mergeReady(context.Background(), x)
			if err != nil && scenario != "uncertain" {
				t.Fatal(err)
			}
			allowed := scenario == "clean" || scenario == "uncertain"
			if (gh.merged == 1) != allowed {
				t.Fatalf("merges=%d", gh.merged)
			}
			if scenario == "clean" {
				task := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
				if task.Stage != "merged" || task.House != Release {
					t.Fatal(task)
				}
			}
			if scenario == "discussion" && s.Snapshot().Towns[x.ID].Tasks["pr:1"].Audit != nil {
				t.Fatal("stale discussion kept audit")
			}
			if scenario == "uncertain" {
				x = s.Snapshot().Towns[x.ID]
				if !x.Tasks["pr:1"].Blocked {
					t.Fatal("uncertain merge not visible")
				}
				_, _ = sup.mergeReady(context.Background(), x)
				if gh.merged != 1 {
					t.Fatal("uncertain merge replayed")
				}
				if e := sup.Control(x.ID, Review, "retry", "pr:1"); e != nil {
					t.Fatal(e)
				}
				if s.Snapshot().Towns[x.ID].Intents[1].Status != "retry" {
					t.Fatal("retry lost intent")
				}
			}
		})
	}
}
func TestSupervisorPauseFinishStopCancelAndTownIsolation(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		t := st.Towns[x.ID]
		t.Initialized = true
		t.Workers[Repo].Enabled = false
		t.Workers[Bug].Enabled = true
		c := DefaultConfig("acme/other")
		_, _ = st.Add(c)
		st.Towns[c.Repo].Workers[Repo].Enabled = false
	})
	entered, release, exited := make(chan struct{}, 2), make(chan struct{}), make(chan struct{}, 2)
	workers := workerFunc(func(ctx context.Context, _ *Town, _ Role, observe func(Progress), log *slog.Logger) (RunResult, error) {
		entered <- struct{}{}
		defer func() { exited <- struct{}{} }()
		for i := 0; i < 100; i++ {
			observe(Progress{"checking", "Working"})
			log.Info("progress", "step", i)
		}
		select {
		case <-release:
			return RunResult{}, nil
		case <-ctx.Done():
			return RunResult{}, fmt.Errorf("process terminated: %v", ctx.Err())
		}
	})
	sup := NewSupervisor(s, newGH(1), workers)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	<-entered
	if err := sup.Control(x.ID, Bug, "pause", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
		t.Fatal("pause canceled active work")
	default:
	}
	close(release)
	<-exited
	eventually(t, func() bool { return s.Snapshot().Towns[x.ID].Workers[Bug].Status == "paused" })
	if s.Snapshot().Towns["acme/other"].Workers[Bug].Enabled {
		t.Fatal("control leaked across towns")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hung")
	}
	// A new run blocks until Stop cancels its context.
	release = make(chan struct{})
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- sup.Run(ctx) }()
	if err := sup.Control(x.ID, Bug, "start", ""); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := sup.Control(x.ID, Bug, "stop", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not cancel")
	}
	cancel()
	<-done
	if s.Snapshot().Towns[x.ID].Workers[Bug].Status != "paused" {
		t.Fatal("operator stop was reported as a failure")
	}
}
func TestFailedInitialPersistenceNeverRunsWorker(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	x.Workers[Bug].Enabled = true
	s.Close()
	called := false
	sup := NewSupervisor(s, nil, workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		called = true
		return RunResult{}, nil
	}))
	sup.execute(context.Background(), x, Bug)
	if called {
		t.Fatal("ran agent without durable state")
	}
}
func TestDemoIsIsolatedAndCompletesTheLoop(t *testing.T) {
	s := testStore(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, s, time.Millisecond) }()
	eventually(t, func() bool {
		for _, e := range s.Snapshot().Events {
			if e.From == "feature" && e.To == "issue" {
				return true
			}
		}
		return false
	})
	cancel()
	<-done
	st := s.Snapshot()
	if len(st.Towns) != 2 {
		t.Fatal("demo lost multi-town view")
	}
	routes := map[string]bool{}
	for _, e := range st.Events {
		routes[e.From+">"+e.To] = true
	}
	for _, r := range []string{"bug>issue", "feature>issue", "issue>review", "review>issue", "review>release", "release>outside"} {
		if !routes[r] {
			t.Fatal("missing route", r)
		}
	}
	live := testStore(t, false)
	if Demo(context.Background(), live) == nil {
		t.Fatal("demo accepted live store")
	}
}

func (f *fakeGH) Changes(context.Context, string, string, string) ([]RemoteCommit, error) {
	return nil, nil
}

func TestDirectCommitArrivesAtReleaseAndAppearsInReport(t *testing.T) {
	s := NewState(false)
	x, _ := s.Add(DefaultConfig("acme/orchard"))
	now := time.Now()
	Reconcile(&s, x, inventory(), now)
	c := RemoteCommit{SHA: fixSHA, URL: "https://github.com/acme/orchard/commit/" + fixSHA}
	c.Commit.Message = "Improve reconnect diagnostics\n\nDetails"
	remote := inventory()
	remote.Head = fixSHA
	remote.Commits = []RemoteCommit{c}
	Reconcile(&s, x, remote, now)
	if x.Tasks["commit:"+fixSHA].House != Release || !strings.Contains(x.Reports[len(x.Reports)-1].Body, "Improve reconnect diagnostics") {
		t.Fatal("direct commit omitted")
	}
	found := false
	for _, e := range s.Events {
		if e.From == "outside" && e.To == "release" && e.Cargo == "commit:"+fixSHA {
			found = true
		}
	}
	if !found {
		t.Fatal("no external delivery")
	}
}

func TestSlowInventoryCannotUndoConfirmedRepair(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	remote := inventory(pull(1))
	remote.ObservedHeads = map[int]string{1: headSHA}
	update(t, s, func(st *State) {
		task := st.Towns[x.ID].Tasks["pr:1"]
		task.Head = fixSHA
		task.Audit = nil
		task.Stage = "queued"
		Reconcile(st, st.Towns[x.ID], remote, time.Now())
	})
	if s.Snapshot().Towns[x.ID].Tasks["pr:1"].Head != fixSHA {
		t.Fatal("stale inventory undid confirmed repair")
	}
}
func TestRepairConfirmationIsIdempotentAcrossReporterAndWorker(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	intent := &Intent{Kind: "repair", PR: 1, Base: baseSHA, Head: headSHA, NewHead: fixSHA, Branch: pull(1).Head.Ref, Directory: t.TempDir(), Status: "uncertain"}
	update(t, s, func(st *State) {
		st.Towns[x.ID].Intents[1] = intent
		p := pull(1)
		p.Head.SHA = fixSHA
		Reconcile(st, st.Towns[x.ID], inventory(p), time.Now())
	})
	b := &BotWorkers{Store: s}
	if e := b.confirmRepair(x.ID, "pr:1", intent); e != nil {
		t.Fatal(e)
	}
	if s.Snapshot().Towns[x.ID].Tasks["pr:1"].Cycles != 1 {
		t.Fatal("reporter and worker both counted the same fix")
	}
}

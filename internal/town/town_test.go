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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	issuebot "github.com/BrokkAi/issue-bot"
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
	a.Findings = []Finding{{ID: "finding:old", State: "resolved", Detail: "fixed by guard"}, {ID: "comment:7", State: "dismissed", Detail: "question answered"}}
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
	a.Findings = append(a.Findings, Finding{ID: "new:reconnect", State: "open", Detail: "new defect found in the full diff"})
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
	if routes["issue:3"] != "outside>simplifier" || routes["issue:4"] != "bug>simplifier" || routes["issue:6"] != "feature>simplifier" || routes["pr:5"] != "issue>simplifier" || x.Tasks["issue:6"].External {
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
	health     *BranchHealth
	closed     []int
	closeError error
	// Pull requests Town closed, branches it deleted, comments it posted and
	// issues it filed, in order.
	closedPulls []int
	deleted     []string
	comments    []string
	filed       []RemoteIssue
	fileError   error
}

func newGH(n int) *fakeGH {
	return &fakeGH{p: pull(n), discussion: []Discussion{}, gate: MergeGate{Base: baseSHA, Head: headSHA, State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN"}, snapshot: inventory(pull(n))}
}

// Observe answers the repo worker's inventory from this fixture, which is what
// the released bot reads from GitHub itself.
func (f *fakeGH) Observe(_ context.Context, _ *Town, request InventoryRequest, _ func(Progress), _ *slog.Logger) (RunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot := clone(f.snapshot)
	snapshot.Released = map[string]bool{}
	for _, revision := range request.Commits {
		snapshot.Released[revision] = f.contains
	}
	return RunResult{Inventory: &snapshot, Health: f.health}, nil
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
func (f *fakeGH) CloseIssue(_ context.Context, _ string, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closeError != nil {
		return f.closeError
	}
	f.closed = append(f.closed, n)
	for i := range f.snapshot.Issues {
		if f.snapshot.Issues[i].Number == n {
			f.snapshot.Issues[i].State = "closed"
		}
	}
	return nil
}
func (f *fakeGH) ClosePull(_ context.Context, _ string, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closeError != nil {
		return f.closeError
	}
	f.closedPulls = append(f.closedPulls, n)
	if f.p.Number == n {
		f.p.State = "closed"
		f.snapshot.Pulls = []Pull{f.p}
	}
	return nil
}
func (f *fakeGH) Comment(_ context.Context, _ string, n int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, fmt.Sprintf("#%d: %s", n, body))
	return nil
}
func (f *fakeGH) DeleteBranch(_ context.Context, _ string, branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, branch)
	return nil
}
func (f *fakeGH) CreateIssue(_ context.Context, _ string, title, body string) (RemoteIssue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fileError != nil {
		return RemoteIssue{}, f.fileError
	}
	issue := RemoteIssue{Number: 900 + len(f.filed), Title: title, Body: body, URL: fmt.Sprintf("https://github.com/acme/orchard/issues/%d", 900+len(f.filed)), State: "open"}
	f.filed = append(f.filed, issue)
	f.snapshot.Issues = append(f.snapshot.Issues, issue)
	return issue, nil
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

// Observe reports an empty repository. A test whose subject is the inventory
// pairs its worker with the GitHub fixture through observing instead.
func (f workerFunc) Observe(_ context.Context, t *Town, _ InventoryRequest, _ func(Progress), _ *slog.Logger) (RunResult, error) {
	return RunResult{Inventory: &RepoSnapshot{Branch: "main", DefaultBranch: "main", Head: baseSHA}}, nil
}

// observing answers Run from the test's worker and the inventory from the same
// fixture the town's GitHub writes go to, so one repository state drives both.
type observing struct {
	workerFunc
	gh *fakeGH
}

func (o observing) Observe(ctx context.Context, t *Town, request InventoryRequest, p func(Progress), l *slog.Logger) (RunResult, error) {
	return o.gh.Observe(ctx, t, request, p, l)
}

type issueRetryWorker struct {
	root    string
	entered chan int
	mu      sync.Mutex
	active  bool
	runs    int
}

func (w *issueRetryWorker) Run(ctx context.Context, t *Town, role Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
	if role != Issue {
		return RunResult{}, fmt.Errorf("unexpected role %s", role)
	}
	_, state := Workspace(w.root, t.ID, Issue)
	if err := os.MkdirAll(state, 0700); err != nil {
		return RunResult{}, err
	}
	lock, err := os.OpenFile(filepath.Join(state, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return RunResult{}, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return RunResult{}, err
	}
	w.mu.Lock()
	w.active = true
	w.runs++
	run := w.runs
	w.mu.Unlock()
	w.entered <- run
	<-ctx.Done()
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	w.mu.Lock()
	w.active = false
	w.mu.Unlock()
	return RunResult{}, ctx.Err()
}

func (w *issueRetryWorker) RetryIssue(t *Town, issue int) error {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active {
		return errors.New("retry overlapped the active issue worker")
	}
	return (&BotWorkers{Root: w.root}).RetryIssue(t, issue)
}

func (w *issueRetryWorker) CanRetryIssue(t *Town, issue int) (bool, error) {
	return (&BotWorkers{Root: w.root}).CanRetryIssue(t, issue)
}

type failingIssueRetrier struct {
	workerFunc
	inspectErr error
	retryErr   error
}

func (w failingIssueRetrier) CanRetryIssue(*Town, int) (bool, error) {
	return true, w.inspectErr
}
func (w failingIssueRetrier) RetryIssue(*Town, int) error { return w.retryErr }

func blockedIssueTown(t *testing.T, s *Store) *Town {
	t.Helper()
	x := addTown(t, s)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		town.Workers[Repo].Enabled = false
		town.Workers[Issue].Enabled = true
		town.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, Title: "Issue 7", Stage: "queued", House: Issue, Blocked: true, Attempts: 3, RetryAt: time.Now().Add(time.Hour), Updated: time.Now()}
	})
	return x
}

func TestIssueRetryWithoutDurableJobDoesNotStopActiveWorker(t *testing.T) {
	s := testStore(t, false)
	x := blockedIssueTown(t, s)
	workers := &issueRetryWorker{root: t.TempDir(), entered: make(chan int, 2)}
	sup := NewSupervisor(s, newGH(1), workers)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	if run := <-workers.entered; run != 1 {
		t.Fatalf("first run = %d", run)
	}

	if err := sup.Control(x.ID, Issue, "retry", "issue:7"); err != nil {
		t.Fatal(err)
	}
	workers.mu.Lock()
	active, runs := workers.active, workers.runs
	workers.mu.Unlock()
	if !active || runs != 1 {
		t.Fatalf("Town-only retry interrupted the active issue worker: active=%t runs=%d", active, runs)
	}
	task := s.Snapshot().Towns[x.ID].Tasks["issue:7"]
	if task.Blocked || task.Attempts != 0 || !task.RetryAt.IsZero() {
		t.Fatalf("Town-only retry was not committed: %+v", task)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor shutdown hung")
	}
}

func TestIssueRetryValidationFailureDoesNotStopActiveWorker(t *testing.T) {
	s := testStore(t, false)
	x := blockedIssueTown(t, s)
	entered := make(chan struct{})
	canceled := make(chan struct{})
	want := errors.New("durable state is invalid")
	workers := failingIssueRetrier{
		workerFunc: func(ctx context.Context, _ *Town, _ Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return RunResult{}, ctx.Err()
		},
		inspectErr: want,
	}
	sup := NewSupervisor(s, newGH(1), workers)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	<-entered

	err := sup.Control(x.ID, Issue, "retry", "issue:7")
	if !errors.Is(err, want) {
		t.Fatalf("wrong retry error: %v", err)
	}
	select {
	case <-canceled:
		t.Fatal("validation failure canceled the active issue worker")
	default:
	}
	task := s.Snapshot().Towns[x.ID].Tasks["issue:7"]
	if !task.Blocked || task.Attempts != 3 {
		t.Fatalf("validation failure changed Town retry state: %+v", task)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor shutdown hung")
	}
}

func TestIssueRetryResetsSelectedDurableJobAfterActiveWorkerStops(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	now := time.Now()
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		town.Workers[Repo].Enabled = false
		town.Workers[Issue].Enabled = true
		for _, n := range []int{1, 2} {
			id := fmt.Sprintf("issue:%d", n)
			town.Tasks[id] = &Task{ID: id, Kind: "issue", Number: n, Title: fmt.Sprintf("Issue %d", n), Stage: "queued", House: Issue, Blocked: true, Attempts: 3, RetryAt: now.Add(time.Hour), Updated: now}
		}
	})

	root := t.TempDir()
	dir, stateDir := Workspace(root, x.ID, Issue)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	claim := &issuebot.Claim{Token: strings.Repeat("a", 32), Repo: x.ID, Issue: 1, Status: "released", Detail: "publication outcome uncertain"}
	saved := &issuebot.State{
		Format: 1, Remote: "https://github.com/acme/orchard.git", Branch: "main", Directory: dir, Repo: x.ID, Host: "github.com",
		Jobs: map[int]*issuebot.Job{
			1: {ClaimPending: true, Claim: claim, Issue: issuebot.Issue{Number: 1}, Branch: "issue-bot/1", Base: baseSHA, Tries: 3, RetryAt: now.Add(time.Hour), Failure: "budget exhausted", Status: "blocked", Result: &issuebot.Result{Status: "blocked", Detail: "saved diagnostics"}},
			2: {Issue: issuebot.Issue{Number: 2}, Branch: "issue-bot/2", Tries: 3, RetryAt: now.Add(2 * time.Hour), Failure: "other failure", Status: "blocked"},
		},
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stateDir, "state.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	workers := &issueRetryWorker{root: root, entered: make(chan int, 2)}
	sup := NewSupervisor(s, newGH(1), workers)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	if run := <-workers.entered; run != 1 {
		t.Fatalf("first run = %d", run)
	}
	if err = sup.Control(x.ID, Issue, "retry", "issue:1"); err != nil {
		t.Fatal(err)
	}
	if run := <-workers.entered; run != 2 {
		t.Fatalf("retry did not resume issue worker; run = %d", run)
	}

	cfg := issuebot.DefaultConfig()
	cfg.Remote, cfg.Branch, cfg.Directory, cfg.StateDirectory = saved.Remote, saved.Branch, saved.Directory, stateDir
	cfg.GitHub.Repo, cfg.GitHub.Host = saved.Repo, saved.Host
	got, err := issuebot.ReadState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	job := got.Jobs[1]
	if job.Tries != 0 || !job.RetryAt.IsZero() || job.Status != "pending" {
		t.Fatalf("selected durable job was not reset: %+v", job)
	}
	if !job.ClaimPending || job.Claim == nil || job.Claim.Detail != claim.Detail || job.Base != baseSHA || job.Result == nil || job.Result.Detail != "saved diagnostics" {
		t.Fatalf("retry discarded uncertain claim or saved work: %+v", job)
	}
	other := got.Jobs[2]
	if other.Tries != 3 || other.Status != "blocked" || !other.RetryAt.Equal(saved.Jobs[2].RetryAt) {
		t.Fatalf("unrelated durable job changed: %+v", other)
	}
	town := s.Snapshot().Towns[x.ID]
	if town.Tasks["issue:1"].Blocked || town.Tasks["issue:1"].Attempts != 0 || !town.Tasks["issue:1"].RetryAt.IsZero() {
		t.Fatalf("Town retry state was not reset: %+v", town.Tasks["issue:1"])
	}
	if !town.Tasks["issue:2"].Blocked || town.Tasks["issue:2"].Attempts != 3 {
		t.Fatalf("unrelated Town task changed: %+v", town.Tasks["issue:2"])
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor shutdown hung")
	}
}

func TestIssueRetryFailureDoesNotAcknowledgeTownRetry(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	retryAt := time.Now().Add(time.Hour)
	update(t, s, func(st *State) {
		task := st.Towns[x.ID].Tasks["issue:1"]
		if task == nil {
			task = &Task{ID: "issue:1", Kind: "issue", Number: 1, Title: "Issue 1", Stage: "queued", House: Issue, Updated: time.Now()}
			st.Towns[x.ID].Tasks[task.ID] = task
		}
		task.Blocked, task.Attempts, task.RetryAt = true, 3, retryAt
	})
	want := errors.New("durable state is unavailable")
	sup := NewSupervisor(s, newGH(1), failingIssueRetrier{workerFunc: func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		return RunResult{}, nil
	}, retryErr: want})
	err := sup.Control(x.ID, Issue, "retry", "issue:1")
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "reset issue-bot retry state") {
		t.Fatalf("wrong retry error: %v", err)
	}
	task := s.Snapshot().Towns[x.ID].Tasks["issue:1"]
	if !task.Blocked || task.Attempts != 3 || !task.RetryAt.Equal(retryAt) || task.House != Issue {
		t.Fatalf("failed durable reset was acknowledged by Town: %+v", task)
	}
}

func TestMergePersistsIntentAndChecksFreshEvidence(t *testing.T) {
	for _, scenario := range []string{"clean", "stale", "checks", "discussion", "description", "external", "manual", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t, false)
			x := setupPR(t, s, 1)
			gh := newGH(1)
			sup := NewSupervisor(s, gh, observing{gh: gh})
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
			_, err := sup.mergeReady(context.Background(), x, slog.Default())
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
				_, _ = sup.mergeReady(context.Background(), x, slog.Default())
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

func TestReviewFailuresGetOneMoreAttemptThenCloseAndRequeue(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Workers[Review].Enabled = true
		task := town.Tasks["pr:1"]
		task.Stage, task.Audit = "queued", nil
		town.Tasks["issue:1"] = &Task{ID: "issue:1", Kind: "issue", Number: 1, Title: "Issue 1", Stage: "implemented", House: Issue, Updated: time.Now()}
	})
	failure := &ReviewAttemptError{Complete: false, ExpectedBase: baseSHA, ExpectedHead: headSHA}
	gh := newGH(1)
	sup := NewSupervisor(s, gh, workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		return RunResult{PR: 1}, failure
	}))
	sup.execute(context.Background(), s.Snapshot().Towns[x.ID], Review, nil)
	task := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
	if task.Attempts != 1 || task.Blocked || task.Stage != "queued" || task.RetryAt.IsZero() || !strings.Contains(task.Detail, "one more attempt") {
		t.Fatalf("first failure did not schedule exactly one more attempt: %+v", task)
	}
	if !strings.Contains(task.Detail, "<missing>") {
		t.Fatalf("reviewer error hidden from the task: %q", task.Detail)
	}
	update(t, s, func(st *State) { st.Towns[x.ID].Tasks["pr:1"].RetryAt = time.Time{} })
	sup.execute(context.Background(), s.Snapshot().Towns[x.ID], Review, nil)
	town := s.Snapshot().Towns[x.ID]
	task = town.Tasks["pr:1"]
	if task.Stage != "closing" || task.House != Hall || task.Blocked || !strings.Contains(task.Detail, "Two Review attempts") {
		t.Fatalf("second failure did not retire the pull request: %+v", task)
	}
	if town.Workers[Review].Status == "failed" {
		t.Fatal("a pull request failure marked the whole house failed")
	}
	// The next inventory closes the pull request, deletes Town's branch, leaves
	// the reason on both the PR and the issue, and queues a fresh attempt.
	if err := sup.closeRetiredPulls(context.Background(), town, inventory(pull(1))); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 1 || gh.closedPulls[0] != 1 || len(gh.deleted) != 1 || gh.deleted[0] != "issue-1" || len(gh.comments) != 2 {
		t.Fatalf("GitHub side of the close is incomplete: closed=%v deleted=%v comments=%d", gh.closedPulls, gh.deleted, len(gh.comments))
	}
	town = s.Snapshot().Towns[x.ID]
	if got := town.Tasks["pr:1"]; got.Stage != "closed" || got.House != Hall {
		t.Fatalf("closed pull request not recorded: %+v", got)
	}
	if issue := town.Tasks["issue:1"]; issue.Stage != "queued" || issue.House != Issue || issue.Requeue != 1 || issue.Attempts != 0 {
		t.Fatalf("issue was not queued for a fresh attempt: %+v", issue)
	}
	if !town.Workers[Issue].Next.IsZero() {
		t.Fatal("issue house not woken for the fresh attempt")
	}
	// Closing is idempotent: a second inventory pass touches nothing.
	if err := sup.closeRetiredPulls(context.Background(), town, inventory(pull(1))); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 1 || len(gh.comments) != 2 {
		t.Fatal("closing repeated GitHub writes")
	}
	closed := false
	for _, o := range town.Outcomes {
		if o.Kind == "closed" && o.TaskID == "pr:1" {
			closed = true
		}
	}
	if !closed {
		t.Fatal("close outcome not recorded")
	}
}

func TestCompletedNegativeReviewRoutesByOwnership(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(fmt.Sprint("external=", external), func(t *testing.T) {
			s := testStore(t, false)
			x := setupPR(t, s, 1)
			negative := clean()
			negative.Verdict = "changes_needed"
			negative.Findings = []Finding{{ID: "new:defect", State: "open", Detail: "broken behavior"}}
			update(t, s, func(st *State) {
				town := st.Towns[x.ID]
				town.Workers[Review].Enabled = true
				task := town.Tasks["pr:1"]
				task.Stage, task.Audit, task.External = "queued", nil, external
			})
			sup := NewSupervisor(s, newGH(1), workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
				return RunResult{PR: 1, Audit: negative}, nil
			}))
			sup.execute(context.Background(), s.Snapshot().Towns[x.ID], Review, nil)
			task := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
			if external {
				if task.House != Hall || task.Stage != "awaiting_mayor" || task.MayoralDecision != "pending" {
					t.Fatalf("external review did not reach Mayor: %+v", task)
				}
				if err := sup.Control(x.ID, Hall, "decline", task.ID); err != nil {
					t.Fatal(err)
				}
				if got := s.Snapshot().Towns[x.ID].Tasks[task.ID]; got.Stage != "declined" {
					t.Fatalf("Mayor could not resolve task: %+v", got)
				}
			} else if task.House != Issue || task.Stage != "fixes" {
				t.Fatalf("owned review did not reach Issue Bot: %+v", task)
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
			observe(Progress{Phase: "checking", Task: "Working"})
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
	sup.execute(context.Background(), x, Bug, nil)
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
			if e.From == "feature" && e.To == "hall" {
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
	for _, r := range []string{"bug>issue", "feature>hall", "issue>review", "review>issue", "review>release", "release>outside"} {
		if !routes[r] {
			t.Fatal("missing route", r)
		}
	}
	live := testStore(t, false)
	if Demo(context.Background(), live) == nil {
		t.Fatal("demo accepted live store")
	}
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

// A worker that reports phases faster than the persisting consumer drains them
// must leave the newest phase on display, not the first one that fit.
func TestProgressChannelKeepsNewestPhase(t *testing.T) {
	ch := make(chan Progress, 1)
	latestProgress(ch, Progress{Phase: "starting", Task: "Preparing"})
	latestProgress(ch, Progress{Phase: "reviewing", Task: "Reading the diff"})
	latestProgress(ch, Progress{Phase: "certifying", Task: "Checking findings", Seq: 7})
	select {
	case got := <-ch:
		if got.Phase != "certifying" || got.Task != "Checking findings" || got.Seq != 7 {
			t.Fatalf("stale phase survived a burst: %+v", got)
		}
	default:
		t.Fatal("no observation was published")
	}
	select {
	case extra := <-ch:
		t.Fatalf("replacement left a second observation queued: %+v", extra)
	default:
	}
	// An empty channel still takes the observation, and a consumer that keeps up
	// sees every phase in order.
	latestProgress(ch, Progress{Phase: "reporting", Task: "Writing the audit"})
	if got := <-ch; got.Phase != "reporting" {
		t.Fatalf("observation was dropped on an empty channel: %+v", got)
	}
}

// One failing verify command must not bloat state.json or the snapshot pushed
// to every client, however much output the subprocess produced.
func TestPersistedTextIsBounded(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	huge := strings.Repeat("verify output ", 100000)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		w := town.Workers[Review]
		w.Error = huge
		w.Task = "Work paused: " + huge
		w.Phase = huge
		w.Logs = append(w.Logs, Log{At: time.Now(), Level: "ERROR", Text: huge})
		town.Error = huge
		town.Tasks["pr:1"].Detail = huge
		town.Intents[1] = &Intent{Kind: "merge", PR: 1, Base: baseSHA, Head: headSHA, Status: "uncertain", Detail: huge, At: time.Now()}
		town.RecordOutcome(OutcomeRecord{ID: "blocked:pr:1:1", At: time.Now(), Class: "outcome", Kind: "blocked", Status: "blocked", Role: Review, TaskID: "pr:1", Detail: huge})
		town.Report(huge, huge, time.Now())
	})
	town := s.Snapshot().Towns[x.ID]
	w := town.Workers[Review]
	fields := map[string]string{
		"worker error": w.Error, "worker task": w.Task, "worker phase": w.Phase,
		"worker log": w.Logs[len(w.Logs)-1].Text, "town error": town.Error,
		"task detail": town.Tasks["pr:1"].Detail, "intent detail": town.Intents[1].Detail,
		"outcome detail": town.Outcomes[len(town.Outcomes)-1].Detail,
		"report body":    town.Reports[len(town.Reports)-1].Body,
		"report title":   town.Reports[len(town.Reports)-1].Title,
	}
	for name, value := range fields {
		if len(value) > StateTextLimit+64 {
			t.Fatalf("%s kept %d bytes of subprocess output", name, len(value))
		}
		if !strings.Contains(value, "verify output") {
			t.Fatalf("%s lost the explanation entirely: %q", name, value)
		}
	}
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 256<<10 {
		t.Fatalf("state file grew to %d bytes", info.Size())
	}
}

// Retrying one blocked task must not start a house the operator paused: that
// would release every other ready merge and queued review at the same time.
func TestRetryLeavesAPausedHousePaused(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Workers[Review].Enabled = false
		town.Workers[Review].Status = "paused"
		task := town.Tasks["pr:1"]
		task.Blocked = true
		task.Attempts = 3
		task.RetryAt = time.Now().Add(time.Hour)
		task.Detail = "Merge outcome is uncertain."
		town.Intents[1] = &Intent{Kind: "merge", PR: 1, Base: baseSHA, Head: headSHA, Status: "uncertain", At: time.Now()}
	})
	sup := NewSupervisor(s, newGH(1), workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		t.Fatal("paused house dispatched work after a task retry")
		return RunResult{}, nil
	}))
	before := len(s.Snapshot().Events)
	if err := sup.Control(x.ID, Review, "retry", "pr:1"); err != nil {
		t.Fatal(err)
	}
	town := s.Snapshot().Towns[x.ID]
	w := town.Workers[Review]
	if w.Enabled {
		t.Fatal("retry started a paused house")
	}
	if w.Status != "paused" {
		t.Fatalf("paused house changed status: %q", w.Status)
	}
	task := town.Tasks["pr:1"]
	if task.Blocked || task.Attempts != 0 || !task.RetryAt.IsZero() {
		t.Fatalf("retry did not clear the task's own block: %+v", task)
	}
	if town.Intents[1].Status != "retry" {
		t.Fatalf("retry lost the uncertain write intent: %+v", town.Intents[1])
	}
	if !strings.Contains(task.Detail, "paused") {
		t.Fatalf("operator is not told the house is paused: %q", task.Detail)
	}
	events := s.Snapshot().Events
	if len(events) != before+1 {
		t.Fatalf("retry recorded %d events", len(events)-before)
	}
	if last := events[len(events)-1]; last.Kind != "control" || last.Cargo != "pr:1" || last.From != "operator" {
		t.Fatalf("retry was not recorded as an operator control event: %+v", last)
	}
	// Scheduling confirms it: the paused house stays out of the pool. The
	// worker above fails the test if it is ever dispatched.
	sup.schedule(context.Background())
	sup.wg.Wait()

	// Starting the house makes the same task eligible again.
	if err := sup.Control(x.ID, Review, "start", ""); err != nil {
		t.Fatal(err)
	}
	if w := s.Snapshot().Towns[x.ID].Workers[Review]; !w.Enabled || !w.Next.IsZero() {
		t.Fatalf("start did not make the house eligible: %+v", w)
	}
}

// The repository default is an observation. Recording it as configuration
// pinned whatever GitHub reported first, so a later rename left the town
// looking up a branch that no longer existed, and an operator's own choice was
// replaced on every poll.
func TestReconcileTracksTheDefaultBranchWithoutRewritingConfig(t *testing.T) {
	s := testStore(t, false)
	update(t, s, func(st *State) {
		follower := DefaultConfig("acme/follower")
		if _, e := st.Add(follower); e != nil {
			t.Fatal(e)
		}
		chosen := DefaultConfig("acme/chosen")
		chosen.Branch = "release"
		if _, e := st.Add(chosen); e != nil {
			t.Fatal(e)
		}
	})
	observe := func(id, inventoried, def string) {
		t.Helper()
		update(t, s, func(st *State) {
			Reconcile(st, st.Towns[id], RepoSnapshot{Branch: inventoried, DefaultBranch: def, Head: baseSHA}, time.Now())
		})
	}
	observe("acme/follower", "main", "main")
	observe("acme/chosen", "release", "main")

	follower := s.Snapshot().Towns["acme/follower"]
	if follower.Config.Branch != "" {
		t.Fatalf("the observed default was written into configuration: %q", follower.Config.Branch)
	}
	if follower.DefaultBranch != "main" || follower.Branch() != "main" {
		t.Fatalf("observed default not tracked: %+v", follower.DefaultBranch)
	}
	chosen := s.Snapshot().Towns["acme/chosen"]
	if chosen.Config.Branch != "release" || chosen.Branch() != "release" {
		t.Fatalf("operator's branch was overwritten: %q", chosen.Config.Branch)
	}
	if chosen.DefaultBranch != "main" {
		t.Fatalf("repository default not observed alongside the choice: %q", chosen.DefaultBranch)
	}

	// Renaming the repository default moves a following town with it, and leaves
	// a town that chose its own branch alone.
	observe("acme/follower", "trunk", "trunk")
	observe("acme/chosen", "release", "trunk")
	if got := s.Snapshot().Towns["acme/follower"].Branch(); got != "trunk" {
		t.Fatalf("renamed default did not reach a following town: %q", got)
	}
	if got := s.Snapshot().Towns["acme/chosen"].Branch(); got != "release" {
		t.Fatalf("rename disturbed an operator's branch: %q", got)
	}

	// Clients read the branch actually in use, not an empty setting.
	public := s.Snapshot().Public()["towns"].(map[string]any)
	for id, want := range map[string]string{"acme/follower": "trunk", "acme/chosen": "release"} {
		cfg := public[id].(map[string]any)["config"].(PublicConfig)
		if cfg.Branch != want {
			t.Fatalf("%s reported branch %q, want %q", id, cfg.Branch, want)
		}
	}
}

// orderedGH serves one inventory per call, holding the first open until it is
// released, and reports whether two reconciliations were ever inside the repo
// worker at the same time.
type orderedGH struct {
	*fakeGH
	heads      []string
	calls      atomic.Int32
	inFlight   atomic.Int32
	overlapped atomic.Bool
	entered    chan struct{}
	release    chan struct{}
}

func (g *orderedGH) Run(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
	return RunResult{}, nil
}

func (g *orderedGH) Observe(ctx context.Context, t *Town, request InventoryRequest, p func(Progress), l *slog.Logger) (RunResult, error) {
	if g.inFlight.Add(1) > 1 {
		g.overlapped.Store(true)
	}
	defer g.inFlight.Add(-1)
	index := int(g.calls.Add(1)) - 1
	if index == 0 {
		close(g.entered)
		<-g.release
	}
	result, err := g.fakeGH.Observe(ctx, t, request, p, l)
	if index < len(g.heads) && result.Inventory != nil {
		result.Inventory.Head = g.heads[index]
	}
	return result, err
}

// The repo worker and the merge path both reconcile. Overlapping passes could
// commit in the order they finished rather than the order they observed,
// leaving the town's recorded head behind the repository.
func TestReconcileIsSerializedPerTown(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	older, newer := strings.Repeat("1", 40), strings.Repeat("2", 40)
	gh := &orderedGH{fakeGH: newGH(1), heads: []string{older, newer}, entered: make(chan struct{}), release: make(chan struct{})}
	sup := NewSupervisor(s, gh, gh)
	update(t, s, func(st *State) { st.Towns[x.ID].Initialized = true; st.Towns[x.ID].Head = baseSHA })

	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]) }()
	select {
	case <-gh.entered:
	case err := <-first:
		t.Fatalf("the first reconcile never read GitHub: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the first reconcile never started")
	}
	go func() { second <- sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]) }()
	// A serialized second pass waits its turn and cannot finish while the first
	// holds GitHub open. An unserialized one observes and commits immediately,
	// and its commit is then overtaken by the older inventory below.
	overtaken := false
	select {
	case err := <-second:
		overtaken = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
	}
	close(gh.release)
	waits := []chan error{first}
	if !overtaken {
		waits = append(waits, second)
	}
	for _, done := range waits {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reconcile did not finish")
		}
	}
	if gh.overlapped.Load() {
		t.Fatal("two reconciliations read GitHub at the same time")
	}
	if got := s.Snapshot().Towns[x.ID].Head; got != newer {
		t.Fatalf("the town's head regressed to an older observation: %s", got)
	}
}

// A reconcile waiting its turn still answers a stop, so a shutdown does not
// have to wait out a full inventory of a large repository.
func TestWaitingReconcileAnswersCancellation(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	gh := &orderedGH{fakeGH: newGH(1), entered: make(chan struct{}), release: make(chan struct{})}
	sup := NewSupervisor(s, gh, gh)
	update(t, s, func(st *State) { st.Towns[x.ID].Initialized = true })
	held := make(chan error, 1)
	go func() { held <- sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]) }()
	select {
	case <-gh.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first reconcile never started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() { waiting <- sup.reconcileNow(ctx, s.Snapshot().Towns[x.ID]) }()
	cancel()
	select {
	case err := <-waiting:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a canceled reconcile returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a canceled reconcile waited for the inventory in flight")
	}
	close(gh.release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
}

// Release ancestry is proven by the repo worker now. Town's part is to name the
// commits it cannot account for and to apply what comes back.
func TestInventoryAsksTheWorkerToProveUnshippedCommits(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	shipped, unshipped := strings.Repeat("a", 40), strings.Repeat("b", 40)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		town.Head = baseSHA
		town.Tasks["commit:"+shipped] = &Task{ID: "commit:" + shipped, Kind: "commit", Stage: "shipped", House: Release, Head: shipped}
		town.Tasks["commit:"+unshipped] = &Task{ID: "commit:" + unshipped, Kind: "commit", Stage: "unreleased", House: Release, Head: unshipped}
	})
	gh := newGH(1)
	var asked InventoryRequest
	workers := &recordingObserver{gh: gh, released: map[string]bool{unshipped: true}, seen: &asked}
	sup := NewSupervisor(s, gh, workers)
	if err := sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if asked.SinceHead != baseSHA {
		t.Fatalf("the worker was asked to compare from %q, want the head Town last observed", asked.SinceHead)
	}
	if len(asked.Commits) != 1 || asked.Commits[0] != unshipped {
		t.Fatalf("Town asked about %v, want only the commit it cannot account for", asked.Commits)
	}
	if !asked.Health {
		t.Fatal("the repo house must ask for the branch-health duty")
	}
	if stage := s.Snapshot().Towns[x.ID].Tasks["commit:"+unshipped].Stage; stage != "shipped" {
		t.Fatalf("proven ancestry was not applied: commit is %q", stage)
	}
}

// recordingObserver answers the inventory from the GitHub fixture and records
// what Town asked for.
type recordingObserver struct {
	workerFunc
	gh       *fakeGH
	released map[string]bool
	seen     *InventoryRequest
}

func (o *recordingObserver) Observe(ctx context.Context, t *Town, request InventoryRequest, p func(Progress), l *slog.Logger) (RunResult, error) {
	*o.seen = request
	result, err := o.gh.Observe(ctx, t, request, p, l)
	if result.Inventory != nil {
		result.Inventory.Released = o.released
	}
	return result, err
}

func (o *recordingObserver) Run(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
	return RunResult{}, nil
}

// countCommits reports how many durable state transactions happen while fn runs.
func countCommits(t *testing.T, s *Store, fn func()) int {
	t.Helper()
	var commits atomic.Int32
	stop, watching := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watching)
		for {
			changed := s.Watch()
			select {
			case <-changed:
				commits.Add(1)
			case <-stop:
				return
			}
		}
	}()
	fn()
	close(stop)
	// Wake the watcher so it observes the stop.
	update(t, s, func(*State) {})
	<-watching
	return int(commits.Load())
}

// A chatty agent produces log lines far faster than a durable transaction can
// commit them, and each transaction clones, revalidates and fsyncs the whole
// state and pushes a snapshot to every client.
func TestWorkerChatterIsCoalescedIntoFewTransactions(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		town.Workers[Bug].Enabled = true
	})
	const lines = 300
	workers := workerFunc(func(_ context.Context, _ *Town, _ Role, observe func(Progress), log *slog.Logger) (RunResult, error) {
		for i := 0; i < lines; i++ {
			log.Info("scanning", "file", fmt.Sprintf("source-%d.go", i))
			observe(Progress{Phase: "scanning", Task: fmt.Sprintf("file %d", i)})
		}
		return RunResult{}, nil
	})
	sup := NewSupervisor(s, newGH(1), workers)
	commits := countCommits(t, s, func() {
		sup.execute(context.Background(), s.Snapshot().Towns[x.ID], Bug, nil)
	})
	if commits > 12 {
		t.Fatalf("%d durable transactions for one worker run of %d log lines", commits, lines)
	}
	w := s.Snapshot().Towns[x.ID].Workers[Bug]
	if len(w.Logs) == 0 {
		t.Fatal("coalescing dropped every log line")
	}
	if last := w.Logs[len(w.Logs)-1].Text; !strings.Contains(last, fmt.Sprintf("source-%d.go", lines-1)) {
		t.Fatalf("the newest log line was not committed: %q", last)
	}
	if w.Phase != "scanning" || !strings.Contains(w.Task, fmt.Sprint(lines-1)) {
		t.Fatalf("the newest phase was not committed: %q %q", w.Phase, w.Task)
	}
}

// Observe reports an empty repository: this fixture's subject is dispatch, not
// the inventory.
func (w *issueRetryWorker) Observe(context.Context, *Town, InventoryRequest, func(Progress), *slog.Logger) (RunResult, error) {
	return RunResult{Inventory: &RepoSnapshot{Branch: "main", DefaultBranch: "main", Head: baseSHA}}, nil
}

// reconcileNow runs one inventory with the branch-health duty, the way the repo
// house does.
func (s *Supervisor) reconcileNow(ctx context.Context, t *Town) error {
	return s.reconcile(ctx, t, true, func(Progress) {}, slog.Default())
}

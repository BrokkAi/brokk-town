package town

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// judging answers Run from the test's worker and Judge from a script keyed by
// task ID, so a test can drive Auto-Mayor without an agent.
type judging struct {
	workerFunc
	verdicts map[string]Judgment
	failures map[string]error
	seen     []string
}

func (j *judging) Judge(_ context.Context, _ *Town, task *Task, _ *slog.Logger) (Judgment, error) {
	j.seen = append(j.seen, task.ID)
	if err := j.failures[task.ID]; err != nil {
		return Judgment{}, err
	}
	return j.verdicts[task.ID], nil
}

func idleWorker(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
	return RunResult{}, nil
}

// pendingAt places a task at Town Hall awaiting the Mayor.
func pendingAt(t *Town, task *Task) *Task {
	task.House, task.Stage, task.MayoralDecision = Hall, "awaiting_mayor", "pending"
	task.Updated = time.Now()
	t.Tasks[task.ID] = task
	return task
}

func autoMayorTown(t *testing.T, store *Store, on bool) *Town {
	t.Helper()
	x := addTown(t, store)
	update(t, store, func(st *State) {
		current := st.Towns[x.ID]
		current.Initialized = true
		current.Config.AutoMayor = on
		pendingAt(current, &Task{ID: "issue:1", Kind: "issue", Number: 1, Title: "Bug Bot finding", Simplification: &Simplification{Mode: "suggest", Decision: "admit", Detail: "Focused work."}})
		pendingAt(current, &Task{ID: "pr:4", Kind: "pr", Number: 4, Title: "Outside PR", External: true, Head: fixSHA, Base: baseSHA})
		pendingAt(current, &Task{ID: upgradeTaskID(Feature), Kind: "upgrade", Title: "Feature Bot 0.1.2 is available", Upgrade: &BotUpgrade{Role: Feature, From: "0.1.1", To: "0.1.2"}})
	})
	return store.Snapshot().Towns[x.ID]
}

// judgeAll runs scheduling passes until nothing waits, or until the deadline.
func judgeAll(t *testing.T, sup *Supervisor, id string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		sup.schedule(context.Background())
		sup.wg.Wait()
		current := sup.Store.Snapshot().Towns[id]
		if nextJudgment(current, sup.now()) == nil {
			return
		}
	}
	t.Fatal("Auto-Mayor did not finish judging")
}

func TestAutoMayorAppliesTheAgentsVerdictThroughTheMayorsPath(t *testing.T) {
	store := testStore(t, false)
	x := autoMayorTown(t, store, true)
	workers := &judging{workerFunc: idleWorker, verdicts: map[string]Judgment{
		"issue:1":              {Decision: "admit", Reason: "A real defect with a bounded fix."},
		"pr:4":                 {Decision: "decline", Reason: "Rewrites the scheduler for one flag."},
		upgradeTaskID(Feature): {Decision: "delay", Reason: "Let the release settle for a day."},
	}}
	sup := NewSupervisor(store, nil, workers)
	judgeAll(t, sup, x.ID)
	state := store.Snapshot()
	town := state.Towns[x.ID]
	issue, pr, upgrade := town.Tasks["issue:1"], town.Tasks["pr:4"], town.Tasks[upgradeTaskID(Feature)]
	if issue.Stage != "queued" || issue.House != Issue || issue.MayoralDecision != "admitted" || !strings.Contains(issue.Detail, "bounded fix") {
		t.Fatalf("issue was not admitted with the agent's reason: %+v", issue)
	}
	if pr.Stage != "declined" || pr.MayoralDecision != "declined" || !strings.Contains(pr.Detail, "for one flag") {
		t.Fatalf("PR was not declined with the agent's reason: %+v", pr)
	}
	if upgrade.Stage != "delayed" || upgrade.RetryAt.IsZero() || town.Config.BotVersions[Feature] == "0.1.2" {
		t.Fatalf("bot update was not delayed: %+v", upgrade)
	}
	if town.Workers[Issue].Next != (time.Time{}) {
		t.Fatal("the Issue house was not woken for the admitted work")
	}
	named := 0
	for _, e := range state.Events {
		if e.Kind == "decision" && strings.HasPrefix(e.Title, "Auto-Mayor ") {
			named++
		}
	}
	if named != 3 || len(workers.seen) != 3 {
		t.Fatalf("expected three judgments attributed to Auto-Mayor, got %d events over %v", named, workers.seen)
	}
	if state.Capacity != nil && state.Capacity.Active != 0 {
		t.Fatalf("judgment slots were not released: %+v", state.Capacity)
	}
}

func TestAutoMayorRetriesAFailedJudgmentThenLeavesItForThePerson(t *testing.T) {
	store := testStore(t, false)
	x := autoMayorTown(t, store, true)
	workers := &judging{workerFunc: idleWorker, verdicts: map[string]Judgment{
		"pr:4":                 {Decision: "admit", Reason: "Small and correct."},
		upgradeTaskID(Feature): {Decision: "admit", Reason: "Stable release."},
	}, failures: map[string]error{"issue:1": errors.New("agent did not finish with a receipt")}}
	sup := NewSupervisor(store, nil, workers)
	clock := time.Now()
	sup.now = func() time.Time { return clock }
	judgeAll(t, sup, x.ID)
	issue := store.Snapshot().Towns[x.ID].Tasks["issue:1"]
	if issue.MayoralDecision != "pending" || issue.Attempts != 1 || !issue.RetryAt.After(clock) || !strings.Contains(issue.Detail, "attempt 1 of 3") {
		t.Fatalf("failed judgment did not back off: %+v", issue)
	}
	if pr := store.Snapshot().Towns[x.ID].Tasks["pr:4"]; pr.MayoralDecision != "admitted" {
		t.Fatalf("one failure held up unrelated arrivals: %+v", pr)
	}
	for i := 0; i < 2; i++ {
		clock = clock.Add(mayorRetryDelay + time.Second)
		judgeAll(t, sup, x.ID)
	}
	issue = store.Snapshot().Towns[x.ID].Tasks["issue:1"]
	if issue.MayoralDecision != "pending" || issue.Attempts != mayorAttempts || !strings.Contains(issue.Detail, "left for the Mayor") {
		t.Fatalf("exhausted judgment was not left for the person: %+v", issue)
	}
	clock = clock.Add(mayorRetryDelay + time.Second)
	sup.schedule(context.Background())
	sup.wg.Wait()
	if count := strings.Count(strings.Join(workers.seen, " "), "issue:1"); count != mayorAttempts {
		t.Fatalf("expected exactly %d attempts, got %d", mayorAttempts, count)
	}
	if err := sup.Control(x.ID, Hall, "admit", "issue:1"); err != nil {
		t.Fatal(err)
	}
	if issue = store.Snapshot().Towns[x.ID].Tasks["issue:1"]; issue.Attempts != 0 || issue.MayoralDecision != "admitted" {
		t.Fatalf("the Mayor's own decision did not clear the attempts: %+v", issue)
	}
}

func TestAutoMayorStaysOutOfTownsThatDidNotOptIn(t *testing.T) {
	store := testStore(t, false)
	x := autoMayorTown(t, store, false)
	workers := &judging{workerFunc: idleWorker, verdicts: map[string]Judgment{"issue:1": {Decision: "admit", Reason: "ok"}}}
	sup := NewSupervisor(store, nil, workers)
	sup.schedule(context.Background())
	sup.wg.Wait()
	if len(workers.seen) != 0 {
		t.Fatalf("a town without Auto-Mayor was judged: %v", workers.seen)
	}
	if err := sup.SetAutoMayor(x.ID, true); err != nil {
		t.Fatal(err)
	}
	if !store.Snapshot().Towns[x.ID].Config.Public().AutoMayor {
		t.Fatal("Auto-Mayor setting was not published")
	}
	sup.schedule(context.Background())
	sup.wg.Wait()
	if len(workers.seen) != 1 {
		t.Fatalf("turning Auto-Mayor on did not judge the waiting arrival: %v", workers.seen)
	}
	plain := NewSupervisor(store, nil, workerFunc(idleWorker))
	plain.schedule(context.Background())
	plain.wg.Wait()
}

func TestAutoMayorHoldsAnAgentSlot(t *testing.T) {
	store := testStore(t, false)
	x := autoMayorTown(t, store, true)
	if err := store.Update(func(st *State) error { st.ServiceConfig.MaxWorkers = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	workers := &judging{workerFunc: idleWorker, verdicts: map[string]Judgment{"issue:1": {Decision: "admit", Reason: "ok"}}}
	sup := NewSupervisor(store, nil, workers)
	sup.mu.Lock()
	sup.running[x.ID+":issue"] = func() {}
	sup.mu.Unlock()
	sup.schedule(context.Background())
	sup.wg.Wait()
	if len(workers.seen) != 0 {
		t.Fatalf("Auto-Mayor judged beyond the agent capacity: %v", workers.seen)
	}
	sup.mu.Lock()
	delete(sup.running, x.ID+":issue")
	sup.mu.Unlock()
	sup.schedule(context.Background())
	sup.wg.Wait()
	if len(workers.seen) != 1 {
		t.Fatalf("Auto-Mayor did not use the freed slot: %v", workers.seen)
	}
}

func TestMayorAgentJudgesInAReadOnlyCheckoutWithFullContext(t *testing.T) {
	b, x, task, _ := fixtureWorkers(t)
	update(t, b.Store, func(st *State) {
		current := st.Towns[x.ID]
		pending := current.Tasks[task.ID]
		pending.Stage, pending.House, pending.MayoralDecision = "awaiting_mayor", Hall, "pending"
		pending.Simplification = &Simplification{Mode: "suggest", Decision: "decline", Detail: "Adds a registry for one caller."}
	})
	task = b.Store.Snapshot().Towns[x.ID].Tasks[task.ID]
	var prompted string
	b.executeAgent = func(ctx context.Context, _ *Town, tree sessionTree, role string, _ *slog.Logger, prompt string) (string, error) {
		if role != string(Hall) {
			t.Fatalf("Mayor ran as %q", role)
		}
		if _, err := os.Stat(filepath.Join(tree.dir, "code.txt")); err != nil {
			t.Fatalf("the Mayor has no checkout of the branch: %v", err)
		}
		path := strings.TrimSuffix(strings.TrimSpace(strings.SplitN(strings.SplitN(prompt, "JSON context at ", 2)[1], "\n", 2)[0]), ".")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		prompted = string(raw)
		return "Weighed the advice.\nMAYOR_DECISION " + judgmentJSON(Judgment{Decision: "decline", Reason: "The registry is not worth its complexity."}), nil
	}
	verdict, err := b.Judge(context.Background(), x, task, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != "decline" || !strings.Contains(verdict.Reason, "complexity") {
		t.Fatalf("wrong verdict %+v", verdict)
	}
	var supplied struct {
		Arrival map[string]json.RawMessage `json:"arrival"`
	}
	if err := json.Unmarshal([]byte(prompted), &supplied); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"simplifier_advice", "source", "discussion", "title"} {
		if _, ok := supplied.Arrival[key]; !ok {
			t.Fatalf("Mayor context lacks %s: %s", key, prompted)
		}
	}
	extensions, err := os.ReadDir(filepath.Join(b.Root, "towns", Key(x.ID), "extensions", string(Hall)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range extensions {
		if strings.HasPrefix(entry.Name(), "work-") {
			t.Fatal("the Mayor's worktree was not removed")
		}
	}
	b.executeAgent = func(ctx context.Context, _ *Town, tree sessionTree, _ string, _ *slog.Logger, _ string) (string, error) {
		if err := os.WriteFile(filepath.Join(tree.dir, "code.txt"), []byte("edited\n"), 0600); err != nil {
			return "", err
		}
		return `MAYOR_DECISION {"decision":"admit","reason":"Edited it."}`, nil
	}
	if _, err := b.Judge(context.Background(), x, task, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil || !strings.Contains(err.Error(), "changed tracked source") {
		t.Fatalf("an agent that edits the checkout was accepted: %v", err)
	}
	b.executeAgent = func(context.Context, *Town, sessionTree, string, *slog.Logger, string) (string, error) {
		return `MAYOR_DECISION {"decision":"merge","reason":"Ship it."}`, nil
	}
	if verdict, err = b.Judge(context.Background(), x, task, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	store := testStore(t, false)
	y := autoMayorTown(t, store, true)
	sup := NewSupervisor(store, nil, &judging{workerFunc: idleWorker, verdicts: map[string]Judgment{"issue:1": verdict, "pr:4": {Decision: "delay", Reason: "later"}, upgradeTaskID(Feature): {Decision: "admit", Reason: "fine"}}})
	judgeAll(t, sup, y.ID)
	town := store.Snapshot().Towns[y.ID]
	if town.Tasks["issue:1"].MayoralDecision != "pending" || !strings.Contains(town.Tasks["issue:1"].Detail, `invalid decision "merge"`) {
		t.Fatalf("an invalid decision was applied: %+v", town.Tasks["issue:1"])
	}
	if town.Tasks["pr:4"].MayoralDecision != "pending" || !strings.Contains(town.Tasks["pr:4"].Detail, "bot update decisions only") {
		t.Fatalf("delay was accepted for a pull request: %+v", town.Tasks["pr:4"])
	}
}

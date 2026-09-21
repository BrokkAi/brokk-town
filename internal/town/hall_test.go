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

	"github.com/BrokkAi/acp-go/runner"
)

// pendingAt places a task at Town Hall awaiting the Mayor.
func pendingAt(t *Town, task *Task) *Task {
	task.House, task.Stage, task.MayoralDecision = Hall, "awaiting_mayor", "pending"
	task.Updated = time.Now()
	t.Tasks[task.ID] = task
	return task
}

func hallTown(t *testing.T, store *Store) *Town {
	t.Helper()
	x := addTown(t, store)
	update(t, store, func(st *State) {
		current := st.Towns[x.ID]
		current.Initialized = true
		current.Workers[Hall].Enabled = true
		pendingAt(current, &Task{ID: "issue:1", Kind: "issue", Number: 1, Title: "Bug Bot finding", Simplification: &Simplification{Mode: "suggest", Decision: "admit", Detail: "Focused work."}})
		pendingAt(current, &Task{ID: "pr:4", Kind: "pr", Number: 4, Title: "Outside PR", External: true, Head: fixSHA, Base: baseSHA})
		pendingAt(current, &Task{ID: upgradeTaskID(Feature), Kind: "upgrade", Title: "Feature Bot 0.1.2 is available", Upgrade: &BotUpgrade{Role: Feature, From: "0.1.1", To: "0.1.2"}})
	})
	return store.Snapshot().Towns[x.ID]
}

func TestMayorHouseIsAnAgentHouseAtTownHall(t *testing.T) {
	if !ValidAgentRole(Hall) || !ValidRole(Hall) || workerPackageNames[Hall] != "@brokkai/mayor-bot" || botDisplayName(Hall) != "Mayor Bot" {
		t.Fatal("Town Hall is not wired to mayor-bot")
	}
	store := testStore(t, false)
	x := addTown(t, store)
	if w := x.Workers[Hall]; w == nil || w.Enabled || w.Status != "paused" {
		t.Fatalf("a new town's Mayor Bot must start paused: %+v", x.Workers[Hall])
	}
	run := &WorkerRun{Bot: "mayor-bot", Version: "0.1.0", Command: "/bin/bmb", PID: 1, Socket: "/tmp/s", Mode: "judge", PR: 4, HeadSHA: fixSHA}
	if err := run.Validate(Hall); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]WorkerRun{
		"unknown duty":       {Bot: "mayor-bot", Version: "0.1.0", Command: "/bin/bmb", PID: 1, Socket: "/tmp/s", Mode: "scan"},
		"bulletin with a PR": {Bot: "mayor-bot", Version: "0.1.0", Command: "/bin/bmb", PID: 1, Socket: "/tmp/s", Mode: "bulletin", PR: 4},
		"head without a PR":  {Bot: "mayor-bot", Version: "0.1.0", Command: "/bin/bmb", PID: 1, Socket: "/tmp/s", Mode: "judge", Issue: 1, HeadSHA: fixSHA},
	} {
		bad := bad
		if err := bad.Validate(Hall); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestNextJudgmentOrderBackoffAndArrivalContext(t *testing.T) {
	store := testStore(t, false)
	x := hallTown(t, store)
	now := time.Now()
	if task := nextJudgment(x, now); task == nil || task.ID != "issue:1" {
		t.Fatalf("expected the lowest pending arrival first, got %+v", task)
	}
	x.Tasks["issue:1"].Attempts, x.Tasks["issue:1"].RetryAt = 1, now.Add(mayorRetryDelay)
	if task := nextJudgment(x, now); task == nil || task.ID != "pr:4" {
		t.Fatalf("a backed-off arrival was not skipped: %+v", task)
	}
	if task := nextJudgment(x, now.Add(mayorRetryDelay+time.Second)); task == nil || task.ID != "issue:1" {
		t.Fatalf("a backed-off arrival was not retried after its delay: %+v", task)
	}
	x.Tasks["issue:1"].Attempts = mayorAttempts
	x.Tasks["pr:4"].Attempts = mayorAttempts
	if task := nextJudgment(x, now.Add(time.Hour)); task == nil || task.ID != upgradeTaskID(Feature) {
		t.Fatalf("exhausted arrivals were judged again: %+v", task)
	}
	var arrival map[string]any
	if err := json.Unmarshal(arrivalContext(x.Tasks["issue:1"]), &arrival); err != nil {
		t.Fatal(err)
	}
	if arrival["kind"] != "issue" || arrival["title"] != "Bug Bot finding" || arrival["simplifier_advice"] == nil {
		t.Fatalf("issue arrival lacks its advice: %v", arrival)
	}
	if err := json.Unmarshal(arrivalContext(x.Tasks[upgradeTaskID(Feature)]), &arrival); err != nil {
		t.Fatal(err)
	}
	if arrival["kind"] != "upgrade" || arrival["bot_update"].(map[string]any)["to"] != "0.1.2" {
		t.Fatalf("upgrade arrival lacks its versions: %v", arrival)
	}
}

func TestBulletinWindowNeedsMergesAndTheInterval(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	if _, _, due := bulletinWindow(x, now); due {
		t.Fatal("a town with no merges owes no bulletin")
	}
	x.RecordOutcome(OutcomeRecord{ID: "merge:pr:1", At: now.Add(-time.Hour), Class: "outcome", Kind: "merge", Status: "confirmed", TaskID: "pr:1"})
	since, until, due := bulletinWindow(x, now)
	if !due || !until.Equal(now) || !since.Equal(now.Add(-time.Duration(DefaultBulletinSeconds)*time.Second)) {
		t.Fatalf("first bulletin window = %s..%s due=%v", since, until, due)
	}
	x.Bulletins = append(x.Bulletins, Bulletin{At: now, Since: since, Until: until, Title: "First", Items: []BulletinItem{}, Pulls: []int{1}})
	if _, _, due := bulletinWindow(x, now.Add(time.Hour)); due {
		t.Fatal("a bulletin was owed before the interval passed")
	}
	later := now.Add(7 * time.Hour)
	if _, _, due := bulletinWindow(x, later); due {
		t.Fatal("a bulletin was owed with nothing merged since the last one")
	}
	x.RecordOutcome(OutcomeRecord{ID: "merge:pr:2", At: now.Add(2 * time.Hour), Class: "outcome", Kind: "merge", Status: "confirmed", TaskID: "pr:2"})
	since, until, due = bulletinWindow(x, later)
	if !due || !since.Equal(now) || !until.Equal(later) {
		t.Fatalf("next window = %s..%s due=%v, want %s..%s", since, until, due, now, later)
	}
}

func TestMayorBotResultsApplyThroughTheMayorsPath(t *testing.T) {
	store := testStore(t, false)
	x := hallTown(t, store)
	results := map[string]RunResult{
		"issue:1":              {JudgedTask: "issue:1", Judgment: &Judgment{Decision: "admit", Reason: "A real defect with a bounded fix."}},
		"pr:4":                 {JudgedTask: "pr:4", Judgment: &Judgment{Decision: "decline", Reason: "Rewrites the scheduler for one flag."}},
		upgradeTaskID(Feature): {JudgedTask: upgradeTaskID(Feature), Judgment: &Judgment{Decision: "delay", Reason: "Let the release settle."}},
	}
	var seen []string
	workers := workerFunc(func(_ context.Context, t *Town, r Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		if r != Hall {
			return RunResult{}, nil
		}
		task := nextJudgment(t, time.Now())
		if task == nil {
			return RunResult{}, nil
		}
		seen = append(seen, task.ID)
		return results[task.ID], nil
	})
	sup := NewSupervisor(store, nil, workers)
	for i := 0; i < 3; i++ {
		sup.execute(context.Background(), store.Snapshot().Towns[x.ID], Hall, nil)
	}
	state := store.Snapshot()
	town := state.Towns[x.ID]
	issue, pr, upgrade := town.Tasks["issue:1"], town.Tasks["pr:4"], town.Tasks[upgradeTaskID(Feature)]
	if issue.Stage != "queued" || issue.House != Issue || issue.MayoralDecision != "admitted" || !strings.Contains(issue.Detail, "bounded fix") {
		t.Fatalf("issue was not admitted with the bot's reason: %+v", issue)
	}
	if pr.Stage != "declined" || !strings.Contains(pr.Detail, "for one flag") {
		t.Fatalf("PR was not declined with the bot's reason: %+v", pr)
	}
	if upgrade.Stage != "delayed" || upgrade.RetryAt.IsZero() {
		t.Fatalf("bot update was not delayed: %+v", upgrade)
	}
	named := 0
	for _, e := range state.Events {
		if e.Kind == "decision" && strings.HasPrefix(e.Title, "Mayor Bot ") {
			named++
		}
	}
	if named != 3 || len(seen) != 3 {
		t.Fatalf("expected three decisions attributed to Mayor Bot, got %d over %v", named, seen)
	}
	if w := town.Workers[Hall]; w.Status != "waiting" || w.Error != "" {
		t.Fatalf("the house did not return to waiting: %+v", w)
	}
}

func TestMayorBotFailuresBackOffThenLeaveTheArrivalForThePerson(t *testing.T) {
	store := testStore(t, false)
	x := hallTown(t, store)
	update(t, store, func(st *State) {
		delete(st.Towns[x.ID].Tasks, "pr:4")
		delete(st.Towns[x.ID].Tasks, upgradeTaskID(Feature))
	})
	calls := 0
	clock := time.Now()
	workers := workerFunc(func(_ context.Context, t *Town, r Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		if task := nextJudgment(t, clock); task != nil {
			calls++
			return RunResult{JudgedTask: task.ID}, errors.New("agent did not finish with a receipt")
		}
		return RunResult{}, nil
	})
	sup := NewSupervisor(store, nil, workers)
	sup.now = func() time.Time { return clock }
	sup.execute(context.Background(), store.Snapshot().Towns[x.ID], Hall, nil)
	town := store.Snapshot().Towns[x.ID]
	issue := town.Tasks["issue:1"]
	if issue.MayoralDecision != "pending" || issue.Attempts != 1 || !issue.RetryAt.After(clock) || !strings.Contains(issue.Detail, "attempt 1 of 3") {
		t.Fatalf("failed judgment did not back off: %+v", issue)
	}
	if w := town.Workers[Hall]; w.Status != "waiting" || w.Error != "" {
		t.Fatalf("one arrival's failure stopped the house: %+v", w)
	}
	for i := 0; i < 3; i++ {
		clock = clock.Add(mayorRetryDelay + time.Second)
		sup.execute(context.Background(), store.Snapshot().Towns[x.ID], Hall, nil)
	}
	issue = store.Snapshot().Towns[x.ID].Tasks["issue:1"]
	if calls != mayorAttempts || issue.Attempts != mayorAttempts || !strings.Contains(issue.Detail, "left for the Mayor") {
		t.Fatalf("expected %d attempts then a hand-off, got %d: %+v", mayorAttempts, calls, issue)
	}
	if err := sup.Control(x.ID, Hall, "admit", "issue:1"); err != nil {
		t.Fatal(err)
	}
	if issue = store.Snapshot().Towns[x.ID].Tasks["issue:1"]; issue.Attempts != 0 || issue.MayoralDecision != "admitted" {
		t.Fatalf("the Mayor's own decision did not clear the attempts: %+v", issue)
	}
}

func TestMayorBotBulletinsBuildAContiguousFeed(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	update(t, store, func(st *State) {
		current := st.Towns[x.ID]
		current.Initialized = true
		current.Workers[Hall].Enabled = true
		current.RecordOutcome(OutcomeRecord{ID: "merge:pr:12", At: now.Add(-time.Hour), Class: "outcome", Kind: "merge", Status: "confirmed", TaskID: "pr:12"})
	})
	var windows []Bulletin
	workers := workerFunc(func(_ context.Context, t *Town, r Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		since, until, due := bulletinWindow(t, now)
		if !due {
			return RunResult{}, nil
		}
		b := Bulletin{Since: since, Until: until, Title: "Exports you can trust", Summary: "Exports keep your filters.", Pulls: []int{12}, Items: []BulletinItem{{Kind: "fix", Title: "Exports keep the active filter", Detail: "An export no longer drops the filter.", Pulls: []int{12}, Issues: []int{9}}}}
		windows = append(windows, b)
		return RunResult{Bulletin: &b}, nil
	})
	sup := NewSupervisor(store, nil, workers)
	sup.now = func() time.Time { return now }
	sup.execute(context.Background(), store.Snapshot().Towns[x.ID], Hall, nil)
	town := store.Snapshot().Towns[x.ID]
	if len(town.Bulletins) != 1 || town.Bulletins[0].Title != "Exports you can trust" || !town.Bulletins[0].At.Equal(now) || town.Bulletins[0].Items[0].Kind != "fix" {
		t.Fatalf("bulletin was not recorded: %+v", town.Bulletins)
	}
	if town.Config.Public().BulletinSeconds != DefaultBulletinSeconds {
		t.Fatalf("public config does not report the bulletin cadence: %+v", town.Config.Public())
	}
	found := false
	for _, e := range store.Snapshot().Events {
		if e.Kind == "bulletin" && strings.Contains(e.Title, "Exports you can trust") {
			found = true
		}
	}
	if !found {
		t.Fatal("the bulletin was not announced")
	}
	sup.execute(context.Background(), store.Snapshot().Towns[x.ID], Hall, nil)
	if len(store.Snapshot().Towns[x.ID].Bulletins) != 1 || len(windows) != 1 {
		t.Fatal("a second bulletin was written with nothing new merged")
	}
	gap := Bulletin{Since: now.Add(time.Hour), Until: now.Add(2 * time.Hour), Title: "Gap", Summary: "s", Pulls: []int{}, Items: []BulletinItem{}}
	broken := NewSupervisor(store, nil, workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		return RunResult{Bulletin: &gap}, nil
	}))
	broken.now = func() time.Time { return now.Add(2 * time.Hour) }
	broken.execute(context.Background(), store.Snapshot().Towns[x.ID], Hall, nil)
	town = store.Snapshot().Towns[x.ID]
	if len(town.Bulletins) != 1 || !strings.Contains(town.Workers[Hall].Error, "continue the feed") {
		t.Fatalf("a bulletin with a gap was accepted: %d bulletins, error %q", len(town.Bulletins), town.Workers[Hall].Error)
	}
}

func TestMayorWorkerProtocolCarriesTheArrivalAndDuty(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	capture := filepath.Join(dir, "request.json")
	body := strings.Replace(pythonFakeWorker, `'bot': 'issue-bot'`, `'bot': 'mayor-bot'`, 1)
	body = strings.Replace(body, `['run', 'progress', 'issue-result', 'exact-issue']`, `['run', 'progress', 'mayor-judgment', 'mayor-bulletin']`, 1)
	body = strings.Replace(body, `{'issue': {'owned': [{'pr': 7, 'branch': 'town/7', 'issue': 7}]}}`, `{'judgment': {'decision': 'admit', 'reason': 'A real defect with a bounded fix.'}}`, 1)
	writeFakeWorker(t, fake, body)
	t.Setenv("TOWN_WORKER_TEST_CAPTURE", capture)
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent"}}
	pendingAt(x, &Task{ID: "issue:7", Kind: "issue", Number: 7, Title: "Complex request", Simplification: &Simplification{Mode: "suggest", Decision: "decline", Detail: "Adds a registry."}})
	workers := &BotWorkers{Root: dir, Store: store, BotCommands: map[Role]string{Hall: fake}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := workers.Run(context.Background(), x, Hall, func(Progress) {}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if result.JudgedTask != "issue:7" || result.Judgment == nil || result.Judgment.Decision != "admit" {
		t.Fatalf("unexpected mayor result: %+v", result)
	}
	var request struct {
		Issue   int             `json:"issue"`
		Mode    string          `json:"mode"`
		Arrival json.RawMessage `json:"arrival"`
		Since   *time.Time      `json:"since"`
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Issue != 7 || request.Mode != "judge" || request.Since != nil || !strings.Contains(string(request.Arrival), "Adds a registry") {
		t.Fatalf("worker request omitted the duty or arrival: %s", data)
	}
}

package town

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

func TestSimplifierIntakeRoutesArrivalsBeforeMayorAndWorkers(t *testing.T) {
	state := NewState(false)
	cfg := DefaultConfig("acme/orchard")
	cfg.SimplifierMode = "auto"
	town, err := state.Add(cfg)
	if err != nil {
		t.Fatal(err)
	}
	remote := inventory(pull(8))
	remote.Issues = []RemoteIssue{
		{Number: 7, Title: "Outside issue", State: "open"},
		{Number: 9, Title: "Bug finding", State: "open", Body: "<!-- bug-bot:1 -->"},
		{Number: 10, Title: "Remove the unused registry", State: "open", Body: "<!-- simplifier-bot:abc -->"},
	}
	Reconcile(&state, town, remote, time.Now())
	for _, id := range []string{"issue:7", "issue:9", "pr:8"} {
		task := town.Tasks[id]
		if task == nil || task.House != Simplifier || task.Stage != "simplifying" || task.MayoralDecision != "" {
			t.Fatalf("%s did not wait for simplifier: %+v", id, task)
		}
	}
	own := town.Tasks["issue:10"]
	if own == nil || own.House != Issue || own.Stage != "queued" || own.External || own.MayoralDecision != "" {
		t.Fatalf("simplifier's own auto-mode issue was recursively routed: %+v", own)
	}
}

func TestSimplifierSuggestAttachesAdviceAndAutoActs(t *testing.T) {
	for _, test := range []struct {
		mode     string
		decision string
		stage    string
		house    Role
		mayor    string
	}{{
		mode: "suggest", decision: "decline", stage: "awaiting_mayor", house: Hall, mayor: "pending",
	}, {
		mode: "auto", decision: "decline", stage: "declined", house: Hall,
	}, {
		mode: "auto", decision: "admit", stage: "queued", house: Issue,
	}} {
		state := NewState(false)
		town, _ := state.Add(DefaultConfig("acme/orchard"))
		task := &Task{ID: "issue:12", Kind: "issue", Number: 12, Title: "Complex feature", Stage: "simplifying", House: Simplifier, Updated: time.Now()}
		town.Tasks[task.ID] = task
		assessment := &Simplification{Mode: test.mode, Decision: test.decision, Summary: "Low value", Detail: "One caller and no acceptance coverage."}
		applySimplification(&state, town, task, assessment, nil, time.Now())
		if task.Stage != test.stage || task.House != test.house || task.MayoralDecision != test.mayor || task.Simplification != assessment {
			t.Fatalf("mode=%s decision=%s routed %+v", test.mode, test.decision, task)
		}
	}
}

func TestSimplifierAutoDeclineClosesIssue(t *testing.T) {
	store := testStore(t, false)
	town := addTown(t, store)
	task := &Task{ID: "issue:21", Kind: "issue", Number: 21, Title: "Speculative matrix", Stage: "declined", House: Hall, External: true, Simplification: &Simplification{Mode: "auto", Decision: "decline", Detail: "No callers."}, Updated: time.Now()}
	update(t, store, func(st *State) { st.Towns[town.ID].Tasks[task.ID] = task })
	gh := &fakeGH{snapshot: inventory()}
	gh.snapshot.Issues = []RemoteIssue{{Number: 21, Title: task.Title, State: "open"}}
	supervisor := NewSupervisor(store, gh, nil)
	if err := supervisor.reconcile(context.Background(), store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closed) != 1 || gh.closed[0] != 21 {
		t.Fatalf("auto decline did not close exactly once: %v", gh.closed)
	}
	if task := store.Snapshot().Towns[town.ID].Tasks[task.ID]; task.Stage != "declined" || task.Simplification.Decision != "decline" {
		t.Fatalf("closure erased simplifier decision: %+v", task)
	}
}

func TestSupervisorAppliesSimplifierResult(t *testing.T) {
	store := testStore(t, false)
	town := addTown(t, store)
	update(t, store, func(st *State) {
		current := st.Towns[town.ID]
		current.Workers[Simplifier].Enabled = true
		current.Tasks["issue:31"] = &Task{ID: "issue:31", Kind: "issue", Number: 31, Title: "Tiny focused fix", Stage: "simplifying", House: Simplifier, Updated: time.Now()}
	})
	workers := workerFunc(func(_ context.Context, _ *Town, role Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		if role != Simplifier {
			t.Fatal(role)
		}
		return RunResult{Issue: 31, Simplification: &Simplification{Mode: "auto", Decision: "admit", Detail: "Focused and useful."}}, nil
	})
	supervisor := NewSupervisor(store, nil, workers)
	supervisor.execute(context.Background(), store.Snapshot().Towns[town.ID], Simplifier, nil)
	task := store.Snapshot().Towns[town.ID].Tasks["issue:31"]
	if task.House != Issue || task.Stage != "queued" || task.Attempts != 0 || task.Simplification.Decision != "admit" {
		t.Fatalf("supervisor did not apply simplifier result: %+v", task)
	}
}

func TestSimplifierRepositoryScanHasNoItemAssessment(t *testing.T) {
	workers := &BotWorkers{}
	x := addTown(t, testStore(t, false))
	result, err := workers.complete(context.Background(), x, Simplifier, dispatch{mode: "auto"}, workerResult{}, nil, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Issue != 0 || result.PR != 0 || result.Simplification != nil {
		t.Fatalf("scan returned item state: %+v", result)
	}
}

func TestSimplifierWorkerProtocolCarriesModeAndTypedAssessment(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	capture := filepath.Join(dir, "request.json")
	body := strings.Replace(pythonFakeWorker, `'bot': 'issue-bot'`, `'bot': 'simplifier-bot'`, 1)
	body = strings.Replace(body, `['run', 'progress', 'issue-result', 'exact-issue']`, `['run', 'progress', 'simplifier-review']`, 1)
	body = strings.Replace(body, `{'issue': {'owned': [{'pr': 7, 'branch': 'town/7', 'issue': 7}]}}`, `{'simplification': {'mode': 'suggest', 'decision': 'decline', 'summary': 'Low value', 'detail': 'No callers.'}}`, 1)
	writeFakeWorker(t, fake, body)
	t.Setenv("TOWN_WORKER_TEST_CAPTURE", capture)
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent"}}
	x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, Title: "Complex request", Stage: "simplifying", House: Simplifier, Updated: time.Now()}
	workers := &BotWorkers{Root: dir, Store: store, BotCommands: map[Role]string{Simplifier: fake}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := workers.Run(context.Background(), x, Simplifier, func(Progress) {}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if result.Issue != 7 || result.Simplification == nil || result.Simplification.Decision != "decline" || result.Simplification.Mode != "suggest" {
		t.Fatalf("unexpected simplifier result: %+v", result)
	}
	var request struct {
		Issue int    `json:"issue"`
		Mode  string `json:"mode"`
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Issue != 7 || request.Mode != "suggest" {
		t.Fatalf("worker request omitted target or mode: %+v", request)
	}
}

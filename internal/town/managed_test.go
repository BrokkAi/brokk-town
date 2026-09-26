package town

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

func TestManagedCertificationReadsExactTargetDiff(t *testing.T) {
	b, x, task, _ := fixtureWorkers(t)
	x.Config.Execution = &mjolnir.Selection{Target: "builder", Profile: "coder"}
	stop := errors.New("checked prompt; no real agent")
	b.executeAgent = func(_ context.Context, _ *Town, tree sessionTree, role string, _ *slog.Logger, prompt string) (string, error) {
		if role != "review" || !strings.Contains(prompt, task.Base+" "+task.Head) || !strings.Contains(prompt, "git diff --no-ext-diff") || strings.Contains(prompt, "diff --git") || strings.Contains(prompt, tree.dir) || !strings.Contains(prompt, "all discussion retained") {
			t.Fatal("managed certification lost the exact diff or complete evidence")
		}
		return "", stop
	}
	_, err := b.certify(t.Context(), x, task, map[string]string{"existing": "all discussion retained"}, nil, slog.Default())
	if !errors.Is(err, stop) {
		t.Fatalf("certification did not reach the guarded remote prompt: %v", err)
	}
}

func TestManagedFailurePreservesActionableCause(t *testing.T) {
	b, x, _ := managedFixture(t)
	m, err := b.beginManaged(t.Context(), x, Review, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.execute(t.Context(), strings.Repeat("a", 40), strings.Repeat("x", 65537), false)
	if err == nil || !errors.Is(err, m.failure()) || !strings.Contains(m.failure().Error(), "65536") || len(m.runs) != 0 {
		t.Fatal("callback discarded the actionable managed failure", err)
	}
}

func managedFixture(t *testing.T) (*BotWorkers, *Town, *atomic.Int32) {
	t.Helper()
	x := &Town{ID: "acme/app", Config: DefaultConfig("acme/app"), Tasks: map[string]*Task{}}
	x.Config.Execution = &mjolnir.Selection{Target: "builder", Profile: "coder"}
	x.Config.Agent.Command = []string{"must-never-run-local-harness"}
	x.Config.Agent.Model = "selected-model"
	calls := &atomic.Int32{}
	catalog := runtimeCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Mj-Api-Version", "1")
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" {
			t.Error("unexpected mutation", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/v1/sessions/discovery":
			runtimeResponse(w, "saved-runtime")
		case "/api/v1/options":
			fmt.Fprint(w, `{"revision":1,"profiles":[{"id":"coder","harness":"codex"}],"targets":[{"id":"builder","kind":"docker","availability":"ready"}],"bundles":[{"id":"bundle","primary_repository":"project","repositories":[{"id":"project","github":"acme/app","destination":"app"}]}]}`)
		case "/api/v1/workspaces":
			json.NewEncoder(w).Encode(map[string]any{"workspaces": []mjolnir.Workspace{{ID: "workspace", Name: "Town " + Key(x.ID)}}})
		default:
			t.Error("unexpected managed request", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	pin, err := catalog.DiscoverRuntime(t.Context(), *x.Config.Execution, "discovery")
	if err != nil {
		t.Fatal(err)
	}
	x.Config.ExecutionRuntimes = map[string]mjolnir.RuntimePin{runtimeSelectionKey(*x.Config.Execution): pin}
	return &BotWorkers{Root: t.TempDir(), Mjolnir: catalog}, x, calls
}

func TestManagedDispatchFreezesSettingsAndHoldsRetainedRuns(t *testing.T) {
	b, x, calls := managedFixture(t)
	m, err := b.beginManaged(t.Context(), x, Review, nil)
	if err != nil {
		t.Fatal(err)
	}
	x.Config.Agent.Model = "changed-after-dispatch"
	if m.config.Agent.Model != "selected-model" {
		t.Fatal("settings changed during dispatch")
	}
	id := "retained"
	plan := mjolnir.RunPlan{ID: id, Repository: x.Config.Repo, Selection: *m.config.Execution,
		Placement: m.placement, Runtime: m.runtime,
		Checkout: mjolnir.ExactCheckout{Repository: "project", Commit: strings.Repeat("a", 40), Branch: "town/" + id}}
	run, err := m.executor.Runs.Prepare(t.Context(), plan, func(_ context.Context, _ mjolnir.RunPlan) (string, error) { return "", errors.New("lost response") })
	if err == nil || run == nil || run.Record().State != "uncertain" {
		t.Fatal("missing uncertain run")
	}
	before := calls.Load()
	if _, err := b.beginManaged(t.Context(), x, Review, nil); err == nil || !strings.Contains(err.Error(), "retained") || calls.Load() != before {
		t.Fatal("new dispatch ignored an unresolved launch intent", err)
	}
}

func TestManagedSocketRequiresExactRevisionAndRetainsRefusals(t *testing.T) {
	head := strings.Repeat("a", 40)
	m := &managedDispatch{failed: true}
	path, close, err := startRemoteAgent(t.Context(), m, head)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("callback socket is not private")
	}
	client := workerClient(path)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"protocol":1,"head":"wrong","prompt":"review"}`, 400},
		{`{"protocol":2,"head":"` + head + `","prompt":"review"}`, 400},
		{`{"protocol":1,"head":"` + head + `","prompt":"review","command":"local"}`, 400},
		{`{"protocol":1,"head":"` + head + `","prompt":"review"}{}`, 400},
		{`{"protocol":1,"head":"` + head + `","prompt":"review"}`, 409},
	} {
		req, _ := http.NewRequestWithContext(t.Context(), "POST", "http://worker/v1/agent", strings.NewReader(tc.body))
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.status || strings.Contains(string(body), "evidence\"") {
			t.Fatalf("invalid callback accepted: %d %s", res.StatusCode, body)
		}
	}
	if len(m.runs) != 0 {
		t.Fatal("refused callback created a run")
	}
}

func TestManagedUnsupportedDutiesAndDemoRemainOffline(t *testing.T) {
	b, x, calls := managedFixture(t)
	before := calls.Load()
	for _, role := range []Role{Bug, Feature, Issue, Release, Hall, Simplifier} {
		if executionHold(x, role) == "" {
			t.Fatal("unsupported duty was schedulable", role)
		}
		if _, err := b.beginManaged(t.Context(), x, role, nil); err == nil {
			t.Fatal("unsupported duty dispatched", role)
		}
	}
	if executionHold(x, Review) != "" {
		t.Fatal("known review runtime is still held")
	}
	x.Tasks["pr:1"] = &Task{ID: "pr:1", Kind: "pr", Number: 1, House: Issue, Stage: "fixes"}
	if executionHold(x, Issue) != "" {
		t.Fatal("review repair was held")
	}
	b.Store = testStore(t, true)
	if _, err := b.beginManaged(t.Context(), x, Review, nil); err == nil || calls.Load() != before {
		t.Fatal("demo or unsupported work contacted Mjolnir")
	}
}

func TestManagedWorkerCapabilitiesRefuseBeforeRunIntent(t *testing.T) {
	for _, request := range []workerRequest{{RemoteAgent: "/private/agent.sock"}, {DryRun: true}} {
		p := &workerProcess{done: make(chan struct{}), gate: make(chan struct{}, 1), info: workerInitialize{Capabilities: []string{"run", "progress"}}}
		started := false
		_, err := p.run(t.Context(), request, false, time.Now().Add(time.Minute), nil, func(WorkerRun) error { started = true; return nil })
		if err == nil || started {
			t.Fatal("legacy worker received an unsupported request")
		}
	}
}

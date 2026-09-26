package town

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

func executionCatalog(t *testing.T) *mjolnir.Catalog {
	t.Helper()
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Mj-Api-Version", "1")
		fmt.Fprint(w, `{"revision":1,"profiles":[{"id":"coder","harness":"codex"}],"targets":[{"id":"builder","kind":"ssh-bare","availability":"unavailable","unavailable_reason":"Start the host."}],"bundles":[]}`)
	}))
	t.Cleanup(h.Close)
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	c := mjolnir.New(dir, false, mjolnir.Connection{URL: h.URL + "/api/v1", TokenFile: token})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(3 * time.Second)
	for c.List().Fetched.IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("catalog did not refresh", c.List())
		}
		time.Sleep(time.Millisecond)
	}
	return c
}

func TestExecutionInheritanceIsIndependentAndDurable(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	s := NewSupervisor(store, nil, nil)
	s.Mjolnir = executionCatalog(t)
	remote := mjolnir.Selection{Target: "builder", Profile: "coder"}
	local := mjolnir.Selection{}
	if err := s.SetExecution(x.ID, "", &remote); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExecution(x.ID, Review, &local); err != nil {
		t.Fatal(err)
	}
	if err := s.SettingsForRole(x.ID, Review, AgentSettings{Model: ptr("review-model")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Settings(x.ID, AgentSettings{Model: ptr("town-model")}); err != nil {
		t.Fatal(err)
	}
	get := func() Config { return store.Snapshot().Towns[x.ID].Config }
	c := get()
	if c.ExecutionForRole(Issue) != remote || c.ExecutionForRole(Review) != local || c.ForRole(Review).Agent.Model != "review-model" {
		t.Fatal(c)
	}
	if err := s.SetExecution(x.ID, Review, nil); err != nil {
		t.Fatal(err)
	}
	if get().ForRole(Review).Agent.Model != "review-model" || get().ExecutionForRole(Review) != remote {
		t.Fatal("placement reset lost agent override")
	}
	if err := s.SetExecution(x.ID, Review, &local); err != nil {
		t.Fatal(err)
	}
	if err := s.SettingsForRole(x.ID, Review, AgentSettings{Inherit: true}); err != nil {
		t.Fatal(err)
	}
	if get().ExecutionForRole(Review) != local || get().ForRole(Review).Agent.Model != "town-model" {
		t.Fatal("profile reset lost placement")
	}
	before := get()
	if err := s.SetExecution(x.ID, Review, &mjolnir.Selection{Target: "invented", Profile: "coder"}); err == nil {
		t.Fatal("accepted free-text target")
	}
	if !reflect.DeepEqual(before, get()) {
		t.Fatal("invalid update changed config")
	}
	public := get().Public()
	if public.Execution == nil || *public.Execution != remote || public.BotExecution[Review] != local {
		t.Fatal(public)
	}
	dir := filepath.Dir(store.path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored := reopened.Snapshot().Towns[x.ID].Config
	if restored.ExecutionForRole(Review) != local || restored.ExecutionForRole(Issue) != remote {
		t.Fatal("placement lost on restart")
	}
}

func TestManagedExecutionIsHeldWithoutBudgetOrLocalFallback(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	remote := mjolnir.Selection{Target: "builder", Profile: "coder"}
	update(t, store, func(st *State) {
		town := st.Towns[x.ID]
		town.Config.Execution = &remote
		town.Initialized = true
		for _, w := range town.Workers {
			w.Enabled = w.Role != Repo
		}
	})
	workers := workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		t.Error("managed dispatch reached local worker")
		return RunResult{}, nil
	})
	s := NewSupervisor(store, nil, workers)
	before := store.Snapshot().Towns[x.ID].Budget
	s.schedule(context.Background())
	// The dispatch itself must recheck placement if settings changed since scheduling.
	s.execute(context.Background(), x, Review)
	st := store.Snapshot()
	for _, r := range AgentRoles {
		if r == Repo {
			continue
		}
		w := st.Towns[x.ID].Workers[r]
		if w.Status != "waiting" || w.Phase != "execution" || w.Run != nil {
			t.Fatal(r, w)
		}
	}
	if !reflect.DeepEqual(before, st.Towns[x.ID].Budget) || s.activeWorkers() != 0 || s.claimRepair(x.ID) {
		t.Fatal("held execution spent capacity/budget")
	}
	bot := &BotWorkers{Root: t.TempDir()}
	if _, err := bot.Run(context.Background(), st.Towns[x.ID], Review, func(Progress) {}, slog.Default()); err == nil || !strings.Contains(err.Error(), "not available yet") {
		t.Fatal(err)
	}
	if _, err := s.ChoicesForRole(context.Background(), x.ID, Review, AgentSettings{}); err == nil || !strings.Contains(err.Error(), "not available yet") {
		t.Fatal("local model probe was not refused", err)
	}
	if err := s.SetExecution(x.ID, Review, &mjolnir.Selection{}); err != nil {
		t.Fatal(err)
	}
	if w := store.Snapshot().Towns[x.ID].Workers[Review]; w.Phase == "execution" || !w.Next.IsZero() {
		t.Fatal("local override did not release hold", w)
	}
}

func TestDispatchedExecutionAndRecoveryKeepOriginalSelection(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	update(t, store, func(st *State) { st.Towns[x.ID].Workers[Review].Enabled = true })
	started, release := make(chan struct{}), make(chan struct{})
	s := NewSupervisor(store, nil, workerFunc(func(_ context.Context, dispatched *Town, _ Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		close(started)
		<-release
		if dispatched.Config.Execution == nil || dispatched.Config.Execution.Managed() {
			t.Error("dispatch changed with settings")
		}
		return RunResult{}, nil
	}))
	s.Mjolnir = executionCatalog(t)
	done := make(chan struct{})
	go func() { s.execute(context.Background(), x, Review); close(done) }()
	<-started
	remote := mjolnir.Selection{Target: "builder", Profile: "coder"}
	if err := s.SetExecution(x.ID, "", &remote); err != nil {
		t.Fatal(err)
	}
	if w := store.Snapshot().Towns[x.ID].Workers[Review]; w.Execution == nil || w.Execution.Managed() {
		t.Fatal("running placement changed", w)
	}
	close(release)
	<-done
	w := Worker{Role: Review, Run: &WorkerRun{Execution: &remote, BaseSHA: baseSHA, HeadSHA: headSHA, PR: 1, Started: time.Now()}}
	recoverWorkerRun(&w)
	remote.Target = "edited"
	if w.Recovery.Execution.Target != "builder" || w.Recovery.Base != baseSHA || w.Recovery.Head != headSHA {
		t.Fatal("recovery identity lost", w.Recovery)
	}
	data, _ := json.Marshal(w.Recovery)
	if strings.Contains(string(data), "edited") {
		t.Fatal(string(data))
	}
}

func TestManagedSelectionPreservesUncertaintyAndBlocksInheritedProfileProbe(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	remote := mjolnir.Selection{Target: "builder", Profile: "coder"}
	update(t, store, func(st *State) {
		town := st.Towns[x.ID]
		town.Config.BotExecution = map[Role]mjolnir.Selection{Review: remote}
		for _, w := range town.Workers {
			w.Enabled = w.Role == Review
		}
		w := town.Workers[Review]
		w.Recovery = &WorkerRecovery{Detail: "Uncertain publication; inspect the saved evidence."}
		w.Phase, w.Task = "recovery", "Preserve original explanation"
	})
	s := NewSupervisor(store, nil, nil)
	s.schedule(context.Background())
	w := store.Snapshot().Towns[x.ID].Workers[Review]
	if w.Phase != "recovery" || w.Task != "Preserve original explanation" || w.Recovery == nil {
		t.Fatal(w)
	}
	// Inheriting the agent profile must not also inherit its execution location.
	if _, err := s.ChoicesForRole(context.Background(), x.ID, Review, AgentSettings{Inherit: true}); err == nil || !strings.Contains(err.Error(), "not available yet") {
		t.Fatal(err)
	}
	checks := s.setupChecks(context.Background(), store.Snapshot().Towns[x.ID].Config)
	found := false
	for _, check := range checks {
		if check.Role != Review {
			continue
		}
		if check.Code != "execution" || check.Status != "blocked" {
			t.Fatal("managed role received local readiness claim", check)
		}
		found = true
	}
	if !found {
		t.Fatal("missing execution diagnostic")
	}
}

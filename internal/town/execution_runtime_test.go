package town

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

func runtimeCatalog(t *testing.T, handler http.HandlerFunc) *mjolnir.Catalog {
	t.Helper()
	h := httptest.NewServer(handler)
	t.Cleanup(h.Close)
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return mjolnir.New(dir, false, mjolnir.Connection{URL: h.URL + "/api/v1", TokenFile: token})
}

func runtimeResponse(w http.ResponseWriter, id string) {
	w.Header().Set("Mj-Api-Version", "1")
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"discovery","workspace_id":"workspace","bundle_id":"bundle","target_id":"builder","profile_id":"coder","state":"Running","lifecycle":"live","chat_phase":"idle","is_idle":true,"has_error":false,"runtime":{"id":%q,"harness":"codex","platform":"linux-x86_64","provenance":"managed_installation","components":[{"name":"provider_cli","version":"1.2.3","sha256":null}],"event_ordinal":7,"observed_at_ms":1788000000000}}`, id)
}

func TestRuntimeSelectionPersistsWithoutLocalHarnessAndFreezesDispatch(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	s := NewSupervisor(store, nil, nil)
	var replacement atomic.Bool
	s.Mjolnir = runtimeCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/sessions/discovery" {
			t.Errorf("unexpected runtime operation %s %s", r.Method, r.URL)
		}
		id := "saved-runtime"
		if replacement.Load() {
			id = "new-runtime"
		}
		runtimeResponse(w, id)
	})
	selection := mjolnir.Selection{Target: "builder", Profile: "coder"}
	update(t, store, func(st *State) {
		c := &st.Towns[x.ID].Config
		c.Agent.Command = []string{"must-never-launch-local-harness"}
		c.Execution = &selection
		c.BotExecution = map[Role]mjolnir.Selection{Bug: {}}
	})
	if err := s.SelectExecutionRuntime(t.Context(), x.ID, Review, "discovery"); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot().Towns[x.ID].Config
	pin := before.ExecutionRuntimeForRole(Review)
	if pin == nil || pin.Runtime.ID != "saved-runtime" || before.ExecutionRuntimeForRole(Bug) != nil || before.ExecutionRuntimeForRole(Issue) == nil {
		t.Fatal("runtime inheritance differs from placement")
	}
	public := before.Public().BotAgents[Review]
	if public.Harness != "codex" || public.HarnessVersion != "provider_cli 1.2.3" {
		t.Fatalf("wrong remote profile %+v", public)
	}
	frozen := before.ForRole(Review)
	replacement.Store(true)
	// Reading the catalog/session never updates a saved selection implicitly.
	if _, err := s.Mjolnir.DiscoverRuntime(t.Context(), selection, "discovery"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, store.Snapshot().Towns[x.ID].Config) {
		t.Fatal("discovery silently upgraded runtime")
	}
	if err := s.SelectExecutionRuntime(t.Context(), x.ID, Review, "discovery"); err != nil {
		t.Fatal(err)
	}
	if frozen.ExecutionRuntimeForRole(Review).Runtime.ID != "saved-runtime" || store.Snapshot().Towns[x.ID].Config.ExecutionRuntimeForRole(Review).Runtime.ID != "new-runtime" {
		t.Fatal("explicit runtime update changed frozen work")
	}
	path := store.path
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filepath.Dir(path), false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Snapshot().Towns[x.ID].Config.ExecutionRuntimeForRole(Review); got == nil || got.Runtime.ID != "new-runtime" {
		t.Fatal("runtime selection did not survive restart")
	}
}

func TestRuntimeSelectionRefusesChangedPlacementAndDemo(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	s := NewSupervisor(store, nil, nil)
	selection := mjolnir.Selection{Target: "builder", Profile: "coder"}
	update(t, store, func(st *State) { st.Towns[x.ID].Config.Execution = &selection })
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.Mjolnir = runtimeCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		runtimeResponse(w, "saved-runtime")
	})
	done := make(chan error, 1)
	go func() { done <- s.SelectExecutionRuntime(t.Context(), x.ID, Review, "discovery") }()
	<-started
	update(t, store, func(st *State) { st.Towns[x.ID].Config.Execution = nil })
	close(release)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("lost settings race: %v", err)
	}
	if len(store.Snapshot().Towns[x.ID].Config.ExecutionRuntimes) != 0 {
		t.Fatal("saved stale runtime")
	}
	demo := testStore(t, true)
	d := NewSupervisor(demo, nil, nil)
	d.Mjolnir = s.Mjolnir
	if err := d.SelectExecutionRuntime(t.Context(), x.ID, Review, "discovery"); err == nil || calls.Load() != 1 {
		t.Fatal("demo contacted Mjolnir")
	}
}

func TestRuntimeConfigRejectsForgedPinAndMismatchedDispatch(t *testing.T) {
	c := DefaultConfig("acme/app")
	c.ExecutionRuntimes = map[string]mjolnir.RuntimePin{"invented": {}}
	if c.Validate() == nil {
		t.Fatal("accepted invalid runtime pin")
	}
	run := &WorkerRun{Runtime: &mjolnir.RuntimePin{}}
	if run.Validate(Review) == nil {
		t.Fatal("accepted worker runtime without evidence")
	}
}

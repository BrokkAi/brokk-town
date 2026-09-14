package town

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type capacityWorker struct {
	started chan string
	mu      sync.Mutex
	release map[string]chan struct{}
	cleanup <-chan struct{}
}

func (w *capacityWorker) Run(ctx context.Context, t *Town, r Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
	key := t.ID + ":" + string(r)
	w.started <- key
	w.mu.Lock()
	release := w.release[key]
	w.mu.Unlock()
	select {
	case <-release:
		return RunResult{}, nil
	case <-ctx.Done():
		if w.cleanup != nil {
			<-w.cleanup
		}
		return RunResult{}, ctx.Err()
	}
}

func addCapacityTown(t *testing.T, s *Store, repo string, roles ...Role) {
	t.Helper()
	if err := s.Update(func(st *State) error {
		x, err := st.Add(DefaultConfig(repo))
		if err != nil {
			return err
		}
		x.Initialized = true
		x.Workers[Repo].Enabled = false
		for _, role := range roles {
			x.Workers[role].Enabled = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func newCapacityWorker(t *testing.T, keys ...string) *capacityWorker {
	t.Helper()
	release := make(map[string]chan struct{}, len(keys))
	for _, key := range keys {
		release[key] = make(chan struct{})
	}
	return &capacityWorker{started: make(chan string, len(keys)+4), release: release}
}

func waitCapacityStarts(t *testing.T, started <-chan string, want int) []string {
	t.Helper()
	got := make([]string, 0, want)
	deadline := time.After(3 * time.Second)
	for len(got) < want {
		select {
		case key := <-started:
			got = append(got, key)
		case <-deadline:
			t.Fatalf("started %d workers, want %d (%v)", len(got), want, got)
		}
	}
	return got
}

func waitCapacity(t *testing.T, s *Store, active int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.Snapshot().Capacity.Active; got == active {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("active capacity = %d, want %d", s.Snapshot().Capacity.Active, active)
}

func TestCapacitySaturatesAndWakeStartsFreedSlot(t *testing.T) {
	s := testStore(t, false)
	if err := s.Update(func(st *State) error {
		st.ServiceConfig.MaxWorkers = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	addCapacityTown(t, s, "acme/one", Issue)
	addCapacityTown(t, s, "acme/two", Bug)
	addCapacityTown(t, s, "acme/three", Feature)
	w := newCapacityWorker(t, "acme/one:issue", "acme/two:bug", "acme/three:feature")
	sup := NewSupervisor(s, nil, w)
	ctx := context.Background()
	sup.schedule(ctx)
	started := waitCapacityStarts(t, w.started, 2)
	if s.Snapshot().Capacity.Active != 2 {
		t.Fatalf("capacity did not reserve both slots: %v", s.Snapshot().Capacity)
	}
	select {
	case extra := <-w.started:
		t.Fatalf("started over capacity: %s", extra)
	case <-time.After(100 * time.Millisecond):
	}
	close(w.release[started[0]])
	// There is no scheduler ticker in this test: releaseWorker must publish the
	// wake event before the freed slot can be scheduled.
	select {
	case <-sup.wake:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker cleanup did not wake the scheduler")
	}
	sup.schedule(ctx)
	third := waitCapacityStarts(t, w.started, 1)[0]
	want := 2
	if s.Snapshot().Capacity.Active != want {
		t.Fatalf("freed slot was not reserved: %v", s.Snapshot().Capacity)
	}
	closed := map[string]bool{started[0]: true}
	for _, key := range append(started[1:], third) {
		if !closed[key] {
			close(w.release[key])
			closed[key] = true
		}
	}
	waitCapacity(t, s, 0)
	sup.wg.Wait()
}

func TestCapacityIncreaseStartsWaitersAndDecreaseKeepsReservations(t *testing.T) {
	s := testStore(t, false)
	if err := s.Update(func(st *State) error {
		st.ServiceConfig.MaxWorkers = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"acme/one", "acme/two", "acme/three"} {
		addCapacityTown(t, s, repo, Issue)
	}
	w := newCapacityWorker(t, "acme/one:issue", "acme/two:issue", "acme/three:issue", "acme/four:issue")
	sup := NewSupervisor(s, nil, w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	first := waitCapacityStarts(t, w.started, 1)[0]
	select {
	case key := <-w.started:
		t.Fatalf("started over initial capacity: %s", key)
	case <-time.After(100 * time.Millisecond):
	}
	if err := sup.SetCapacity(3); err != nil {
		t.Fatal(err)
	}
	started := waitCapacityStarts(t, w.started, 2)
	if got := s.Snapshot().Capacity.Active; got != 3 {
		t.Fatalf("capacity increase did not reserve waiters: %d", got)
	}
	addCapacityTown(t, s, "acme/four", Issue)
	select {
	case key := <-w.started:
		t.Fatalf("pending worker started before the fourth town was below capacity: %s", key)
	case <-time.After(100 * time.Millisecond):
	}
	if err := sup.SetCapacity(1); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().Capacity.Active; got != 3 {
		t.Fatalf("capacity decrease canceled existing reservations: %d", got)
	}
	close(w.release[first])
	waitCapacity(t, s, 2)
	sup.schedule(ctx)
	select {
	case key := <-w.started:
		t.Fatalf("capacity reduction allowed a pending worker: %s", key)
	case <-time.After(100 * time.Millisecond):
	}
	close(w.release[started[0]])
	waitCapacity(t, s, 1)
	sup.schedule(ctx)
	select {
	case key := <-w.started:
		t.Fatalf("capacity reduction allowed a pending worker while one slot was still occupied: %s", key)
	case <-time.After(100 * time.Millisecond):
	}
	close(w.release[started[1]])
	waitCapacityStarts(t, w.started, 1)
	close(w.release["acme/four:issue"])
	waitCapacity(t, s, 0)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	sup.wg.Wait()
}

func TestCapacityReporterRunDoesNotConsumeAgentSlot(t *testing.T) {
	s := testStore(t, false)
	if err := s.Update(func(st *State) error {
		st.ServiceConfig.MaxWorkers = 1
		add, err := st.Add(DefaultConfig("acme/reporter"))
		if err != nil {
			return err
		}
		add.Workers[Repo].Enabled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	addCapacityTown(t, s, "acme/worker", Issue)
	w := newCapacityWorker(t, "acme/worker:issue")
	sup := NewSupervisor(s, newGH(1), w)
	sup.schedule(context.Background())
	waitCapacityStarts(t, w.started, 1)
	eventually(t, func() bool {
		state := s.Snapshot()
		reporter := state.Towns["acme/reporter"]
		return reporter.Workers[Repo].Status == "waiting" && state.Capacity.Active == 1
	})
	close(w.release["acme/worker:issue"])
	waitCapacity(t, s, 0)
	sup.wg.Wait()
}

func TestCapacityEnforcesPerTownRoleExclusivityAndIgnoresNonAgents(t *testing.T) {
	s := testStore(t, false)
	addCapacityTown(t, s, "acme/one", Issue)
	w := newCapacityWorker(t, "acme/one:issue")
	sup := NewSupervisor(s, nil, w)
	sup.mu.Lock()
	sup.running["acme/one:requests"] = func() {}
	sup.running["acme/one:choices"] = func() {}
	sup.running["acme/one:repo"] = func() {}
	sup.running["acme/one:issue"] = func() {}
	if got := sup.activeWorkers(); got != 1 {
		sup.mu.Unlock()
		t.Fatalf("non-agent reservations counted as active: %d", got)
	}
	delete(sup.running, "acme/one:issue")
	delete(sup.running, "acme/one:requests")
	delete(sup.running, "acme/one:choices")
	delete(sup.running, "acme/one:repo")
	sup.mu.Unlock()
	sup.schedule(context.Background())
	sup.schedule(context.Background())
	waitCapacityStarts(t, w.started, 1)
	select {
	case duplicate := <-w.started:
		t.Fatalf("same town-role started twice: %s", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
	close(w.release["acme/one:issue"])
	waitCapacity(t, s, 0)
	sup.wg.Wait()
}

func TestCapacityDeletionKeepsReservationUntilCleanup(t *testing.T) {
	s := testStore(t, false)
	addCapacityTown(t, s, "acme/one", Issue)
	w := newCapacityWorker(t, "acme/one:issue")
	cleanup := make(chan struct{})
	w.cleanup = cleanup
	sup := NewSupervisor(s, nil, w)
	sup.schedule(context.Background())
	waitCapacityStarts(t, w.started, 1)
	if err := sup.Delete("acme/one"); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().Capacity.Active; got != 1 {
		t.Fatalf("deleted town reservation released before worker cleanup: %d", got)
	}
	if len(s.Snapshot().Public()["towns"].(map[string]any)) != 0 {
		t.Fatal("deleted town remained public")
	}
	close(cleanup)
	waitCapacity(t, s, 0)
	sup.wg.Wait()
}

func TestCapacityExcludesRequestPublishingAndModelDiscovery(t *testing.T) {
	s := testStore(t, false)
	if err := s.Update(func(st *State) error {
		st.ServiceConfig.MaxWorkers = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	addCapacityTown(t, s, "acme/one", Issue)
	entered, release := make(chan struct{}), make(chan struct{})
	w := newCapacityWorker(t, "acme/one:issue")
	sup := NewSupervisor(s, nil, w)
	sup.Publisher = requestPublisher{post: func(context.Context, string, string, string) (RemoteIssue, error) {
		close(entered)
		<-release
		return RemoteIssue{Number: 11, URL: "https://github.com/acme/one/issues/11", State: "open", Body: requestMarker(sampleRequest().ID)}, nil
	}, find: func(context.Context, string, string) (*RemoteIssue, error) { return nil, nil }}
	sup.schedule(context.Background())
	waitCapacityStarts(t, w.started, 1)
	if got := s.Snapshot().Capacity.Active; got != 1 {
		t.Fatalf("agent did not consume the one available slot: %d", got)
	}
	if _, err := sup.SubmitRequest("acme/one", sampleRequest()); err != nil {
		t.Fatal(err)
	}
	// These are real scheduler reservations: request publishing and choices use
	// the same running map but neither belongs to the agent worker pool.
	sup.mu.Lock()
	sup.running["acme/one:choices"] = func() {}
	sup.mu.Unlock()
	sup.schedule(context.Background())
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request publisher did not start")
	}
	if got := s.Snapshot().Capacity.Active; got != 1 {
		t.Fatalf("request or choices reservation consumed agent capacity: %d", got)
	}
	close(release)
	close(w.release["acme/one:issue"])
	sup.mu.Lock()
	delete(sup.running, "acme/one:choices")
	sup.mu.Unlock()
	sup.wg.Wait()
	waitCapacity(t, s, 0)
}

func TestCapacityPersistenceMigrationValidationAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = (&Supervisor{Store: s, running: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1), fatal: make(chan error, 1), now: time.Now}).SetCapacity(9); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().ServiceConfig.MaxWorkers; got != 9 {
		t.Fatalf("capacity was not committed: %d", got)
	}
	for _, invalid := range []int{0, -1, MaximumMaxWorkers + 1} {
		if err := (&Supervisor{Store: s, running: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1), fatal: make(chan error, 1), now: time.Now}).SetCapacity(invalid); err == nil {
			t.Fatalf("accepted invalid capacity %d", invalid)
		}
		if got := s.Snapshot().ServiceConfig.MaxWorkers; got != 9 {
			t.Fatalf("invalid capacity changed saved value: %d", got)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (&Supervisor{Store: s, running: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1), fatal: make(chan error, 1), now: time.Now}).SetCapacity(8); err == nil {
		t.Fatal("closed store accepted capacity update")
	}
	s, err = Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().ServiceConfig.MaxWorkers; got != 9 || s.Snapshot().Capacity.Active != 0 {
		t.Fatalf("restart did not preserve limit or reset runtime count: %+v", s.Snapshot())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	base := NewState(false)
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{name: "absent", edit: func(fields map[string]any) { delete(fields, "service_config") }},
		{name: "null", edit: func(fields map[string]any) { fields["service_config"] = nil }},
		{name: "empty", edit: func(fields map[string]any) { fields["service_config"] = map[string]any{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err = json.Unmarshal(b, &fields); err != nil {
				t.Fatal(err)
			}
			tc.edit(fields)
			path := filepath.Join(t.TempDir(), "state.json")
			b, _ = json.Marshal(fields)
			if err = os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(path)
			got, err := Open(dir, false)
			if tc.name == "absent" {
				if err != nil || got.Snapshot().ServiceConfig.MaxWorkers != DefaultMaxWorkers {
					t.Fatalf("absent service config did not migrate: %v", err)
				}
				got.Close()
				return
			}
			if err == nil {
				got.Close()
				t.Fatal("present invalid service config was accepted")
			}
		})
	}
}

func TestDemoCapacityDerivesActiveFromWorkerStatus(t *testing.T) {
	s := testStore(t, true)
	addCapacityTown(t, s, "acme/one", Bug)
	update(t, s, func(st *State) {
		x := st.Towns["acme/one"]
		x.Workers[Bug].Status = "working"
		x.Workers[Issue].Status = "pausing"
		x.Workers[Repo].Status = "working"
		x.Workers[Review].Status = "working"
		x.Deleted = false
	})
	if got := s.Snapshot().Capacity.Active; got != 3 {
		t.Fatalf("demo active status count = %d, want 3", got)
	}
	update(t, s, func(st *State) { st.Towns["acme/one"].Deleted = true })
	if got := s.Snapshot().Capacity.Active; got != 0 {
		t.Fatalf("deleted demo town counted as active: %d", got)
	}
}

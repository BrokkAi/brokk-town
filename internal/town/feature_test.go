package town

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestFeatureWorkerUsesDiscoveryCadenceAndNoSyntheticDelivery(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) { st.Towns[x.ID].Workers[Feature].Enabled = true })
	now := time.Now()
	called := false
	sup := NewSupervisor(s, nil, workerFunc(func(_ context.Context, _ *Town, role Role, progress func(Progress), _ *slog.Logger) (RunResult, error) {
		called = true
		if role != Feature {
			t.Fatalf("wrong role: %s", role)
		}
		progress(Progress{Phase: "reviewing", Task: "Comparing the proposal against issue history"})
		return RunResult{}, nil
	}))
	sup.now = func() time.Time { return now }
	sup.execute(context.Background(), x, Feature)
	state := s.Snapshot()
	w := state.Towns[x.ID].Workers[Feature]
	if !called || !w.Next.Equal(now.Add(30*time.Minute)) || w.Status != "waiting" {
		t.Fatalf("wrong feature cadence: %+v", w)
	}
	for _, event := range state.Events {
		if event.Kind == "delivery" {
			t.Fatal("worker success must not invent a filed issue")
		}
	}
}

func TestFeatureDemoNeverSchedulesRealWork(t *testing.T) {
	s := testStore(t, true)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		town.Workers[Feature].Enabled = true
	})
	sup := NewSupervisor(s, nil, workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		t.Error("demo launched a real worker")
		return RunResult{}, nil
	}))
	sup.schedule(context.Background())
	sup.wg.Wait()
}

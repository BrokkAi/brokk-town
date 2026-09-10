package town

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExistingTownAddsPausedFeatureWorker(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "invalid worker"}[corrupt], func(t *testing.T) {
			dir := t.TempDir()
			state := NewState(false)
			town, err := state.Add(DefaultConfig("acme/orchard"))
			if err != nil {
				t.Fatal(err)
			}
			town.Workers[Bug].Enabled = true
			delete(town.Workers, Feature)
			if corrupt {
				town.Workers[Feature] = nil
			}
			data, _ := json.Marshal(state)
			if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			store, err := Open(dir, false)
			if corrupt {
				if err == nil {
					store.Close()
					t.Fatal("silently repaired a corrupt existing worker")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			saved := store.Snapshot().Towns[town.ID]
			w := saved.Workers[Feature]
			if w == nil || w.Enabled || w.Status != "paused" || !saved.Workers[Bug].Enabled {
				t.Fatalf("migration changed automation: %+v", saved.Workers)
			}
			if err := store.Update(func(*State) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

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
		progress(Progress{"reviewing", "Comparing the proposal against issue history"})
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

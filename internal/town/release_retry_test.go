package town

import (
	"context"
	"log/slog"
	"testing"
)

type releaseRetryWorker struct {
	runs    int
	retried bool
}

func (w *releaseRetryWorker) Run(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
	w.runs++
	return RunResult{Retried: w.retried}, nil
}

func TestReleaseRetryControlIsConsumedByTheNextRun(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		w := st.Towns[x.ID].Workers[Release]
		w.Enabled = false
		w.Status = "failed"
		w.Error = "release retry budget exhausted; fix the reported failure and run release-bot retry"
	})
	worker := &releaseRetryWorker{}
	sup := NewSupervisor(s, nil, worker)
	if err := sup.Control(x.ID, Issue, "retry", ""); err == nil {
		t.Fatal("retry without a task must still name a task for other houses")
	}
	if err := sup.Control(x.ID, Release, "retry", ""); err != nil {
		t.Fatal(err)
	}
	w := s.Snapshot().Towns[x.ID].Workers[Release]
	if !w.RetryRequested || !w.Enabled || !w.Next.IsZero() || w.Error != "" {
		t.Fatalf("retry request did not schedule the release house: %+v", w)
	}
	// A run that could not reach the worker API keeps the request pending.
	sup.execute(context.Background(), s.Snapshot().Towns[x.ID], Release)
	if w = s.Snapshot().Towns[x.ID].Workers[Release]; !w.RetryRequested || worker.runs != 1 {
		t.Fatalf("request consumed without the worker accepting it: %+v runs=%d", w, worker.runs)
	}
	worker.retried = true
	sup.execute(context.Background(), s.Snapshot().Towns[x.ID], Release)
	if w = s.Snapshot().Towns[x.ID].Workers[Release]; w.RetryRequested || worker.runs != 2 {
		t.Fatalf("accepted retry was not consumed: %+v runs=%d", w, worker.runs)
	}
}

// Observe reports an empty repository: this fixture's subject is dispatch, not
// the inventory.
func (w *releaseRetryWorker) Observe(context.Context, *Town, InventoryRequest, func(Progress), *slog.Logger) (RunResult, error) {
	return RunResult{Inventory: &RepoSnapshot{Branch: "main", DefaultBranch: "main", Head: baseSHA}}, nil
}

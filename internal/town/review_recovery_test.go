package town

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestExhaustedReviewRefreshesInventoryWithoutStallingOtherPRs(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	at := time.Now()
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		task := town.Tasks["pr:1"]
		task.Stage, task.Audit, task.Attempts = "queued", nil, 4
		town.Tasks["pr:2"] = &Task{ID: "pr:2", Kind: "pr", Number: 2, Stage: "queued", House: Review, Base: baseSHA, Head: headSHA}
		town.Workers[Review].Enabled = true
		town.Workers[Repo].Next = at.Add(time.Hour)
	})
	sup := NewSupervisor(s, newGH(1), workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		return RunResult{PR: 1}, &ReviewAttemptError{Status: "stale", Detail: "revision changed", ExpectedBase: baseSHA, ExpectedHead: headSHA}
	}))
	sup.now = func() time.Time { return at }
	sup.execute(context.Background(), s.Snapshot().Towns[x.ID], Review, nil)
	got := s.Snapshot().Towns[x.ID]
	if !got.Tasks["pr:1"].Blocked || got.Tasks["pr:1"].Audit != nil {
		t.Fatal("incomplete review certified or retry limit lost")
	}
	if !got.Workers[Repo].Next.IsZero() {
		t.Fatal("inventory refresh not requested")
	}
	if !got.Workers[Review].Next.Equal(at.Add(time.Duration(got.Config.PollSeconds) * time.Second)) {
		t.Fatal("failed PR delayed entire house")
	}
	if next := nextTask(got, Review, "queued"); next == nil || next.Number != 2 {
		t.Fatal("unrelated review cannot proceed")
	}
}

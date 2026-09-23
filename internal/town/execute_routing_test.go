package town

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// execute routes each worker result to whoever owns it: a failure on one pull
// request belongs to that pull request, a failure without one belongs to the
// house, an audit is applied only to the exact revision it covers, and a
// cancellation is recorded as abandoned rather than failed.
func TestExecuteRoutesWorkerResults(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	stale := clean()
	stale.Head = fixSHA
	inconclusive := clean()
	inconclusive.Verdict = "inconclusive"
	inconclusive.Summary = "tests would not start"
	withSeverity := clean()
	withSeverity.Findings = []Finding{{ID: "new:nit", State: "resolved", Detail: "style", Severity: "P3"}}

	type check func(t *testing.T, st State, x *Town, w *Worker, task *Task)
	for _, tc := range []struct {
		name   string
		role   Role
		result RunResult
		err    error
		panic  bool
		status string // the attempt outcome recorded for the role
		check  check
	}{
		{name: "house_failure", role: Bug, err: errors.New("scanner crashed"), status: "blocked", check: func(t *testing.T, st State, x *Town, w *Worker, task *Task) {
			if w.Status != "failed" || w.Error != "scanner crashed" || !strings.Contains(w.Task, "Work paused: scanner crashed") || !w.Next.Equal(now.Add(15*time.Minute)) {
				t.Fatalf("house failure not recorded on the house: %+v", w)
			}
			if !hasEvent(st, x.ID, "error", string(Bug)) {
				t.Fatal("house failure raised no event")
			}
		}},
		{name: "panic", role: Issue, panic: true, status: "blocked", check: func(t *testing.T, _ State, _ *Town, w *Worker, _ *Task) {
			if w.Status != "failed" || !strings.Contains(w.Error, "worker panic: boom") {
				t.Fatalf("panic not reported as a house failure: %+v", w)
			}
		}},
		{name: "pull_failure_owned_by_pull", role: Issue, result: RunResult{PR: 1}, err: errors.New("push rejected"), status: "blocked", check: func(t *testing.T, st State, x *Town, w *Worker, task *Task) {
			if w.Status != "waiting" || w.Error != "" {
				t.Fatalf("a pull request failure failed the house: %+v", w)
			}
			if task.Attempts != 1 || task.RetryAt.IsZero() || !strings.Contains(task.Detail, "push rejected") {
				t.Fatalf("failure not charged to the pull request: %+v", task)
			}
			if hasEvent(st, x.ID, "error", string(Issue)) {
				t.Fatal("a pull request failure raised a house error")
			}
		}},
		{name: "review_failure_keeps_poll_cadence", role: Review, result: RunResult{PR: 1}, err: errors.New("agent timed out"), status: "blocked", check: func(t *testing.T, st State, x *Town, w *Worker, task *Task) {
			if w.Status != "waiting" || w.Error != "" || !w.Next.Equal(now.Add(time.Duration(x.Config.PollSeconds)*time.Second)) || w.Task != task.Detail {
				t.Fatalf("review failure held the house: %+v", w)
			}
			if task.Attempts != 1 {
				t.Fatalf("failure not charged to the pull request: %+v", task)
			}
		}},
		{name: "canceled", role: Issue, result: RunResult{PR: 1}, err: context.Canceled, status: "abandoned", check: func(t *testing.T, st State, x *Town, w *Worker, task *Task) {
			if w.Status != "waiting" || w.Error != "" || task.Attempts != 0 {
				t.Fatalf("a cancellation counted as a failure: %+v %+v", w, task)
			}
			if !hasOutcome(x, "abandoned", "abandoned") {
				t.Fatal("cancellation recorded no abandoned outcome")
			}
		}},
		{name: "ownership_merged", role: Issue, result: RunResult{Owned: map[int]Ownership{9: {Branch: "issue-9", Issue: 9}}}, status: "attempted", check: func(t *testing.T, _ State, x *Town, w *Worker, _ *Task) {
			if x.Owned[9] != (Ownership{Branch: "issue-9", Issue: 9}) || x.Owned[1].Branch != "issue-1" {
				t.Fatalf("ownership not merged: %+v", x.Owned)
			}
			if w.Status != "waiting" {
				t.Fatalf("worker = %+v", w)
			}
		}},
		{name: "stale_audit_ignored", role: Review, result: RunResult{PR: 1, Audit: stale}, status: "attempted", check: func(t *testing.T, _ State, _ *Town, _ *Worker, task *Task) {
			if task.Audit != nil || task.Stage != "queued" || task.Attempts != 0 {
				t.Fatalf("an audit of another revision was applied: %+v", task)
			}
		}},
		{name: "inconclusive_audit_is_an_attempt", role: Review, result: RunResult{PR: 1, Audit: inconclusive}, status: "attempted", check: func(t *testing.T, _ State, _ *Town, _ *Worker, task *Task) {
			if task.Audit != nil || task.Attempts != 1 || !strings.Contains(task.Detail, "inconclusive review: tests would not start") {
				t.Fatalf("inconclusive review not charged as an attempt: %+v", task)
			}
		}},
		{name: "clean_audit_applied", role: Review, result: RunResult{PR: 1, Audit: withSeverity, Severities: map[string]string{"old:x": "P1"}}, status: "attempted", check: func(t *testing.T, _ State, _ *Town, _ *Worker, task *Task) {
			if task.Audit == nil || task.Stage != "ready" || task.Attempts != 0 || !task.RetryAt.IsZero() {
				t.Fatalf("clean audit not applied: %+v", task)
			}
			if task.Severities["old:x"] != "P1" || task.Severities["new:nit"] != "P3" || task.Concerns["new:nit"] != "style" {
				t.Fatalf("audit evidence not recorded: %v %v", task.Severities, task.Concerns)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t, false)
			x := setupPR(t, s, 1)
			update(t, s, func(st *State) {
				town := st.Towns[x.ID]
				town.Workers[tc.role].Enabled = true
				town.Workers[Repo].Next = now.Add(time.Hour)
				task := town.Tasks["pr:1"]
				task.Stage, task.Audit = "queued", nil
			})
			sup := NewSupervisor(s, newGH(1), workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
				if tc.panic {
					panic("boom")
				}
				return tc.result, tc.err
			}))
			sup.now = func() time.Time { return now }
			sup.execute(context.Background(), s.Snapshot().Towns[x.ID], tc.role)
			select {
			case err := <-sup.fatal:
				t.Fatalf("the result could not be committed: %v", err)
			default:
			}
			st := s.Snapshot()
			town := st.Towns[x.ID]
			w := town.Workers[tc.role]
			if w.Run != nil || w.Agent != nil {
				t.Fatalf("run state not cleared: %+v", w)
			}
			if !town.Workers[Repo].Next.IsZero() {
				t.Fatal("repo house not woken after a worker run")
			}
			if !hasOutcome(town, "worker_attempt", tc.status) {
				t.Fatalf("no %s attempt outcome in %+v", tc.status, town.Outcomes)
			}
			tc.check(t, st, town, w, town.Tasks["pr:1"])
		})
	}
}

func hasEvent(st State, town, kind, from string) bool {
	for _, e := range st.Events {
		if e.Town == town && e.Kind == kind && e.From == from {
			return true
		}
	}
	return false
}

func hasOutcome(x *Town, kind, status string) bool {
	for _, o := range x.Outcomes {
		if o.Kind == kind && o.Status == status {
			return true
		}
	}
	return false
}

package town

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// snoozeTown holds two queued issues, two queued pull requests, two
// Simplifier intakes and two arrivals awaiting the Mayor, so every selection
// has something else to pick when its first choice is snoozed.
func snoozeTown(t *testing.T, s *Store) *Town {
	t.Helper()
	x := addTown(t, s)
	update(t, s, func(st *State) {
		current := st.Towns[x.ID]
		current.Initialized = true
		for _, n := range []int{1, 2} {
			id := fmt.Sprintf("issue:%d", n)
			current.Tasks[id] = &Task{ID: id, Kind: "issue", Number: n, Title: id, House: Issue, Stage: "queued"}
		}
		for _, n := range []int{10, 11} {
			id := fmt.Sprintf("pr:%d", n)
			current.Tasks[id] = &Task{ID: id, Kind: "pr", Number: n, Title: id, House: Review, Stage: "queued", Head: headSHA, Base: baseSHA}
		}
		for _, n := range []int{20, 21} {
			id := fmt.Sprintf("issue:%d", n)
			current.Tasks[id] = &Task{ID: id, Kind: "issue", Number: n, Title: id, House: Simplifier, Stage: "simplifying"}
		}
		for _, n := range []int{30, 31} {
			id := fmt.Sprintf("issue:%d", n)
			pendingAt(current, &Task{ID: id, Kind: "issue", Number: n, Title: id})
		}
	})
	return s.Snapshot().Towns[x.ID]
}

func TestSnoozedTaskIsSkippedWhileTheHouseWorksTheRest(t *testing.T) {
	s := testStore(t, false)
	x := snoozeTown(t, s)
	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	sup := NewSupervisor(s, nil, nil)
	sup.now = func() time.Time { return now }
	until := now.Add(2 * time.Hour)
	for _, id := range []string{"issue:1", "pr:10", "issue:20", "issue:30"} {
		if err := sup.Defer(x.ID, id, until, "waiting on the vendor"); err != nil {
			t.Fatalf("snooze %s: %v", id, err)
		}
	}
	x = s.Snapshot().Towns[x.ID]
	check := func(at time.Time, issue, pr, intake, arrival string) {
		t.Helper()
		if got := nextIssue(x, at); got == nil || got.ID != issue {
			t.Fatalf("issue house at %s picked %v, want %s", at, got, issue)
		}
		if got := nextTask(x, Review, "queued", at); got == nil || got.ID != pr {
			t.Fatalf("review house at %s picked %v, want %s", at, got, pr)
		}
		if got := nextTask(x, Simplifier, "simplifying", at); got == nil || got.ID != intake {
			t.Fatalf("simplifier at %s picked %v, want %s", at, got, intake)
		}
		if got := nextJudgment(x, at); got == nil || got.ID != arrival {
			t.Fatalf("mayor at %s picked %v, want %s", at, got, arrival)
		}
	}
	check(now, "issue:2", "pr:11", "issue:21", "issue:31")
	check(until.Add(-time.Second), "issue:2", "pr:11", "issue:21", "issue:31")
	// At the resume time the snooze no longer holds, even before the
	// scheduler's sweep records that it ended.
	check(until, "issue:1", "pr:10", "issue:20", "issue:30")

	// A queue holding only snoozed work leaves the house idle.
	update(t, s, func(st *State) {
		delete(st.Towns[x.ID].Tasks, "issue:2")
	})
	x = s.Snapshot().Towns[x.ID]
	if got := nextIssue(x, now); got != nil {
		t.Fatalf("a snoozed issue was dispatched: %v", got.ID)
	}
}

func TestSnoozedReadyPullRequestIsNotMerged(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	gh := newGH(1)
	sup := NewSupervisor(s, gh, observing{gh: gh})
	if err := sup.Defer(x.ID, "pr:1", time.Now().Add(time.Hour), "release freeze"); err != nil {
		t.Fatal(err)
	}
	handled, err := sup.mergeReady(context.Background(), s.Snapshot().Towns[x.ID], slog.Default())
	if err != nil || handled || gh.merged != 0 {
		t.Fatalf("a snoozed pull request was merged: handled=%t merged=%d err=%v", handled, gh.merged, err)
	}
	if intent := s.Snapshot().Towns[x.ID].Intents[1]; intent != nil {
		t.Fatalf("a snoozed pull request recorded a merge intent: %+v", intent)
	}
	if err := sup.Control(x.ID, Review, "undefer", "pr:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sup.mergeReady(context.Background(), s.Snapshot().Towns[x.ID], slog.Default()); err != nil {
		t.Fatal(err)
	}
	if gh.merged != 1 {
		t.Fatalf("clearing the snooze did not make the pull request eligible: merged=%d", gh.merged)
	}
}

func TestSnoozeEndsOnTheSchedulerClockAndWakesTheHouse(t *testing.T) {
	s := testStore(t, false)
	x := snoozeTown(t, s)
	clock := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	sup := NewSupervisor(s, nil, nil)
	sup.now = func() time.Time { return clock }
	until := clock.Add(30 * time.Minute)
	if err := sup.Defer(x.ID, "issue:1", until, "after standup"); err != nil {
		t.Fatal(err)
	}
	// Keep every house from dispatching: this test watches the sweep alone.
	update(t, s, func(st *State) {
		for _, w := range st.Towns[x.ID].Workers {
			w.Enabled = false
			w.Next = clock.Add(time.Hour)
		}
	})
	sup.schedule(context.Background())
	if task := s.Snapshot().Towns[x.ID].Tasks["issue:1"]; !task.DeferredUntil.Equal(until) || task.DeferReason != "after standup" {
		t.Fatalf("the snooze ended early: %+v", task)
	}
	clock = until
	sup.schedule(context.Background())
	current := s.Snapshot().Towns[x.ID]
	task := current.Tasks["issue:1"]
	if !task.DeferredUntil.IsZero() || task.DeferReason != "" {
		t.Fatalf("the snooze outlived its resume time: %+v", task)
	}
	if !current.Workers[Issue].Next.IsZero() {
		t.Fatalf("the issue house was not woken when the snooze ended: %s", current.Workers[Issue].Next)
	}
	if !current.Workers[Review].Next.After(clock) {
		t.Fatal("an unrelated house was woken")
	}
	found := false
	for _, e := range s.Snapshot().Events {
		if e.Cargo == "issue:1" && strings.HasPrefix(e.Title, "Snooze ended") {
			found = true
		}
	}
	if !found {
		t.Fatal("the end of the snooze was not recorded")
	}
}

func TestSnoozeSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	x := snoozeTown(t, s)
	until := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	if err := NewSupervisor(s, nil, nil).Defer(x.ID, "pr:10", until, "needs the design review"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := s.Snapshot().Towns[x.ID].Tasks["pr:10"]
	if !task.DeferredUntil.Equal(until) || task.DeferReason != "needs the design review" {
		t.Fatalf("restart lost the snooze: %+v", task)
	}
	if got := nextTask(s.Snapshot().Towns[x.ID], Review, "queued", time.Now()); got == nil || got.ID != "pr:11" {
		t.Fatalf("a restarted town dispatched the snoozed pull request: %v", got)
	}
}

func TestReconcileKeepsTheOperatorSnooze(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	until := time.Now().Add(time.Hour).UTC()
	if err := NewSupervisor(s, nil, nil).Defer(x.ID, "pr:1", until, "contributor is away"); err != nil {
		t.Fatal(err)
	}
	// A new revision resets every system-owned hold on the task; the
	// operator's snooze is not one of them.
	changed := pull(1)
	changed.Head.SHA = fixSHA
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], inventory(changed), time.Now())
	})
	task := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
	if task.Head != fixSHA || task.Stage != "queued" {
		t.Fatalf("reconcile did not apply the new revision: %+v", task)
	}
	if !task.DeferredUntil.Equal(until) || task.DeferReason != "contributor is away" {
		t.Fatalf("reconcile dropped the snooze: %+v", task)
	}
}

func TestSnoozeRejectsInvalidRequests(t *testing.T) {
	s := testStore(t, false)
	x := snoozeTown(t, s)
	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	update(t, s, func(st *State) {
		current := st.Towns[x.ID]
		current.Tasks["pr:40"] = &Task{ID: "pr:40", Kind: "pr", Number: 40, Title: "merged", House: Release, Stage: "merged"}
		current.Tasks["commit:"+fixSHA] = &Task{ID: "commit:" + fixSHA, Kind: "commit", Title: "c", House: Release, Stage: "unreleased", Head: fixSHA}
	})
	sup := NewSupervisor(s, nil, nil)
	sup.now = func() time.Time { return now }
	for name, tc := range map[string]struct {
		task   string
		until  time.Time
		reason string
		want   string
	}{
		"past":        {"issue:1", now.Add(-time.Minute), "", "not in the future"},
		"now":         {"issue:1", now, "", "not in the future"},
		"too far":     {"issue:1", now.Add(MaxDefer + time.Hour), "", "within 366 days"},
		"long reason": {"issue:1", now.Add(time.Hour), strings.Repeat("x", MaxDeferReason+1), "at most 200"},
		"multiline":   {"issue:1", now.Add(time.Hour), "one\ntwo", "one line"},
		"finished":    {"pr:40", now.Add(time.Hour), "", "no pending work"},
		"commit":      {"commit:" + fixSHA, now.Add(time.Hour), "", "only an issue or pull request"},
		"unknown":     {"issue:99", now.Add(time.Hour), "", "unknown task"},
		"clear unset": {"issue:2", time.Time{}, "", "not snoozed"},
		"no task":     {"", now.Add(time.Hour), "", "unknown task"},
	} {
		err := sup.Defer(x.ID, tc.task, tc.until, tc.reason)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
	if err := sup.Defer("acme/missing", "issue:1", now.Add(time.Hour), ""); err == nil || err.Error() != "unknown town" {
		t.Fatalf("unknown town: %v", err)
	}
	if err := sup.Control(x.ID, Issue, "defer", "issue:1"); err == nil || !strings.Contains(err.Error(), "resume time") {
		t.Fatalf("a snooze without a resume time was accepted: %v", err)
	}
	for _, task := range s.Snapshot().Towns[x.ID].Tasks {
		if !task.DeferredUntil.IsZero() {
			t.Fatalf("a rejected request left a snooze on %s", task.ID)
		}
	}
	// A saved reason without a resume time is not a valid state.
	if err := s.Update(func(st *State) error {
		st.Towns[x.ID].Tasks["issue:1"].DeferReason = "orphan"
		return nil
	}); err == nil {
		t.Fatal("a reason without a resume time was saved")
	}
}

func TestClearingASnoozeMakesTheTaskEligibleImmediately(t *testing.T) {
	s := testStore(t, false)
	x := snoozeTown(t, s)
	now := time.Now()
	sup := NewSupervisor(s, nil, nil)
	sup.now = func() time.Time { return now }
	if err := sup.Defer(x.ID, "issue:1", now.Add(24*time.Hour), "  tomorrow  "); err != nil {
		t.Fatal(err)
	}
	task := s.Snapshot().Towns[x.ID].Tasks["issue:1"]
	if task.DeferReason != "tomorrow" {
		t.Fatalf("reason was not trimmed: %q", task.DeferReason)
	}
	update(t, s, func(st *State) { st.Towns[x.ID].Workers[Issue].Next = now.Add(time.Hour) })
	if err := sup.Control(x.ID, Issue, "undefer", "issue:1"); err != nil {
		t.Fatal(err)
	}
	current := s.Snapshot().Towns[x.ID]
	if got := nextIssue(current, now); got == nil || got.ID != "issue:1" {
		t.Fatalf("a cleared snooze kept the task out of the queue: %v", got)
	}
	if !current.Workers[Issue].Next.IsZero() {
		t.Fatal("clearing the snooze did not wake the house")
	}
	public := s.Snapshot().PublicAt(now)
	entry := public["towns"].(map[string]any)[x.ID].(map[string]any)["tasks"].(map[string]any)["issue:1"].(map[string]any)
	if _, ok := entry["deferred_until"]; ok {
		t.Fatalf("a cleared snooze is still published: %v", entry)
	}
}

func TestDemoSnoozeEndsOnTheDemoClock(t *testing.T) {
	s := testStore(t, true)
	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	update(t, s, func(st *State) {
		c := DefaultConfig("brokkai/orchard")
		x, err := st.Add(c)
		if err != nil {
			t.Fatal(err)
		}
		x.Initialized = true
		x.Head = baseSHA
		seedDemoBoard(st, x, now)
	})
	task := s.Snapshot().Towns["brokkai/orchard"].Tasks["issue:707"]
	if task == nil || !task.Deferred(now) || task.DeferReason == "" {
		t.Fatalf("the demo board has no snoozed task: %+v", task)
	}
	update(t, s, func(st *State) { expireDeferrals(st, now.Add(5*time.Hour)) })
	if task := s.Snapshot().Towns["brokkai/orchard"].Tasks["issue:707"]; !task.Deferred(now.Add(5 * time.Hour)) {
		t.Fatal("the demo snooze ended early")
	}
	update(t, s, func(st *State) { expireDeferrals(st, now.Add(6*time.Hour)) })
	if task := s.Snapshot().Towns["brokkai/orchard"].Tasks["issue:707"]; !task.DeferredUntil.IsZero() {
		t.Fatalf("the demo snooze did not end on the demo clock: %+v", task)
	}
}

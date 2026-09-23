package town

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// An operator can snooze one issue or pull request until a chosen time. The
// house keeps working the rest of its queue; the snoozed task is skipped by
// every selection that would start an agent or write to GitHub for it, and
// becomes eligible again on its own when the time passes.

const (
	// MaxDeferReason bounds the operator's note, in characters, so it stays a
	// short label.
	MaxDeferReason = 200
	// MaxDefer is the furthest ahead a snooze may be set.
	MaxDefer = 366 * 24 * time.Hour
)

// deferTask sets, or with a zero until clears, the operator's snooze on one
// task. Clearing wakes the task's house so the task is eligible at once.
func (s *State) deferTask(t *Town, task *Task, until time.Time, reason string, now time.Time) error {
	if task == nil {
		return errors.New("unknown task")
	}
	if task.Kind != "issue" && task.Kind != "pr" {
		return errors.New("only an issue or pull request can be snoozed")
	}
	if until.IsZero() {
		if task.DeferredUntil.IsZero() {
			return fmt.Errorf("%s is not snoozed", task.ID)
		}
		task.DeferredUntil, task.DeferReason = time.Time{}, ""
		task.Updated = now
		if w := t.Workers[task.House]; w != nil {
			w.Next = time.Time{}
		}
		s.Event(t.ID, "control", "operator", string(task.House), task.ID, "Snooze cleared: "+task.Title, now)
		return nil
	}
	if finished(task) {
		return fmt.Errorf("%s is %s; there is no pending work to snooze", task.ID, task.Stage)
	}
	if !until.After(now) {
		return fmt.Errorf("resume time %s is not in the future", until.UTC().Format(time.RFC3339))
	}
	if until.After(now.Add(MaxDefer)) {
		return fmt.Errorf("resume time must be within %d days", int(MaxDefer/(24*time.Hour)))
	}
	reason = strings.TrimSpace(reason)
	if err := validDeferReason(reason); err != nil {
		return err
	}
	task.DeferredUntil, task.DeferReason = until.UTC(), reason
	task.Updated = now
	title := "Snoozed until " + until.UTC().Format(time.RFC3339) + ": " + task.Title
	s.Event(t.ID, "control", "operator", string(task.House), task.ID, title, now)
	return nil
}

func validDeferReason(reason string) error {
	if utf8.RuneCountInString(reason) > MaxDeferReason || strings.ContainsFunc(reason, unicode.IsControl) {
		return fmt.Errorf("snooze reason must be one line of at most %d characters", MaxDeferReason)
	}
	return nil
}

// finished reports a task with no pending work left to snooze.
func finished(task *Task) bool {
	switch task.Stage {
	case "merged", "closed", "closing", "declined", "implemented":
		return true
	}
	return false
}

// expiredDeferrals reports whether any live town holds a snooze that ended.
func expiredDeferrals(st State, now time.Time) bool {
	for _, t := range st.Towns {
		if t.Deleted {
			continue
		}
		for _, task := range t.Tasks {
			if !task.DeferredUntil.IsZero() && !task.Deferred(now) {
				return true
			}
		}
	}
	return false
}

// expireDeferrals clears every snooze that ended by now and wakes the house
// holding the task, so it resumes without an operator. Selection already
// ignores an ended snooze; clearing it records the resumption and tells
// clients that watch the snapshot.
func expireDeferrals(st *State, now time.Time) {
	for _, t := range st.Towns {
		if t.Deleted {
			continue
		}
		for _, task := range t.Tasks {
			if task.DeferredUntil.IsZero() || task.Deferred(now) {
				continue
			}
			task.DeferredUntil, task.DeferReason = time.Time{}, ""
			if finished(task) {
				// Work that finished while snoozed has nothing to resume, so
				// its snooze is dropped without waking a house or announcing it.
				continue
			}
			task.Updated = now
			if w := t.Workers[task.House]; w != nil {
				w.Next = time.Time{}
			}
			st.Event(t.ID, "control", "hall", string(task.House), task.ID, "Snooze ended: "+task.Title, now)
		}
	}
}

// Defer snoozes one task until the given time with a short reason; a zero
// until clears the snooze. It commits through the same store update as every
// other operator control and wakes the scheduler.
func (s *Supervisor) Defer(id, taskID string, until time.Time, reason string) error {
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		return st.deferTask(t, t.Tasks[taskID], until, reason, s.now())
	})
	if err != nil {
		return err
	}
	s.notifyScheduler()
	return nil
}

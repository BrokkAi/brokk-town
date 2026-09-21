package town

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Auto-Mayor judges everything that reaches Town Hall so a town can run without
// a person clearing each arrival. It never invents a verdict: it follows the
// advice the town already produced, and asks Simplifier Bot for advice when an
// outside arrival has none.
//
//   - A bot update is approved.
//   - Work with a Simplifier assessment gets that assessment's decision.
//   - An external pull request Town reviewed and could not clear, or whose
//     review attempts were exhausted, is declined: Town stops acting on it and
//     the findings already posted on GitHub tell the contributor why.
//   - Work the town proposed to itself without advice is admitted.
//   - An outside arrival without advice goes to Simplifier Bot first; its
//     suggestion returns here and is then applied.
func autoMayorAction(task *Task) string {
	if task.Kind == "upgrade" {
		return "admit"
	}
	if task.Simplification != nil {
		switch task.Simplification.Decision {
		case "admit", "decline":
			return task.Simplification.Decision
		}
	}
	if task.Retired || (task.Audit != nil && task.Audit.Verdict == "changes_needed") {
		return "decline"
	}
	if !task.External {
		return "admit"
	}
	return "simplify"
}

// pendingDecisions reports whether anything waits on the Mayor.
func pendingDecisions(t *Town) bool {
	for _, task := range t.Tasks {
		if task.MayoralDecision == "pending" && task.Stage == "awaiting_mayor" && task.House == Hall {
			return true
		}
	}
	return false
}

// autoMayor resolves every pending Mayoral decision in the town. Tasks are
// visited in ID order so repeated runs produce the same events.
func (s *State) autoMayor(t *Town, now time.Time) {
	ids := make([]string, 0, len(t.Tasks))
	for id := range t.Tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		task := t.Tasks[id]
		if task.MayoralDecision != "pending" || task.Stage != "awaiting_mayor" || task.House != Hall {
			continue
		}
		action := autoMayorAction(task)
		if action == "simplify" {
			task.MayoralDecision = ""
			task.Detail = "Auto-Mayor asked Simplifier Bot for advice on this outside arrival before deciding."
			s.Move(t, task, "simplifying", Simplifier, "Auto-Mayor asked Simplifier Bot about: "+task.Title, now)
			if w := t.Workers[Simplifier]; w != nil {
				w.Next = time.Time{}
			}
			continue
		}
		// The task was just checked as pending, so the shared decision path
		// cannot refuse it.
		_ = s.decideTask(t, task, action, "Auto-Mayor", now)
	}
}

// decideTask applies one Mayoral decision. It is the single path for the
// Mayor's own clicks and for Auto-Mayor, so both leave the same state and the
// same events; only the name in those events differs.
func (s *State) decideTask(t *Town, task *Task, action, by string, now time.Time) error {
	if task == nil || task.MayoralDecision != "pending" || task.Stage != "awaiting_mayor" || task.House != Hall {
		return errors.New("task is not awaiting a Mayoral decision")
	}
	if task.Kind == "upgrade" || action == "delay" {
		return s.decideBotUpgrade(t, task, action, by, now)
	}
	subject := subject(by)
	switch action {
	case "decline":
		task.MayoralDecision = "declined"
		task.Stage = "declined"
		task.Updated = now
		// Work the town proposed to itself is retired at its source: a
		// declined proposal is closed rather than left open for a bot to
		// pick up again. Outside work is only ignored; Town does not
		// close other people's issues and pull requests.
		if task.Kind == "issue" && !task.External {
			task.Detail = subject + " declined this proposal. Town is closing the issue."
			t.Workers[Repo].Next = time.Time{}
			s.Event(t.ID, "decision", "hall", string(Repo), task.ID, by+" declined: "+task.Title, now)
			return nil
		}
		task.Detail = subject + " declined this outside work. Town will not act on it."
		s.Event(t.ID, "decision", "hall", "outside", task.ID, by+" declined: "+task.Title, now)
		return nil
	case "admit":
		task.MayoralDecision = "admitted"
		task.Stage = "queued"
		task.Retired = false
		task.Updated = now
		task.Detail = subject + " admitted this work to town."
		target := Review
		if task.Kind == "issue" {
			target = Issue
		}
		task.House = target
		t.Workers[target].Next = time.Time{}
		s.Event(t.ID, "decision", "hall", string(target), task.ID, by+" admitted: "+task.Title, now)
		return nil
	}
	return fmt.Errorf("unknown decision %q", action)
}

// SetAutoMayor turns Auto-Mayor on or off for one town. Turning it on judges
// everything already waiting at Town Hall.
func (s *Supervisor) SetAutoMayor(id string, on bool) error {
	return s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		t.Config.AutoMayor = on
		if on {
			st.autoMayor(t, s.now())
		}
		return nil
	})
}

package town

import (
	"fmt"
	"time"
)

// Older Repo Bots rejected the empty branch that asks them to discover the
// repository default. A restart must retire that known validation failure even
// if the next inventory is canceled or the house is paused. This is not evidence
// of successful inventory or of an interrupted repair's outcome.
func recoverDefaultBranchFailure(s *State, t *Town, now time.Time) bool {
	if s.Demo || t.Deleted || t.Initialized || t.Config.Branch != "" || t.DefaultBranch != "" {
		return false
	}
	w := t.Workers[Repo]
	const obsolete = "invalid branch"
	if t.Error != obsolete && w.Error != obsolete {
		return false
	}
	if t.Error == obsolete {
		t.Error = ""
	}
	if w.Error == obsolete {
		w.Error = ""
	}
	if w.Error == "" && w.Recovery == nil {
		w.Phase = ""
		w.Task = "Waiting for repository inventory after startup recovery"
	}
	if w.Enabled {
		w.Next = time.Time{}
	}
	s.Event(t.ID, "report", string(Repo), "hall", "", "Retired obsolete invalid branch startup error; repository inventory is still pending", now)
	return true
}

func recoveryDetail(role Role, taskID string) string {
	if role == Repo {
		return "An interrupted repository run may have published a branch repair. Repository inventory continues, but further repairs are held. Check the branch and saved bot result, then start Repo Bot to authorize repairs."
	}
	if taskID != "" {
		return fmt.Sprintf("Interrupted %s work on %s has an uncertain outcome. Check GitHub and the saved bot result; then use retry --task %s if unfinished. Automatic replacement work is held.", role, taskID, taskID)
	}
	return fmt.Sprintf("Interrupted %s scan has an uncertain outcome. Check recent GitHub issues for work that landed; start the %s house to authorize another scan. Automatic replacement work is held.", role, role)
}

// An inventory request carries no agent and cannot publish a repair. Losing
// that read must not hold future reads. Older recovery records did not retain
// the mode, so keep their uncertain outcome while allowing inventory only.
func recoverWorkerRun(w *Worker) {
	if run := w.Run; run != nil {
		if w.Recovery == nil && !(w.Role == Repo && run.Mode == "inventory") {
			target := workerRunTask(*run)
			w.Recovery = &WorkerRecovery{Execution: clone(run.Execution), TaskID: target, Base: run.BaseSHA, Head: run.HeadSHA, Started: run.Started, Detail: recoveryDetail(w.Role, target)}
		}
		if w.Role == Repo {
			w.Next = time.Time{}
		}
		w.Run = nil
	}
	if w.Role == Repo && w.Recovery != nil {
		w.Recovery.Detail = recoveryDetail(Repo, "")
	}
}

// resolveIssueRecovery requires affirmative evidence from the bot's durable
// publication record. An empty inventory or a missing process proves nothing.
func resolveIssueRecovery(t *Town) {
	w := t.Workers[Issue]
	if w == nil || w.Recovery == nil || w.Run != nil {
		return
	}
	task := t.Tasks[w.Recovery.TaskID]
	if task == nil || task.IssueJob == nil || (task.IssueJob.Status != "submitted" && task.IssueJob.Status != "has_pr") {
		return
	}
	w.Recovery = nil
	w.Error = ""
	w.Next = time.Time{}
	w.Task = "Recovered interrupted issue work: its implementation PR is recorded"
	w.Status = "waiting"
	if !w.Enabled {
		w.Status = "paused"
	}
}

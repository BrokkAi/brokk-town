package town

import (
	"fmt"
	"time"
)

func recoveryDetail(role Role, taskID string) string {
	if taskID != "" {
		return fmt.Sprintf("Interrupted %s work on %s has an uncertain outcome. Check GitHub and the saved bot result; then use retry --task %s if unfinished. Automatic replacement work is held.", role, taskID, taskID)
	}
	return fmt.Sprintf("Interrupted %s scan has an uncertain outcome. Check recent GitHub issues for work that landed; start the %s house to authorize another scan. Automatic replacement work is held.", role, role)
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

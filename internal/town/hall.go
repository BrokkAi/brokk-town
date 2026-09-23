package town

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Town Hall is Mayor Bot's house. When the house is started, the bot judges
// every arrival that waits for a Mayoral decision and writes the town bulletin:
// the feed of features gained and bugs fixed, written for the people who use
// the software. Both are one-shot worker runs like every other house's work,
// and every judgment is applied through the same path as the Mayor's clicks.

// Bulletin is one entry in the town's work-completed feed.
type Bulletin struct {
	At      time.Time      `json:"at"`
	Since   time.Time      `json:"since"`
	Until   time.Time      `json:"until"`
	Title   string         `json:"title"`
	Summary string         `json:"summary"`
	Items   []BulletinItem `json:"items"`
	Pulls   []int          `json:"pulls"`
}

// BulletinItem is one user-facing change: a feature, a fix, an improvement or
// something else users would notice, citing the pull requests it came from.
type BulletinItem struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Pulls  []int  `json:"pulls"`
	Issues []int  `json:"issues,omitempty"`
}

// Judgment is Mayor Bot's decision on one arrival.
type Judgment struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

const (
	// mayorAttempts bounds how often a judgment is retried for one arrival
	// before it is left for a person; mayorRetryDelay separates the attempts.
	mayorAttempts   = 3
	mayorRetryDelay = 15 * time.Minute
	// DefaultBulletinSeconds is how often the bulletin is written when
	// something merged since the last one.
	DefaultBulletinSeconds = 6 * 60 * 60
	maxBulletins           = 200
)

func ValidBulletinKind(kind string) bool {
	switch kind {
	case "feature", "fix", "improvement", "other":
		return true
	}
	return false
}

func validBulletin(b Bulletin) bool {
	if b.At.IsZero() || !b.Until.After(b.Since) || strings.TrimSpace(b.Title) == "" || len(b.Title) > 256 || len(b.Summary) > 4096 || b.Items == nil || len(b.Items) > 200 || b.Pulls == nil {
		return false
	}
	for _, item := range b.Items {
		if !ValidBulletinKind(item.Kind) || strings.TrimSpace(item.Title) == "" || len(item.Title) > 256 || len(item.Detail) > 2048 || len(item.Pulls) == 0 {
			return false
		}
	}
	return true
}

// pendingDecisions reports whether anything waits on the Mayor.
func pendingDecisions(t *Town) bool {
	for _, task := range t.Tasks {
		if task.MayoralDecision == "pending" && task.Stage == "awaiting_mayor" && task.House == Hall && !task.Blocked {
			return true
		}
	}
	return false
}

// nextJudgment picks the arrival Mayor Bot judges next: the pending decision
// with the lowest ID that is not blocked (a pull request retargeted off this
// town's branch), not waiting out a failed attempt and has not exhausted its
// attempts. An arrival the operator snoozed waits for a person or for its
// resume time.
func nextJudgment(t *Town, now time.Time) *Task {
	ids := make([]string, 0, len(t.Tasks))
	for id, task := range t.Tasks {
		if task.MayoralDecision == "pending" && task.Stage == "awaiting_mayor" && task.House == Hall && !task.Blocked && task.Attempts < mayorAttempts && !task.RetryAt.After(now) && !task.Deferred(now) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	return t.Tasks[ids[0]]
}

// arrivalContext is the town's description of one arrival for Mayor Bot: what
// the item is and every piece of advice the town already attached. The bot
// adds the live GitHub source itself.
func arrivalContext(task *Task) json.RawMessage {
	arrival := map[string]any{
		"kind": task.Kind, "number": task.Number, "title": task.Title, "url": task.URL,
		"external": task.External, "town_detail": task.Detail, "retired_after_failed_reviews": task.Retired,
	}
	if task.Simplification != nil {
		arrival["simplifier_advice"] = task.Simplification
	}
	if task.Audit != nil {
		arrival["town_review"] = map[string]any{"verdict": task.Audit.Verdict, "summary": task.Audit.Summary, "findings": task.Audit.Findings}
	}
	raw, _ := json.Marshal(arrival)
	return raw
}

// bulletinWindow reports the window the next bulletin should cover, or false
// when none is due: the interval since the last bulletin has not passed, or
// nothing merged since it. The first bulletin covers the last interval only.
func bulletinWindow(t *Town, now time.Time) (since, until time.Time, due bool) {
	interval := time.Duration(t.Config.BulletinSecondsOrDefault()) * time.Second
	if len(t.Bulletins) > 0 {
		last := t.Bulletins[len(t.Bulletins)-1]
		since = last.Until
		if now.Sub(last.At) < interval {
			return since, now, false
		}
	} else {
		since = now.Add(-interval)
	}
	for _, record := range t.Outcomes {
		if record.Kind == "merge" && record.Status == "confirmed" && record.At.After(since) && !record.At.After(now) {
			return since, now, true
		}
	}
	return since, now, false
}

// applyJudgment records one judgment run's outcome. A verdict goes through the
// Mayor's decision path with the bot's reason kept on the task; a failed run
// backs the arrival off and, after mayorAttempts failures, leaves it for a
// person with the last failure on it.
func applyJudgment(st *State, t *Town, taskID string, verdict *Judgment, runErr error, now time.Time) {
	task := t.Tasks[taskID]
	if task == nil || task.MayoralDecision != "pending" || task.Stage != "awaiting_mayor" || task.House != Hall {
		return
	}
	if runErr == nil && verdict == nil {
		runErr = errors.New("Mayor Bot returned no decision")
	}
	if runErr != nil {
		task.Attempts++
		task.RetryAt = now.Add(mayorRetryDelay)
		task.Detail = fmt.Sprintf("Mayor Bot could not judge this arrival (attempt %d of %d): %s", task.Attempts, mayorAttempts, runErr.Error())
		if task.Attempts >= mayorAttempts {
			task.Detail += " It is left for the Mayor."
		}
		task.Updated = now
		st.Event(t.ID, "error", "hall", "hall", taskID, "Mayor Bot could not judge: "+task.Title, now)
		return
	}
	if err := st.decideTask(t, task, verdict.Decision, "Mayor Bot", now); err != nil {
		return
	}
	task.Detail += " " + strings.TrimSpace(verdict.Reason)
}

// applyBulletin appends one bulletin to the feed. Windows are contiguous, so a
// bulletin that does not start where the last one ended is refused rather than
// leaving a gap or a double count in the feed.
func applyBulletin(st *State, t *Town, b Bulletin, now time.Time) error {
	b.At = now
	if !validBulletin(b) {
		return errors.New("Mayor Bot returned an invalid bulletin")
	}
	if len(t.Bulletins) > 0 && !t.Bulletins[len(t.Bulletins)-1].Until.Equal(b.Since) {
		return errors.New("bulletin window does not continue the feed")
	}
	t.Bulletins = append(t.Bulletins, b)
	if len(t.Bulletins) > maxBulletins {
		t.Bulletins = t.Bulletins[len(t.Bulletins)-maxBulletins:]
	}
	if len(b.Items) > 0 {
		st.Event(t.ID, "bulletin", "hall", "outside", "", "Town bulletin: "+b.Title, now)
	}
	return nil
}

// decideTask applies one Mayoral decision. It is the single path for the
// Mayor's own clicks and for Mayor Bot, so both leave the same state and the
// same events; only the name in those events differs.
//
// The Mayor may also admit work Simplifier declined in auto mode. That decline
// is otherwise final, so this is the only way back for it; Mayor Bot never
// reaches it because it judges only pending decisions.
func (s *State) decideTask(t *Town, task *Task, action, by string, now time.Time) error {
	overrule := action == "admit" && task != nil && task.House == Hall && task.Stage == "declined" && task.MayoralDecision == "" && autoDeclined(task)
	if !overrule && (task == nil || task.MayoralDecision != "pending" || task.Stage != "awaiting_mayor" || task.House != Hall) {
		return errors.New("task is not awaiting a Mayoral decision")
	}
	task.Attempts = 0
	task.RetryAt = time.Time{}
	subject := by
	if subject == "you" {
		subject = "You"
	}
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
		title := by + " admitted: " + task.Title
		if overrule {
			// The decline no longer governs the task: resume and the
			// declined-issue closer both read it.
			task.Simplification = nil
			task.Detail = subject + " admitted this work to town over Simplifier's decline."
			title = by + " admitted over Simplifier's decline: " + task.Title
		}
		target := Review
		if task.Kind == "issue" {
			target = Issue
		}
		task.House = target
		t.Workers[target].Next = time.Time{}
		s.Event(t.ID, "decision", "hall", string(target), task.ID, title, now)
		return nil
	}
	return fmt.Errorf("unknown decision %q", action)
}

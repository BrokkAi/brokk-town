package town

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Every pull request Town works on ends merged or closed, and a revision gets
// at most two attempts at any step. Nothing waits on a person to press retry.
//
// The first review sends every finding back to issue-bot for one fix round.
// The second review either finds only follow-up work below the town's close
// severity, in which case the pull request merges and the rest becomes issues,
// or it still finds blocking work, in which case Town closes the pull request
// and queues the issue for a fresh attempt from the current base branch.

// pullRetryDelay separates the two attempts a pull request revision gets.
const pullRetryDelay = 15 * time.Minute

// failPullAttempt records an attempt on a pull request that produced no
// decision. The first failure on a revision earns one more attempt after a
// short delay; the second retires the pull request.
func (s *Supervisor) failPullAttempt(st *State, t *Town, task *Task, role Role, detail string) {
	task.Attempts++
	task.Blocked = false
	if task.Attempts < 2 {
		task.RetryAt = s.now().Add(pullRetryDelay)
		task.Detail = fmt.Sprintf("%s attempt did not complete; one more attempt is scheduled. %s", houseName(role), detail)
		return
	}
	task.RetryAt = time.Time{}
	retirePull(st, t, task, fmt.Sprintf("Two %s attempts on this revision did not complete: %s", houseName(role), detail), s.now())
}

// retirePull ends Town's work on a revision that cannot proceed. Town closes
// its own pull requests and starts the issue over. A contributor's pull request
// goes to the Mayor instead, because Town does not close other people's work.
func retirePull(st *State, t *Town, task *Task, reason string, now time.Time) {
	if task.External {
		task.Stage = "awaiting_mayor"
		task.House = Hall
		task.MayoralDecision = "pending"
		task.Retired = true
		task.Attempts = 0
		task.RetryAt = time.Time{}
		task.Updated = now
		task.Detail = reason + " Decide whether Town should review this external PR again or decline it."
		st.Event(t.ID, "decision", string(Review), "hall", task.ID, "External PR needs a Mayoral decision: "+task.Title, now)
		return
	}
	markClosing(st, t, task, reason, now)
}

// markClosing hands a pull request to the next repository inventory, which
// closes it on GitHub and queues its issue for a fresh attempt.
func markClosing(st *State, t *Town, task *Task, reason string, now time.Time) {
	if task == nil || task.Stage == "closing" || task.Stage == "closed" || task.Stage == "merged" {
		return
	}
	task.Attempts = 0
	task.RetryAt = time.Time{}
	task.Blocked = false
	task.MayoralDecision = ""
	task.Detail = reason
	st.Move(t, task, "closing", Hall, "Closing after review: "+task.Title, now)
	if w := t.Workers[Repo]; w != nil {
		w.Next = time.Time{}
	}
}

// declinedOwnPull reports whether Town holds a final decline of its own pull
// request that it carries out by closing it: the Mayor's decline, or
// Simplifier's auto decline that nobody has escalated to the Mayor or admitted.
func declinedOwnPull(task *Task) bool {
	if task == nil || task.Kind != "pr" || task.External || task.House != Hall || task.Stage != "declined" {
		return false
	}
	return task.MayoralDecision == "declined" || (task.MayoralDecision == "" && autoDeclined(task))
}

// claimDeclinedPulls hands each declined pull request of Town's own to the
// closer that closes pull requests after review, which also starts the issue
// over. It runs under the store right before that closer, and only outside
// quiet hours, so until then the Mayor can still admit an auto-declined pull
// request; once claimed as "closing", admission refuses it. A snoozed pull
// request waits for its snooze. The decline itself is kept, so a reopen is
// judged by it.
func claimDeclinedPulls(st *State, t *Town, now time.Time) {
	if t == nil || t.Deleted {
		return
	}
	for _, task := range t.Tasks {
		if !declinedOwnPull(task) || task.Deferred(now) {
			continue
		}
		task.Attempts = 0
		task.RetryAt = time.Time{}
		task.Blocked = false
		task.Detail = declineCloseReason(task)
		st.Move(t, task, "closing", Hall, "Closing declined PR: "+task.Title, now)
	}
}

func declineCloseReason(task *Task) string {
	if task.MayoralDecision == "declined" {
		return "The Mayor declined this pull request. Town is closing it and starting the issue over."
	}
	reason := "Simplifier declined this pull request. Town is closing it and starting the issue over."
	if s := task.Simplification; s != nil {
		if s.Summary != "" {
			reason += "\n\n" + s.Summary
		}
		reason += "\n\n" + s.Detail
	}
	return reason
}

// closedCause says why Town closed one of its own pull requests.
func closedCause(task *Task) string {
	switch {
	case task.MayoralDecision == "declined":
		return "the Mayor declined it"
	case autoDeclined(task):
		return "Simplifier declined it"
	}
	return "its second review"
}

// severity is the rating Town holds for one certified finding: the certifier's
// own when it gave one, otherwise the reviewer's.
func (task *Task) severity(f Finding) string {
	if ValidSeverity(f.Severity) {
		return f.Severity
	}
	return task.Severities[f.ID]
}

// settleReview routes a pull request after a complete, certified audit of its
// current revision.
func (s *Supervisor) settleReview(st *State, t *Town, task *Task) {
	audit := task.Audit
	open := audit.OpenFindings()
	now := s.now()
	switch {
	case len(open) == 0:
		st.Move(t, task, "ready", Review, "Review complete: ready for merge checks", now)
	case task.External:
		task.Stage = "awaiting_mayor"
		task.House = Hall
		task.MayoralDecision = "pending"
		task.Updated = now
		task.Detail = "Review found changes are needed in this external PR. Decide whether Town should review it again or decline it."
		st.Event(t.ID, "decision", "review", "hall", task.ID, "External PR needs a Mayoral decision: "+task.Title, now)
	case task.Cycles == 0:
		st.Move(t, task, "fixes", Issue, "Review feedback delivered to issue-bot", now)
	default:
		threshold := t.Config.ReviewCloseSeverityOrDefault()
		var blocking, deferred []Finding
		for _, f := range open {
			if Blocking(task.severity(f), threshold) {
				blocking = append(blocking, f)
			} else {
				deferred = append(deferred, f)
			}
		}
		if len(blocking) > 0 {
			markClosing(st, t, task, closeReason(task, blocking, threshold), now)
			return
		}
		for i := range audit.Findings {
			if audit.Findings[i].State == "open" || audit.Findings[i].State == "uncertain" {
				audit.Findings[i].State = "deferred"
			}
		}
		audit.Verdict = "clean"
		task.FollowUps = deferred
		task.Detail = fmt.Sprintf("%s %d finding(s) below %s deferred to follow-up issues.", audit.Summary, len(deferred), threshold)
		st.Move(t, task, "ready", Review, "Second review found only follow-up work: ready for merge checks", now)
	}
}

func closeReason(task *Task, blocking []Finding, threshold string) string {
	lines := make([]string, 0, len(blocking))
	for _, f := range blocking {
		severity := task.severity(f)
		if severity == "" {
			severity = "unrated"
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s", severity, firstLine(f.Detail)))
	}
	return fmt.Sprintf("The second review still found %d finding(s) at or above %s. Town is closing this pull request and starting the issue over.\n%s", len(blocking), threshold, strings.Join(lines, "\n"))
}

func firstLine(text string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if len(line) > 140 {
		line = line[:140] + "…"
	}
	return line
}

func closingComment(task *Task, owned Ownership) string {
	body := "## Brokk Town\n\n" + task.Detail
	if owned.Issue > 0 {
		body += fmt.Sprintf("\n\nIssue #%d is queued for a fresh attempt from the current base branch.", owned.Issue)
	}
	return body + "\n\n<!-- brokk-town:closed-after-review -->"
}

func requeueComment(task *Task, pr int) string {
	return fmt.Sprintf("## Brokk Town\n\nPull request #%d was closed after %s. The next attempt starts over from the current base branch and should address the reason:\n\n%s\n\n<!-- brokk-town:requeued pr=%d -->", pr, closedCause(task), task.Detail, pr)
}

// closeRetiredPulls performs the GitHub side of a closing decision: close the
// pull request, delete Town's branch, tell the issue why, then queue the issue
// again. Each step is idempotent, so a failed step is retried by the next
// inventory without repeating the ones that succeeded.
func (s *Supervisor) closeRetiredPulls(ctx context.Context, t *Town, remote RepoSnapshot) error {
	if t == nil || t.Deleted {
		return nil
	}
	pulls := map[int]Pull{}
	for _, p := range remote.Pulls {
		pulls[p.Number] = p
	}
	numbers := []int{}
	for _, task := range t.Tasks {
		if task.Kind == "pr" && task.Stage == "closing" {
			numbers = append(numbers, task.Number)
		}
	}
	sort.Ints(numbers)
	var failures error
	for _, n := range numbers {
		task := t.Tasks[fmt.Sprintf("pr:%d", n)]
		if p, known := pulls[n]; known && p.MergedAt != nil {
			continue // The inventory records the merge; there is nothing to close.
		}
		owned := t.Owned[n]
		if p, known := pulls[n]; !known || p.State == "open" {
			if err := s.GitHub.ClosePull(ctx, t.Config.Repo, n); err != nil {
				failures = errors.Join(failures, fmt.Errorf("close PR #%d: %w", n, err))
				var rejected *RejectedError
				if errors.As(err, &rejected) && task.MayoralDecision == "" && autoDeclined(task) {
					// GitHub refused the close, so nothing happened there.
					// Simplifier's decline is released, as for an issue, so the
					// Mayor can admit the pull request before the next claim.
					if err := s.Store.Update(func(st *State) error {
						releaseDeclinedPull(st, st.Towns[t.ID], n, rejected.Status, s.now())
						return nil
					}); err != nil {
						return errors.Join(failures, err)
					}
				}
				continue
			}
			if err := s.GitHub.Comment(ctx, t.Config.Repo, n, closingComment(task, owned)); err != nil {
				failures = errors.Join(failures, fmt.Errorf("explain closing PR #%d: %w", n, err))
				continue
			}
		}
		if owned.Branch != "" {
			if err := s.GitHub.DeleteBranch(ctx, t.Config.Repo, owned.Branch); err != nil {
				failures = errors.Join(failures, fmt.Errorf("delete branch %s of PR #%d: %w", owned.Branch, n, err))
				continue
			}
			if owned.Issue > 0 {
				if err := s.GitHub.Comment(ctx, t.Config.Repo, owned.Issue, requeueComment(task, n)); err != nil {
					failures = errors.Join(failures, fmt.Errorf("explain requeue of issue #%d: %w", owned.Issue, err))
					continue
				}
			}
		}
		if err := s.Store.Update(func(st *State) error {
			finalizeClosedPull(st, st.Towns[t.ID], n, s.now())
			return nil
		}); err != nil {
			return errors.Join(failures, err)
		}
	}
	return failures
}

// releaseDeclinedPull returns an auto-declined pull request GitHub refused to
// close from "closing" to "declined", where the Mayor can admit it.
func releaseDeclinedPull(st *State, t *Town, n int, status int, now time.Time) {
	if t == nil {
		return
	}
	task := t.Tasks[fmt.Sprintf("pr:%d", n)]
	if task == nil || task.Stage != "closing" || task.MayoralDecision != "" || !autoDeclined(task) {
		return
	}
	task.Stage = "declined"
	task.Detail = fmt.Sprintf("GitHub refused to close this pull request (HTTP %d). Simplifier's decline stands; the Mayor can admit it anyway.", status)
	task.Updated = now
	st.Event(t.ID, "error", string(Repo), "hall", task.ID, "GitHub refused to close: "+task.Title, now)
}

// finalizeClosedPull records the closed pull request and queues its issue for a
// fresh attempt that starts over rather than on top of the closed work.
func finalizeClosedPull(st *State, t *Town, n int, now time.Time) {
	if t == nil {
		return
	}
	task := t.Tasks[fmt.Sprintf("pr:%d", n)]
	if task == nil || task.Stage != "closing" {
		return
	}
	owned := t.Owned[n]
	related := ""
	if owned.Issue > 0 {
		related = fmt.Sprintf("issue:%d", owned.Issue)
	}
	task.Stage = "closed"
	task.House = Hall
	if task.MayoralDecision != "declined" {
		// A Mayoral decline stays on the closed pull request, as it does
		// when an author closes one, so a reopen does not undo it.
		task.MayoralDecision = ""
	}
	task.Updated = now
	t.RecordOutcome(OutcomeRecord{ID: fmt.Sprintf("closed:%s:%s", task.ID, task.Head), At: now, Class: "outcome", Kind: "closed", Status: "confirmed", Role: Review, TaskID: task.ID, RelatedTaskID: related, Revision: task.Head, URL: task.URL, Detail: task.Detail})
	st.Event(t.ID, "delivery", "hall", "outside", task.ID, "Closed after "+closedCause(task)+": "+task.Title, now)
	if related == "" {
		return
	}
	issue := t.Tasks[related]
	if issue == nil || issue.Stage == "closed" || issue.Stage == "locked" || issue.MayoralDecision == "declined" {
		return
	}
	issue.Requeue = n
	issue.Attempts = 0
	issue.RetryAt = time.Time{}
	issue.Blocked = false
	issue.IssueJob = nil
	issue.MayoralDecision = ""
	issue.Detail = fmt.Sprintf("PR #%d was closed after %s. A fresh attempt is queued; the reason is on the issue.", n, closedCause(task))
	if issue.Stage == "queued" && issue.House == Issue {
		issue.Updated = now
	} else {
		st.Move(t, issue, "queued", Issue, fmt.Sprintf("Starting over after PR #%d closed: %s", n, issue.Title), now)
	}
	if w := t.Workers[Issue]; w != nil {
		w.Next = time.Time{}
	}
}

// fileFollowUps turns the findings deferred from a merged pull request into
// issues. Each issue is committed before the next is created so a failure
// never files the same finding twice.
func (s *Supervisor) fileFollowUps(ctx context.Context, t *Town) error {
	if t == nil || t.Deleted {
		return nil
	}
	numbers := []int{}
	for _, task := range t.Tasks {
		if task.Kind == "pr" && task.Stage == "merged" && len(task.FollowUps) > 0 {
			numbers = append(numbers, task.Number)
		}
	}
	sort.Ints(numbers)
	var failures error
	for _, n := range numbers {
		task := t.Tasks[fmt.Sprintf("pr:%d", n)]
		for _, f := range task.FollowUps {
			title := followUpTitle(f)
			created, err := s.GitHub.CreateIssue(ctx, t.Config.Repo, title, followUpBody(t, task, f))
			if err != nil {
				failures = errors.Join(failures, fmt.Errorf("file follow-up for PR #%d: %w", n, err))
				break
			}
			if err := s.Store.Update(func(st *State) error {
				current := st.Towns[t.ID]
				if current == nil {
					return nil
				}
				x := current.Tasks[task.ID]
				if x == nil {
					return nil
				}
				kept := x.FollowUps[:0]
				for _, remaining := range x.FollowUps {
					if remaining.ID != f.ID {
						kept = append(kept, remaining)
					}
				}
				x.FollowUps = kept
				if len(x.FollowUps) == 0 {
					x.FollowUps = nil
				}
				current.RecordOutcome(OutcomeRecord{ID: fmt.Sprintf("followup:%s:%s", x.ID, f.ID), At: s.now(), Class: "artifact", Kind: "followup_filed", Status: "submitted", Role: Review, TaskID: fmt.Sprintf("issue:%d", created.Number), RelatedTaskID: x.ID, Revision: x.Head, URL: created.URL, Detail: title})
				st.Event(t.ID, "delivery", string(Review), string(Simplifier), fmt.Sprintf("issue:%d", created.Number), "Follow-up filed: "+title, s.now())
				return nil
			}); err != nil {
				return errors.Join(failures, err)
			}
		}
	}
	return failures
}

func followUpTitle(f Finding) string {
	line := firstLine(f.Detail)
	if strings.HasPrefix(line, "[") {
		if end := strings.Index(line, "] "); end > 0 && end < 6 {
			line = line[end+2:]
		}
	}
	if len(line) > 100 {
		line = line[:100] + "…"
	}
	if line == "" {
		line = "review finding " + f.ID
	}
	return "Follow-up: " + line
}

func followUpBody(t *Town, task *Task, f Finding) string {
	severity := task.severity(f)
	if severity == "" {
		severity = "unrated"
	}
	return fmt.Sprintf("## Review follow-up\n\nDeferred from PR #%d (`%s`) after its second review: severity %s, below the town's close threshold %s, so the pull request merged.\n\n%s\n\n<!-- review-bot:follow-up pr=%d id=%s -->", task.Number, task.Head, severity, t.Config.ReviewCloseSeverityOrDefault(), f.Detail, task.Number, f.ID)
}

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
	task.Closes++
	task.BranchKept = false
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
// request waits for its snooze, and a blocked one (as when it is retargeted off
// this town's branch) waits like other blocked work; Mayor Bot and the
// selectors skip blocked tasks too. The decline itself is kept, so a reopen is
// judged by it.
func claimDeclinedPulls(st *State, t *Town, now time.Time) {
	if t == nil || t.Deleted {
		return
	}
	for _, task := range t.Tasks {
		if !declinedOwnPull(task) || task.Deferred(now) || task.Blocked || task.Offbranch {
			continue
		}
		task.Attempts = 0
		task.RetryAt = time.Time{}
		task.Blocked = false
		task.Detail = declineCloseReason(task)
		task.Closes++
		task.BranchKept = false
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

func closingComment(task *Task, requeued int) string {
	body := "## Brokk Town\n\n" + task.Detail
	if requeued > 0 {
		body += fmt.Sprintf("\n\nTown starts issue #%d over from the current base branch.", requeued)
	}
	return body + "\n\n<!-- brokk-town:closed-after-review -->"
}

// requeueMarker identifies one requeue comment: the pull request and which of
// Town's closes of it the comment explains. A pull request reopened and
// closed again legitimately requeues its issue a second time, and that second
// comment must not be mistaken for the first.
func requeueMarker(task *Task) string {
	return fmt.Sprintf("<!-- brokk-town:requeued pr=%d close=%d -->", task.Number, task.Closes)
}

func requeueComment(task *Task) string {
	return fmt.Sprintf("## Brokk Town\n\nPull request #%d was closed after %s. The next attempt starts over from the current base branch and should address the reason:\n\n%s\n\n%s", task.Number, closedCause(task), task.Detail, requeueMarker(task))
}

// requeuedIssue returns the issue closing this pull request starts over, or
// nil when the live state says the issue is not waiting on it: the issue is
// closed, locked or declined, it is not implemented (Issue Bot is already on
// a fresh attempt, or Town already requeued it for this pull request), or
// another of Town's open pull requests implements it.
func requeuedIssue(t *Town, task *Task) *Task {
	owned := t.Owned[task.Number]
	if owned.Issue <= 0 {
		return nil
	}
	issue := t.Tasks[fmt.Sprintf("issue:%d", owned.Issue)]
	if issue == nil || issue.Stage != "implemented" || issue.MayoralDecision == "declined" || issue.Requeue == task.Number {
		return nil
	}
	for m, other := range t.Owned {
		if m == task.Number || other.Issue != owned.Issue {
			continue
		}
		if pr := t.Tasks[fmt.Sprintf("pr:%d", m)]; pr != nil && pr.Stage != "merged" && pr.Stage != "closed" {
			return nil
		}
	}
	return issue
}

// postClose runs one step owed after GitHub closed the pull request. A
// definite rejection cannot succeed on retry (an issue that is gone or not
// writable), so it is noted and the close is finished anyway; any other
// failure keeps the claim for the next inventory.
func postClose(failures *error, skipped *[]string, what string, err error) bool {
	if err == nil {
		return true
	}
	*failures = errors.Join(*failures, fmt.Errorf("%s: %w", what, err))
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		*skipped = append(*skipped, fmt.Sprintf("%s (HTTP %d)", what, rejected.Status))
		return true
	}
	return false
}

// explainRequeue posts the issue's requeue comment unless GitHub already has
// it, so an uncertain post is not repeated.
func (s *Supervisor) explainRequeue(ctx context.Context, repo string, issue int, task *Task) error {
	comments, err := s.GitHub.IssueComments(ctx, repo, issue)
	if err != nil {
		return err
	}
	for _, body := range comments {
		if strings.Contains(body, requeueMarker(task)) {
			return nil
		}
	}
	return s.GitHub.Comment(ctx, repo, issue, requeueComment(task))
}

// closeRetiredPulls performs the GitHub side of a closing decision: close the
// pull request, delete Town's branch, tell the issue why, then queue the issue
// again. Each step is idempotent, so a failed step is retried by the next
// inventory without repeating the ones that succeeded.
//
// Issue Bot pushes its fresh attempt to the same branch name without force, so
// a branch GitHub refused to delete (a protected branch) holds the issue: the
// pull request is recorded closed with BranchKept, the issue says why, and
// every inventory tries the delete again. Once the branch is gone, whoever
// removed it, the issue starts over.
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
		if task.Kind == "pr" && (task.Stage == "closing" || (task.Stage == "closed" && task.BranchKept)) {
			numbers = append(numbers, task.Number)
		}
	}
	sort.Ints(numbers)
	var failures error
	for _, n := range numbers {
		task := t.Tasks[fmt.Sprintf("pr:%d", n)]
		owned := t.Owned[n]
		if task.Stage == "closed" {
			if err := s.retryKeptBranch(ctx, t, task, owned); err != nil {
				failures = errors.Join(failures, err)
			}
			continue
		}
		if p, known := pulls[n]; known && p.MergedAt != nil {
			continue // The inventory records the merge; there is nothing to close.
		}
		requeued := 0
		if issue := requeuedIssue(t, task); issue != nil {
			requeued = issue.Number
		}
		skipped := []string{}
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
			err := s.GitHub.Comment(ctx, t.Config.Repo, n, closingComment(task, requeued))
			if !postClose(&failures, &skipped, fmt.Sprintf("explain closing PR #%d", n), err) {
				continue
			}
		}
		kept := ""
		if owned.Branch != "" && !branchInUse(t, n, owned.Branch) {
			err := s.GitHub.DeleteBranch(ctx, t.Config.Repo, owned.Branch)
			var rejected *RejectedError
			if errors.As(err, &rejected) {
				failures = errors.Join(failures, fmt.Errorf("delete branch %s of PR #%d: %w", owned.Branch, n, err))
				kept = fmt.Sprintf("GitHub refused to delete branch %s (HTTP %d).", owned.Branch, rejected.Status)
			} else if err != nil {
				failures = errors.Join(failures, fmt.Errorf("delete branch %s of PR #%d: %w", owned.Branch, n, err))
				continue
			}
		}
		if requeued > 0 && kept == "" {
			if !postClose(&failures, &skipped, fmt.Sprintf("explain requeue of issue #%d", requeued), s.explainRequeue(ctx, t.Config.Repo, requeued, task)) {
				continue
			}
		}
		if err := s.Store.Update(func(st *State) error {
			finalizeClosedPull(st, st.Towns[t.ID], n, skipped, kept, s.now())
			return nil
		}); err != nil {
			return errors.Join(failures, err)
		}
	}
	return failures
}

// branchInUse reports whether another of Town's open pull requests has the
// same head branch. Issue Bot reuses one branch name per issue, so the branch
// of an old pull request can now carry a newer one; deleting it would make
// GitHub close that pull request. The old pull request's branch is then
// treated as gone.
func branchInUse(t *Town, n int, branch string) bool {
	for m, other := range t.Owned {
		if m == n || other.Branch != branch {
			continue
		}
		if pr := t.Tasks[fmt.Sprintf("pr:%d", m)]; pr != nil && pr.Stage != "merged" && pr.Stage != "closed" {
			return true
		}
	}
	return false
}

// retryKeptBranch tries again to delete the branch of a pull request Town
// closed, and starts its issue over once the branch is gone. A repeated
// refusal is expected until someone removes the branch, so it is not reported.
func (s *Supervisor) retryKeptBranch(ctx context.Context, t *Town, task *Task, owned Ownership) error {
	if owned.Branch != "" && !branchInUse(t, task.Number, owned.Branch) {
		err := s.GitHub.DeleteBranch(ctx, t.Config.Repo, owned.Branch)
		var rejected *RejectedError
		if errors.As(err, &rejected) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("delete branch %s of PR #%d: %w", owned.Branch, task.Number, err)
		}
	}
	if issue := requeuedIssue(t, task); issue != nil {
		if err := s.explainRequeue(ctx, t.Config.Repo, issue.Number, task); err != nil {
			var rejected *RejectedError
			if !errors.As(err, &rejected) {
				return fmt.Errorf("explain requeue of issue #%d: %w", issue.Number, err)
			}
		}
	}
	return s.Store.Update(func(st *State) error {
		current := st.Towns[t.ID]
		if current == nil {
			return nil
		}
		x := current.Tasks[task.ID]
		if x == nil || x.Stage != "closed" || !x.BranchKept {
			return nil
		}
		x.BranchKept = false
		x.Updated = s.now()
		st.Event(t.ID, "delivery", "hall", "outside", x.ID, "Branch removed: "+x.Title, s.now())
		requeueAfterClose(st, current, x, s.now())
		return nil
	})
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
// fresh attempt that starts over rather than on top of the closed work. When
// its branch was kept, the issue is held with an explanation instead.
func finalizeClosedPull(st *State, t *Town, n int, skipped []string, kept string, now time.Time) {
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
	if len(skipped) > 0 {
		task.Detail += "\n\nGitHub refused, so Town did not: " + strings.Join(skipped, "; ") + "."
		st.Event(t.ID, "error", string(Repo), "hall", task.ID, "Closed with steps GitHub refused: "+task.Title, now)
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
	if kept == "" {
		requeueAfterClose(st, t, task, now)
		return
	}
	task.BranchKept = true
	task.Detail += "\n\n" + kept + " Town retries the delete at each inventory."
	st.Event(t.ID, "error", string(Repo), "hall", task.ID, "Branch kept after close: "+task.Title, now)
	if issue := requeuedIssue(t, task); issue != nil {
		issue.Detail = fmt.Sprintf("PR #%d was closed after %s, but %s Issue Bot pushes its next attempt to that branch name, so delete %s on GitHub by hand; Town then starts this issue over.", n, closedCause(task), kept, owned.Branch)
		issue.Updated = now
	}
}

// requeueAfterClose queues the closed pull request's issue for a fresh attempt
// when the issue is still waiting on it.
func requeueAfterClose(st *State, t *Town, task *Task, now time.Time) {
	issue := requeuedIssue(t, task)
	if issue == nil {
		return
	}
	n := task.Number
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

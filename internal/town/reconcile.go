package town

import (
	"fmt"
	"strings"
	"time"
)

// Reconcile consumes a complete remote inventory. First inventory is a baseline,
// subsequent transitions generate deliveries exactly once, irrespective of polls.
func Reconcile(s *State, t *Town, remote RepoSnapshot, now time.Time) {
	initial := !t.Initialized
	changes := []string{}
	// The observed default is repository state, not configuration. Writing it
	// into Config.Branch pinned whatever GitHub reported at the first poll, so a
	// later rename left the town looking up a branch that no longer exists, and
	// an operator's own choice was silently replaced on every poll.
	observed := remote.DefaultBranch
	if observed == "" && t.Config.Branch == "" {
		// An inventory that reports only the branch it covered covered the
		// repository default, because this town has not chosen a branch.
		observed = remote.Branch
	}
	// A default Town cannot fetch is not recorded: keeping the last usable
	// observation is better than failing every reconcile on state validation.
	if ValidBranch(observed) {
		t.DefaultBranch = observed
	}
	for _, i := range remote.Issues {
		if len(i.Pull) > 0 && string(i.Pull) != "null" {
			// An explicit null is an issue: that is how an inventory
			// re-encoded from RemoteIssue reports "no pull request".
			continue
		}
		id := fmt.Sprintf("issue:%d", i.Number)
		task := t.Tasks[id]
		if task == nil {
			from := "outside"
			if strings.Contains(i.Body, "<!-- feature-bot:") {
				from = "feature"
			} else if strings.Contains(i.Body, "<!-- bug-bot:") {
				from = "bug"
			} else if strings.Contains(i.Body, "<!-- simplifier-bot:") {
				from = "simplifier"
			} else if strings.Contains(i.Body, "<!-- review-bot:") {
				from = "review"
			}
			house, stage, decision := Issue, "queued", ""
			if from == "simplifier" && t.Config.SimplifierModeOrDefault() == "suggest" {
				house, stage, decision = Hall, "awaiting_mayor", "pending"
			} else if from != "simplifier" {
				house, stage = Simplifier, "simplifying"
			}
			task = &Task{ID: id, Kind: "issue", Number: i.Number, Title: i.Title, URL: i.URL, House: house, External: from == "outside", Stage: stage, MayoralDecision: decision, Updated: now}
			t.Tasks[id] = task
			if from == "bug" || from == "feature" {
				t.RecordOutcome(OutcomeRecord{ID: "finding-filed:" + id, At: now, Class: "artifact", Kind: "finding_filed", Status: "confirmed", Role: Role(from), TaskID: id, URL: i.URL, Detail: i.Title})
			}
			if !initial && i.State == "open" {
				s.Event(t.ID, "delivery", from, string(house), id, "Issue arrived: "+i.Title, now)
				changes = append(changes, fmt.Sprintf("New issue #%d: %s", i.Number, i.Title))
			}
			if stage == "simplifying" && t.Workers[Simplifier] != nil {
				t.Workers[Simplifier].Next = time.Time{}
			}
		}
		task.Title = i.Title
		task.URL = i.URL
		task.Labels = LabelNames(i.Labels)
		if task.MayoralDecision == "declined" {
			// A decline is final, and closing the issue is how Town carries it
			// out; the resulting closure must not erase the decision.
		} else if i.State == "closed" {
			task.Stage = "closed"
			if task.MayoralDecision == "pending" {
				// A pending decision lives only in Town Hall; resume restores
				// it if the issue is reopened.
				task.MayoralDecision = ""
			}
		} else if task.MayoralDecision == "pending" {
			// Repository metadata cannot bypass or reopen a Mayoral decision.
		} else if i.Locked {
			task.Stage = "locked"
		} else if task.Stage == "closed" || task.Stage == "locked" || (task.Stage == "queued" && task.House != Issue) {
			// The last case heals an issue an earlier reopen stranded in a
			// house that never selects queued work.
			resume(t, task)
		}
	}
	listed := map[int]bool{}
	for _, i := range remote.Issues {
		listed[i.Number] = true
	}
	for _, task := range t.Tasks {
		if task.Kind == "issue" && task.Stage == "closing" && task.MayoralDecision == "" && !listed[task.Number] {
			// The inventory lists every issue, so one Town was closing that is
			// gone (deleted or transferred) has nothing left to close. The
			// claim is released; the decline stands.
			task.Stage = "declined"
			task.Updated = now
		}
	}
	for _, p := range remote.Pulls {
		id := fmt.Sprintf("pr:%d", p.Number)
		if p.Base.Ref != remote.Branch {
			// A pull request can be retargeted without touching its head or
			// base commit, so a task Town already reviewed keeps a clean audit
			// that now describes a merge into a branch this town does not
			// cover. Skipping quietly left that audit usable; the task is
			// retired from this town's work instead.
			if task := t.Tasks[id]; task != nil && task.Stage != "merged" && task.Stage != "closed" {
				if !task.Offbranch {
					s.Event(t.ID, "delivery", string(task.House), "hall", id, fmt.Sprintf("PR retargeted to %s; outside this town", p.Base.Ref), now)
				}
				task.Audit = nil
				task.Offbranch = true
				task.Blocked = true
				task.Detail = fmt.Sprintf("Targets %s, but this town covers %s. Town stops work on it until it targets %s again.", p.Base.Ref, remote.Branch, remote.Branch)
				task.Updated = now
			}
			continue
		}
		task := t.Tasks[id]
		// A repair may finish while the remote inventory is in flight. Do not
		// overwrite its confirmed head with the older observation; poll again.
		if remote.ObservedHeads != nil && task != nil && task.Head != remote.ObservedHeads[p.Number] {
			continue
		}
		owned := t.Owned[p.Number]
		isOwned := owned.Branch != "" && owned.Branch == p.Head.Ref && strings.EqualFold(p.Head.Repo.FullName, t.Config.Repo)
		if task == nil {
			house, stage := Simplifier, "simplifying"
			decision := ""
			if !isOwned && t.Config.SimplifierModeOrDefault() == "suggest" {
				house, stage, decision = Hall, "awaiting_mayor", "pending"
			}
			task = &Task{ID: id, Kind: "pr", Number: p.Number, Title: p.Title, URL: p.URL, House: house, Stage: stage, External: !isOwned, MayoralDecision: decision, Updated: now}
			t.Tasks[id] = task
			if !initial && p.State == "open" {
				from := "issue"
				if !isOwned {
					from = "outside"
				}
				s.Event(t.ID, "delivery", from, string(house), id, "PR arrived: "+p.Title, now)
				changes = append(changes, fmt.Sprintf("New PR #%d: %s", p.Number, p.Title))
			}
			if stage == "simplifying" && t.Workers[Simplifier] != nil {
				t.Workers[Simplifier].Next = time.Time{}
			}
		}
		if isOwned {
			related := fmt.Sprintf("issue:%d", owned.Issue)
			t.RecordOutcome(OutcomeRecord{ID: "implementation-pr:" + id, At: now, Class: "artifact", Kind: "implementation_pr", Status: "submitted", Role: Issue, TaskID: id, RelatedTaskID: related, Revision: p.Head.SHA, URL: p.URL, Detail: p.Title})
		}
		if autoDeclined(task) && (task.House != Hall || (task.MayoralDecision == "pending" && task.External) || task.Retired || task.Audit != nil) {
			// Older state let a revision carry an auto-declined pull request
			// back to Review, from where a review can send it to the Mayor;
			// the decline no longer describes where it is. A declined pull
			// request is never reviewed or retired. Town's own pull request
			// awaits the Mayor with a decline only on appeal (see resume),
			// and keeps the assessment the Mayor judges it with.
			task.Simplification = nil
		}
		if task.Offbranch {
			// Town blocked this itself when the pull request left the branch.
			// It is back, so the block is released and a fresh review decides
			// it; nothing about the earlier audit is reused. Intake and a
			// decline are not review, so they resume where they stood.
			task.Offbranch = false
			task.Blocked = false
			task.Attempts = 0
			task.RetryAt = time.Time{}
			task.Audit = nil
			if holdsIntake(task) {
				s.Event(t.ID, "delivery", "outside", string(task.House), id, "PR targets this town's branch again: "+p.Title, now)
				wake(t, task)
			} else {
				s.Move(t, task, "queued", Review, "PR targets this town's branch again: back for review", now)
			}
		}
		wasExternal := task.External
		task.External = !isOwned
		if isOwned && wasExternal && task.MayoralDecision == "pending" {
			task.MayoralDecision = ""
			task.Stage = "queued"
			task.House = Review
		}
		task.Title = p.Title
		task.URL = p.URL
		task.Labels = LabelNames(p.Labels)
		task.Branch = p.Head.Ref
		if (task.Head != "" && task.Head != p.Head.SHA) || (task.Base != "" && task.Base != p.Base.SHA) || (task.Description != "" && task.Description != description(p)) {
			if task.MergeWait != nil {
				task.MergeWait = nil
				task.Detail = ""
			}
			if task.Head != "" && task.Head != p.Head.SHA && !isOwned {
				t.RecordOutcome(OutcomeRecord{ID: "external-change:" + id + ":" + p.Head.SHA, At: now, Class: "outcome", Kind: "external_change", Status: "external", Role: Review, TaskID: id, Revision: p.Head.SHA, URL: p.URL, Detail: "Contributor changed the pull request revision"})
			}
			task.Audit = nil
			task.Blocked = false
			task.Attempts = 0
			task.RetryAt = time.Time{}
			task.FollowUps = nil
			if task.Stage != "merged" && task.Stage != "closed" && task.Stage != "closing" && !holdsIntake(task) {
				s.Move(t, task, "queued", Review, "New revision ready for review", now)
			}
		}
		task.Description = description(p)
		task.Head = p.Head.SHA
		task.Base = p.Base.SHA
		if intent := t.Intents[p.Number]; intent != nil && intent.Kind == "repair" && p.Head.SHA == intent.NewHead {
			if intent.Status != "confirmed" {
				intent.Status = "confirmed"
				task.Blocked = false
				task.Attempts = 0
				task.Cycles++
				t.RecordOutcome(OutcomeRecord{ID: fmt.Sprintf("repair-round:%s:%s", id, intent.NewHead), At: now, Class: "outcome", Kind: "repair_round", Status: "confirmed", Role: Issue, TaskID: id, RelatedTaskID: fmt.Sprintf("issue:%d", owned.Issue), Revision: intent.NewHead, URL: p.URL, Detail: fmt.Sprintf("Repair round %d confirmed on GitHub", task.Cycles)})
				task.Audit = nil
				s.Move(t, task, "queued", Review, "Fixes delivered for another review", now)
			}
		}
		if p.MergedAt != nil {
			task.MayoralDecision = ""
			if intent := t.Intents[p.Number]; intent != nil && intent.Kind == "merge" {
				intent.Status = "confirmed"
				task.Blocked = false
			}
			if task.Stage != "merged" {
				if !initial {
					s.Move(t, task, "merged", Release, "Merged: "+p.Title, now)
					changes = append(changes, fmt.Sprintf("Merged PR #%d", p.Number))
				} else {
					task.Stage = "merged"
					task.House = Release
				}
			}
			t.RecordOutcome(OutcomeRecord{ID: "merge:" + id + ":" + p.MergeCommit, At: *p.MergedAt, Class: "outcome", Kind: "merge", Status: "confirmed", TaskID: id, RelatedTaskID: fmt.Sprintf("issue:%d", owned.Issue), Revision: p.MergeCommit, URL: p.URL, Detail: p.Title})
			if SHA(p.MergeCommit) {
				cid := "commit:" + p.MergeCommit
				if t.Tasks[cid] == nil {
					t.Tasks[cid] = &Task{ID: cid, Kind: "commit", Title: p.Title, URL: p.URL, Stage: "unreleased", House: Release, Head: p.MergeCommit, Updated: *p.MergedAt}
				}
			}
		} else if task.Stage == "closing" && p.State == "closed" {
			// Town's close was accepted, or its outcome was uncertain and it
			// happened, but a later step (the comments, the branch, starting
			// the issue over) is still owed. The claim stays until the closer
			// finishes it; settling it here stranded the issue as implemented.
		} else if p.State == "closed" {
			if isOwned && task.Stage != "closed" {
				t.RecordOutcome(OutcomeRecord{ID: "abandoned:" + id + ":" + p.Head.SHA, At: now, Class: "outcome", Kind: "abandoned", Status: "abandoned", TaskID: id, RelatedTaskID: fmt.Sprintf("issue:%d", owned.Issue), Revision: p.Head.SHA, URL: p.URL, Detail: "Implementation PR closed without a confirmed merge"})
			}
			// A Mayoral decline stays on the closed pull request; resume
			// restores it if the pull request is reopened.
			task.Stage = "closed"
			if task.MayoralDecision == "pending" {
				task.MayoralDecision = ""
			}
		} else {
			if task.Stage == "closed" {
				// Reopened: intake resumes before draft or lock state applies,
				// so a pull request reopened as a draft cannot leave intake by
				// way of a later revision.
				resume(t, task)
			}
			if task.Stage == "closing" {
				// Town is closing this pull request; the next inventory finishes
				// it. A draft or lock change does not cancel the close.
			} else if holdsIntake(task) {
				// A contributor revision or draft-state change never bypasses the
				// durable Simplifier or Mayoral intake decision.
			} else if p.Draft {
				task.Stage = "draft"
			} else if p.Locked {
				task.Stage = "locked"
			} else if task.Stage == "draft" || task.Stage == "locked" {
				resume(t, task)
			}
		}
		if isOwned && (p.MergedAt != nil || p.State != "closed") {
			if it := t.Tasks[fmt.Sprintf("issue:%d", owned.Issue)]; it != nil && it.Stage != "closed" && it.Requeue != p.Number {
				it.Stage = "implemented"
			}
		}
	}
	for _, c := range remote.Commits {
		if !SHA(c.SHA) {
			continue
		}
		id := "commit:" + c.SHA
		title := strings.SplitN(c.Commit.Message, "\n", 2)[0]
		if t.Tasks[id] == nil {
			t.Tasks[id] = &Task{ID: id, Kind: "commit", Title: title, URL: c.URL, Stage: "unreleased", House: Release, Head: c.SHA, External: true, Updated: now}
			if !initial {
				s.Event(t.ID, "delivery", "outside", "release", id, "Change arrived: "+title, now)
			}
		}
		changes = append(changes, c.SHA[:8]+": "+title)
	}
	if t.Head != "" && t.Head != remote.Head && !initial {
		s.Event(t.ID, "change", "outside", "release", "commit:"+remote.Head, "Release branch advanced", now)
		changes = append(changes, "New changes reached "+remote.Branch)
	}
	t.Head = remote.Head
	latest := latestRelease(remote.Releases)
	if latest != nil && latest.Tag != t.LastRelease {
		if !initial {
			s.Event(t.ID, "delivery", "release", "outside", "release:"+latest.Tag, "Shipped "+latest.Tag, now)
			changes = append(changes, "Released "+latest.Tag)
		}
		t.LastRelease = latest.Tag
	}
	if latest != nil {
		t.RecordOutcome(OutcomeRecord{ID: "release:" + latest.Tag, At: latest.At, Class: "outcome", Kind: "release", Status: "confirmed", Role: Release, TaskID: "release:" + latest.Tag, Revision: latest.Tag, URL: latest.URL, Detail: latest.Name})
	}
	for _, task := range t.Tasks {
		if task.Kind == "commit" && remote.Released[task.Head] {
			task.Stage = "shipped"
		}
	}

	if initial || len(changes) > 0 || len(t.Reports) == 0 || now.Sub(t.Reports[len(t.Reports)-1].At) >= time.Duration(t.Config.ReportSeconds)*time.Second {
		openIssues, openPRs, blocked := 0, 0, 0
		for _, task := range t.Tasks {
			if task.Kind == "issue" && task.Stage == "queued" {
				openIssues++
			}
			if task.Kind == "pr" && task.Stage != "merged" && task.Stage != "closed" {
				openPRs++
			}
			if task.Blocked {
				blocked++
			}
		}
		title := "Repository check-in"
		if initial {
			title = "Town inventory"
		} else if len(changes) > 0 {
			title = fmt.Sprintf("%d changes in town", len(changes))
		}
		body := fmt.Sprintf("%d queued issues · %d open PRs · %d blocked tasks. Latest release: %s.", openIssues, openPRs, blocked, t.LastRelease)
		if len(changes) > 0 {
			body += "\n" + strings.Join(changes, "\n")
		}
		if len(changes) > 10 {
			title = "Busy arrivals: " + title
		}
		t.Report(title, body, now)
		s.Event(t.ID, "report", "repo", "hall", "", title, now)
	}
	t.Initialized = true
	t.LastSync = now
	t.Error = ""
}

// autoDeclined reports whether Simplifier's auto mode declined the task, a
// decision as final as the Mayor's.
func autoDeclined(task *Task) bool {
	return task.Simplification != nil && task.Simplification.Mode == "auto" && task.Simplification.Decision == "decline"
}

// holdsIntake reports whether a pull request is still in intake or carries a
// final decline. Repository metadata never moves such a task to Review.
func holdsIntake(task *Task) bool {
	return task.Stage == "simplifying" || task.Stage == "declined" || task.MayoralDecision == "pending" || task.MayoralDecision == "declined"
}

// wake lets the house now holding a task pick it up without waiting out its
// poll interval.
func wake(t *Town, task *Task) {
	house := task.House
	switch {
	case task.Stage == "simplifying":
		house = Simplifier
	case task.MayoralDecision == "pending":
		house = Hall
	case task.Stage != "queued":
		return
	}
	if w := t.Workers[house]; w != nil {
		w.Next = time.Time{}
	}
}

// resume returns an issue or pull request to work after GitHub reopened,
// unlocked or undrafted it. A closure clears a pending Mayoral decision, so the
// house is the durable record of how far intake got: work that had not finished
// intake goes back to it rather than straight to the worker after it, and a
// Mayoral or Simplifier decline stays final. Blocked, Attempts and RetryAt are
// left as they were.
func resume(t *Town, task *Task) {
	switch {
	case task.Kind == "issue" && task.House == Hall && task.MayoralDecision == "" && autoDeclined(task) && task.Stage == "closed":
		// Someone reopened an issue Simplifier declined, which Town closes.
		// Closing it again would ignore them and admitting it would bypass
		// the Mayor, so the reopen is an appeal: the Mayor decides it, with
		// Simplifier's assessment attached, and the closer leaves it alone.
		task.Stage = "awaiting_mayor"
		task.MayoralDecision = "pending"
		task.Detail = "Reopened after Simplifier declined it and Town closed it. The Mayor decides whether Town takes it on."
	case task.Kind == "pr" && !task.External && task.House == Hall && task.MayoralDecision == "" && autoDeclined(task) && task.Stage == "closed":
		// Town's own pull request, closed on Simplifier's decline, reopened:
		// an appeal as for an issue. The issue has already started over.
		task.Stage = "awaiting_mayor"
		task.MayoralDecision = "pending"
		task.Detail = "Reopened after Simplifier declined it and Town closed it. The Mayor decides whether Town reviews it; its issue has already started over."
	case task.MayoralDecision == "declined" || (task.House == Hall && autoDeclined(task)):
		task.Stage = "declined"
		return
	case task.House == Simplifier:
		task.Stage = "simplifying"
	case task.House == Hall && (task.Kind == "issue" || task.External):
		// An issue reaches Town Hall only to await the Mayor, and an outside
		// pull request only for the Mayor to decide it. Town's own pull
		// requests reach Town Hall only to be closed after review.
		task.Stage = "awaiting_mayor"
		task.MayoralDecision = "pending"
	case task.Kind == "issue":
		task.Stage = "queued"
		task.House = Issue
	default:
		task.Stage = "queued"
		task.House = Review
	}
	wake(t, task)
}

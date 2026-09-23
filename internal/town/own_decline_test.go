package town

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ownPullTown is a town in the given Simplifier mode whose issue-bot opened
// pull request #5 for issue #3, which Simplifier is assessing.
func ownPullTown(t *testing.T, mode string) (*Store, *Town, *fakeGH, *Supervisor) {
	t.Helper()
	store := testStore(t, false)
	x := addTown(t, store)
	gh := newGH(5)
	gh.snapshot.Issues = []RemoteIssue{{Number: 3, Title: "Issue 3", State: "open"}}
	update(t, store, func(st *State) {
		town := st.Towns[x.ID]
		town.Config.SimplifierMode = mode
		town.Owned[5] = Ownership{Branch: pull(5).Head.Ref, Issue: 3}
		town.Tasks["issue:3"] = &Task{ID: "issue:3", Kind: "issue", Number: 3, Title: "Issue 3", Stage: "queued", House: Issue, Updated: time.Now()}
		Reconcile(st, town, gh.snapshot, time.Now())
	})
	town := store.Snapshot().Towns[x.ID]
	if task := town.Tasks["pr:5"]; task.External || task.Stage != "simplifying" || town.Tasks["issue:3"].Stage != "implemented" {
		t.Fatalf("own PR did not enter intake with its issue implemented: %+v", task)
	}
	return store, town, gh, NewSupervisor(store, gh, observing{gh: gh})
}

func declineOwnPull(t *testing.T, store *Store, town *Town) {
	t.Helper()
	update(t, store, func(st *State) {
		x := st.Towns[town.ID]
		applySimplification(st, x, x.Tasks["pr:5"], &Simplification{Mode: "auto", Decision: "decline", Summary: "Too much machinery", Detail: "A registry for one caller."}, nil, time.Now())
	})
}

func task(store *Store, town *Town, id string) *Task {
	return store.Snapshot().Towns[town.ID].Tasks[id]
}

func TestSimplifierDeclinedOwnPullIsClosedAndItsIssueStartsOver(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	pr := task(store, town, "pr:5")
	if pr.Stage != "declined" || pr.House != Hall || !strings.Contains(pr.Detail, "Town is closing it") {
		t.Fatalf("own PR decline: %+v", pr)
	}
	ctx := context.Background()
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 1 || gh.closedPulls[0] != 5 || len(gh.deleted) != 1 || gh.deleted[0] != "issue-5" || len(gh.comments) != 2 {
		t.Fatalf("GitHub side of the close: closed=%v deleted=%v comments=%v", gh.closedPulls, gh.deleted, gh.comments)
	}
	if !strings.Contains(gh.comments[0], "A registry for one caller.") || !strings.Contains(gh.comments[1], "closed after Simplifier declined it") {
		t.Fatalf("comments do not explain the decline: %v", gh.comments)
	}
	pr = task(store, town, "pr:5")
	if pr.Stage != "closed" || pr.House != Hall || !autoDeclined(pr) {
		t.Fatalf("closed PR lost its decline: %+v", pr)
	}
	issue := task(store, town, "issue:3")
	if issue.Stage != "queued" || issue.House != Issue || issue.Requeue != 5 || !strings.Contains(issue.Detail, "Simplifier declined") {
		t.Fatalf("issue did not start over: %+v", issue)
	}
	// The next inventory sees the closure and repeats nothing.
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 1 || len(gh.comments) != 2 || task(store, town, "issue:3").Stage != "queued" {
		t.Fatalf("closing repeated: closed=%v comments=%d issue=%+v", gh.closedPulls, len(gh.comments), task(store, town, "issue:3"))
	}
}

func TestOutsideDeclinedPullIsStillLeftOpen(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	gh := newGH(5)
	update(t, store, func(st *State) {
		town := st.Towns[x.ID]
		town.Config.SimplifierMode = "auto"
		Reconcile(st, town, gh.snapshot, time.Now())
		applySimplification(st, town, town.Tasks["pr:5"], &Simplification{Mode: "auto", Decision: "decline", Detail: "Out of scope."}, nil, time.Now())
	})
	sup := NewSupervisor(store, gh, observing{gh: gh})
	if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 0 || task(store, x, "pr:5").Stage != "declined" {
		t.Fatalf("Town closed a contributor's PR: %v", gh.closedPulls)
	}
}

func TestDeclinedOwnPullWaitsOutQuietHoursAndCanBeAdmitted(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	update(t, store, func(st *State) {
		st.Towns[town.ID].Config.QuietHours = &[]QuietWindow{{Days: []string{"mon"}, Start: "18:00", End: "19:00"}}
	})
	declineOwnPull(t, store, town)
	sup.now = func() time.Time { return local(21, 18, 30) }
	if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 0 || task(store, town, "pr:5").Stage != "declined" {
		t.Fatalf("closed inside quiet hours: %v %+v", gh.closedPulls, task(store, town, "pr:5"))
	}
	if err := sup.Control(town.ID, Hall, "admit", "pr:5"); err != nil {
		t.Fatal(err)
	}
	sup.now = func() time.Time { return local(21, 19, 30) }
	if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if pr := task(store, town, "pr:5"); len(gh.closedPulls) != 0 || pr.House != Review || pr.Stage != "queued" {
		t.Fatalf("admitted PR was closed or not reviewed: %v %+v", gh.closedPulls, pr)
	}
}

func TestSnoozedDeclinedOwnPullIsNotClaimed(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	until := time.Now().Add(time.Hour)
	update(t, store, func(st *State) { st.Towns[town.ID].Tasks["pr:5"].DeferredUntil = until })
	declineOwnPull(t, store, town)
	if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 0 || task(store, town, "pr:5").Stage != "declined" {
		t.Fatalf("snoozed PR was closed: %v", gh.closedPulls)
	}
	sup.now = func() time.Time { return until.Add(time.Minute) }
	if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 1 {
		t.Fatalf("PR was not closed after its snooze: %v", gh.closedPulls)
	}
}

func TestUncertainCloseOfDeclinedOwnPullIsFinished(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	ctx := context.Background()
	gh.closeError = errors.New("connection reset")
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err == nil {
		t.Fatal("a failed close was not reported")
	}
	if pr := task(store, town, "pr:5"); pr.Stage != "closing" {
		t.Fatalf("uncertain close released the claim: %+v", pr)
	}
	if err := sup.Control(town.ID, Hall, "admit", "pr:5"); err == nil {
		t.Fatal("the Mayor admitted a PR Town may already have closed")
	}
	// The close did happen; the next inventory lists the PR closed. The claim
	// stays until the branch, the comments and the requeue are done.
	gh.closeError = nil
	gh.p.State = "closed"
	gh.snapshot.Pulls = []Pull{gh.p}
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 0 || len(gh.deleted) != 1 {
		t.Fatalf("finishing the close: closed=%v deleted=%v", gh.closedPulls, gh.deleted)
	}
	if pr, issue := task(store, town, "pr:5"), task(store, town, "issue:3"); pr.Stage != "closed" || issue.Stage != "queued" || issue.Requeue != 5 {
		t.Fatalf("uncertain close stranded the issue: pr=%s issue=%+v", pr.Stage, issue)
	}
}

func TestRejectedCloseOfDeclinedOwnPullReleasesTheClaim(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	gh.closeError = definiteRejection(errors.New("gh api: exit status 1\ngh: Resource not accessible by integration (HTTP 403)"))
	if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[town.ID]); err == nil {
		t.Fatal("a rejected close was not reported")
	}
	if pr := task(store, town, "pr:5"); pr.Stage != "declined" || !strings.Contains(pr.Detail, "HTTP 403") {
		t.Fatalf("a rejected close kept its claim: %+v", pr)
	}
	if err := sup.Control(town.ID, Hall, "admit", "pr:5"); err != nil {
		t.Fatalf("the Mayor could not admit after GitHub refused the close: %v", err)
	}
}

func TestDraftDoesNotCancelClosingOwnPull(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	update(t, store, func(st *State) { claimDeclinedPulls(st, st.Towns[town.ID], time.Now()) })
	draft := pull(5)
	draft.Draft = true
	update(t, store, func(st *State) { Reconcile(st, st.Towns[town.ID], inventory(draft), time.Now()) })
	if pr := task(store, town, "pr:5"); pr.Stage != "closing" {
		t.Fatalf("a draft change cancelled the close: %+v", pr)
	}
	gh.snapshot.Pulls = []Pull{draft}
	if err := sup.closeRetiredPulls(context.Background(), store.Snapshot().Towns[town.ID], gh.snapshot); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 1 {
		t.Fatalf("draft PR was not closed: %v", gh.closedPulls)
	}
}

func TestReopenedOwnPullClosedOnSimplifierDeclineAppealsToTheMayor(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	ctx := context.Background()
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	gh.p.State = "open"
	gh.snapshot.Pulls = []Pull{gh.p}
	for range 2 {
		if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
			t.Fatal(err)
		}
	}
	current := store.Snapshot().Towns[town.ID]
	pr := current.Tasks["pr:5"]
	if nextJudgment(current, time.Now()) == nil || pr.MayoralDecision != "pending" || !autoDeclined(pr) {
		t.Fatalf("reopened PR did not wait for the Mayor with Simplifier's assessment: %+v", pr)
	}
	if len(gh.closedPulls) != 1 {
		t.Fatalf("Town closed the reopened PR again: %v", gh.closedPulls)
	}
	if issue := current.Tasks["issue:3"]; issue.Stage != "queued" {
		t.Fatalf("appeal marked the restarted issue implemented: %+v", issue)
	}
}

func TestMayorDeclinedOwnPullIsClosedAndStaysDeclined(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "suggest")
	update(t, store, func(st *State) {
		x := st.Towns[town.ID]
		applySimplification(st, x, x.Tasks["pr:5"], &Simplification{Mode: "suggest", Decision: "decline", Detail: "A registry for one caller."}, nil, time.Now())
	})
	if err := sup.Control(town.ID, Hall, "decline", "pr:5"); err != nil {
		t.Fatal(err)
	}
	if pr := task(store, town, "pr:5"); !strings.Contains(pr.Detail, "Town is closing it") {
		t.Fatalf("Mayoral decline of own PR: %+v", pr)
	}
	ctx := context.Background()
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	pr, issue := task(store, town, "pr:5"), task(store, town, "issue:3")
	if len(gh.closedPulls) != 1 || pr.Stage != "closed" || pr.MayoralDecision != "declined" || issue.Stage != "queued" || issue.Requeue != 5 {
		t.Fatalf("Mayor-declined own PR: closed=%v pr=%+v issue=%+v", gh.closedPulls, pr, issue)
	}
	// Issue Bot has moved on: its fresh attempt is pull request #7, and the
	// requeue marker is spent (SyncIssues clears it once the job moves on).
	fresh := pull(7)
	fresh.Head.Ref = "issue-3-retry"
	update(t, store, func(st *State) {
		x := st.Towns[town.ID]
		x.Owned[7] = Ownership{Branch: fresh.Head.Ref, Issue: 3}
		x.Tasks["issue:3"].Requeue = 0
		x.Tasks["issue:3"].IssueJob = &IssueJob{Status: "submitted"}
	})
	// A reopen does not undo the Mayor's decline: Town closes it again, but
	// leaves the issue's new attempt alone.
	gh.p.State = "open"
	gh.snapshot.Pulls = []Pull{gh.p, fresh}
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 2 || task(store, town, "pr:5").MayoralDecision != "declined" {
		t.Fatalf("reopened Mayor-declined PR: closed=%v %+v", gh.closedPulls, task(store, town, "pr:5"))
	}
	if issue := task(store, town, "issue:3"); issue.Requeue != 0 || issue.IssueJob == nil || issue.Stage != "implemented" {
		t.Fatalf("second close reset an issue that moved on: %+v", issue)
	}
	if len(gh.comments) != 3 || strings.Contains(gh.comments[2], "starts issue #3 over") || !strings.HasPrefix(gh.comments[2], "#5: ") {
		t.Fatalf("second close commented on the issue or promised a requeue: %v", gh.comments)
	}
}

func TestRequeueCommentIsNotRepeated(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	// An earlier attempt posted the issue comment but its outcome was lost.
	gh.comments = []string{"#3: earlier\n\n" + requeueMarker(&Task{Number: 5, Closes: 1})}
	if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.comments) != 2 || !strings.HasPrefix(gh.comments[1], "#5: ") {
		t.Fatalf("requeue comment repeated: %v", gh.comments)
	}
	if issue := task(store, town, "issue:3"); issue.Requeue != 5 || issue.Stage != "queued" {
		t.Fatalf("issue not requeued: %+v", issue)
	}
}

func TestRefusedStepAfterCloseFinishesTheClose(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*fakeGH)
		note  string
	}{
		{name: "issue comments unreadable", setup: func(gh *fakeGH) {
			gh.issueCommentsError = definiteRejection(errors.New("gh: Resource not accessible by integration (HTTP 403)"))
		}, note: "explain requeue of issue #3 (HTTP 403)"},
		{name: "issue gone", setup: func(gh *fakeGH) {
			gh.commentErrors = map[int]error{3: definiteRejection(errors.New("gh: Not Found (HTTP 404)"))}
		}, note: "explain requeue of issue #3 (HTTP 404)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, town, gh, sup := ownPullTown(t, "auto")
			declineOwnPull(t, store, town)
			test.setup(gh)
			if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[town.ID]); err == nil {
				t.Fatal("a refused step was not reported")
			}
			pr, issue := task(store, town, "pr:5"), task(store, town, "issue:3")
			if pr.Stage != "closed" || !strings.Contains(pr.Detail, test.note) || issue.Stage != "queued" || issue.Requeue != 5 {
				t.Fatalf("refused step stranded the close: pr=%+v issue=%+v", pr, issue)
			}
		})
	}
}

func TestUncertainStepAfterCloseKeepsTheClaim(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	gh.deleteError = errors.New("connection reset")
	ctx := context.Background()
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err == nil {
		t.Fatal("a failed step was not reported")
	}
	if pr := task(store, town, "pr:5"); pr.Stage != "closing" {
		t.Fatalf("uncertain step released the claim: %+v", pr)
	}
	gh.deleteError = nil
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if pr, issue := task(store, town, "pr:5"), task(store, town, "issue:3"); pr.Stage != "closed" || issue.Requeue != 5 || len(gh.closedPulls) != 1 {
		t.Fatalf("retry did not finish the close: pr=%+v issue=%+v closed=%v", pr, issue, gh.closedPulls)
	}
}

func TestBlockedDeclinedOwnPullIsNotClaimed(t *testing.T) {
	store, town, _, _ := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	update(t, store, func(st *State) {
		x := st.Towns[town.ID]
		x.Tasks["pr:5"].Blocked = true
		claimDeclinedPulls(st, x, time.Now())
	})
	if pr := task(store, town, "pr:5"); pr.Stage != "declined" {
		t.Fatalf("blocked PR was claimed: %+v", pr)
	}
}

func TestReopenedOwnPullFailingReviewAgainRequeuesItsIssue(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	ctx := context.Background()
	failReview := func() {
		t.Helper()
		update(t, store, func(st *State) {
			x := st.Towns[town.ID]
			markClosing(st, x, x.Tasks["pr:5"], "The second review still found blocking work.", time.Now())
		})
	}
	update(t, store, func(st *State) {
		x := st.Towns[town.ID]
		applySimplification(st, x, x.Tasks["pr:5"], &Simplification{Mode: "auto", Decision: "admit", Detail: "Proportionate."}, nil, time.Now())
	})
	failReview()
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if issue := task(store, town, "issue:3"); issue.Stage != "queued" || issue.Requeue != 5 {
		t.Fatalf("first close did not requeue: %+v", issue)
	}
	// Issue Bot starts over, which spends the requeue marker.
	root := t.TempDir()
	writeIssueBotState(t, root, store.Snapshot().Towns[town.ID], map[int]*issueJobSummary{3: {Branch: "issue-5", Status: "pending"}})
	workers := &BotWorkers{Root: root, Store: store, jobsQuery: fixtureIssueQuery(root)}
	if err := workers.SyncIssues(store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	// Someone reopens the pull request; reconcile ties the issue to it again
	// and it goes back to Review.
	gh.p.State = "open"
	gh.snapshot.Pulls = []Pull{gh.p}
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if issue, pr := task(store, town, "issue:3"), task(store, town, "pr:5"); issue.Stage != "implemented" || issue.Requeue != 0 || pr.House != Review {
		t.Fatalf("reopen: issue=%+v pr=%s/%s", issue, pr.House, pr.Stage)
	}
	failReview()
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if issue := task(store, town, "issue:3"); len(gh.closedPulls) != 2 || issue.Stage != "queued" || issue.Requeue != 5 {
		t.Fatalf("second close stranded the issue: closed=%v issue=%+v", gh.closedPulls, issue)
	}
	issueComments := []string{}
	for _, c := range gh.comments {
		if strings.HasPrefix(c, "#3: ") {
			issueComments = append(issueComments, c)
		}
	}
	if len(issueComments) != 2 || !strings.Contains(issueComments[1], "close=2") {
		t.Fatalf("second requeue was not explained on the issue: %v", issueComments)
	}
}

func TestRefusedBranchDeleteHoldsTheIssueUntilTheBranchIsGone(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	ctx := context.Background()
	gh.deleteError = definiteRejection(errors.New("gh: Reference update failed (HTTP 422)"))
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err == nil {
		t.Fatal("a refused branch delete was not reported")
	}
	pr, issue := task(store, town, "pr:5"), task(store, town, "issue:3")
	if pr.Stage != "closed" || !pr.BranchKept || !strings.Contains(pr.Detail, "HTTP 422") {
		t.Fatalf("close was not finished with the branch kept: %+v", pr)
	}
	if issue.Stage != "implemented" || issue.Requeue != 0 || !strings.Contains(issue.Detail, "delete issue-5 on GitHub by hand") {
		t.Fatalf("issue was requeued onto a branch Issue Bot cannot push: %+v", issue)
	}
	// The pull request is explained (its comment precedes the delete); the
	// issue is not told it was requeued.
	if len(gh.comments) != 1 || !strings.HasPrefix(gh.comments[0], "#5: ") {
		t.Fatalf("the issue was told it was requeued: %v", gh.comments)
	}
	// Still refused: nothing changes and nothing new is reported.
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if issue := task(store, town, "issue:3"); issue.Stage != "implemented" {
		t.Fatalf("issue requeued while the branch remains: %+v", issue)
	}
	// Someone removes the branch; the next inventory starts the issue over.
	gh.deleteError = nil
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	pr, issue = task(store, town, "pr:5"), task(store, town, "issue:3")
	if pr.BranchKept || issue.Stage != "queued" || issue.Requeue != 5 || len(gh.comments) != 2 || !strings.HasPrefix(gh.comments[1], "#3: ") {
		t.Fatalf("issue did not start over once the branch was gone: pr=%+v issue=%+v comments=%v", pr, issue, gh.comments)
	}
	if len(gh.closedPulls) != 1 {
		t.Fatalf("closed again: %v", gh.closedPulls)
	}
}

func TestClosingAnOldPullKeepsTheBranchANewerOneUses(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "suggest")
	update(t, store, func(st *State) {
		x := st.Towns[town.ID]
		applySimplification(st, x, x.Tasks["pr:5"], &Simplification{Mode: "suggest", Decision: "decline", Detail: "A registry for one caller."}, nil, time.Now())
	})
	if err := sup.Control(town.ID, Hall, "decline", "pr:5"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.deleted) != 1 {
		t.Fatalf("first close did not delete the branch: %v", gh.deleted)
	}
	// Issue Bot's fresh attempt, #7, reuses the issue's branch name. Then
	// someone reopens #5, which Town closes again.
	fresh := pull(7)
	fresh.Head.Ref = pull(5).Head.Ref
	update(t, store, func(st *State) {
		st.Towns[town.ID].Owned[7] = Ownership{Branch: fresh.Head.Ref, Issue: 3}
	})
	gh.p.State = "open"
	gh.snapshot.Pulls = []Pull{gh.p, fresh}
	if err := sup.reconcileNow(ctx, store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 2 || task(store, town, "pr:5").Stage != "closed" {
		t.Fatalf("reopened PR was not closed again: %v", gh.closedPulls)
	}
	if len(gh.deleted) != 1 || task(store, town, "pr:5").BranchKept {
		t.Fatalf("closing #5 deleted the branch #7 uses: %v", gh.deleted)
	}
	if pr := task(store, town, "pr:7"); pr == nil || pr.Stage == "closed" {
		t.Fatalf("newer PR was disturbed: %+v", pr)
	}
}

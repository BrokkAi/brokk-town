package town

import (
	"strings"
	"testing"
	"time"
)

// reopenTown is a town whose first inventory is already the baseline, so each
// later inventory is a transition.
func reopenTown(t *testing.T, mode string) (*State, *Town) {
	t.Helper()
	state := NewState(false)
	cfg := DefaultConfig("acme/orchard")
	cfg.SimplifierMode = mode
	town, err := state.Add(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &state, town
}

func reconcileIssues(t *testing.T, state *State, town *Town, issues ...RemoteIssue) {
	t.Helper()
	remote := inventory()
	remote.Issues = issues
	Reconcile(state, town, remote, time.Now())
	if err := validateState(*state, false); err != nil {
		t.Fatalf("reconcile left invalid state: %v", err)
	}
}

func reconcilePulls(t *testing.T, state *State, town *Town, pulls ...Pull) {
	t.Helper()
	Reconcile(state, town, inventory(pulls...), time.Now())
	if err := validateState(*state, false); err != nil {
		t.Fatalf("reconcile left invalid state: %v", err)
	}
}

// selectedBy names the worker that would pick the task up next, or "" when
// no worker selects it.
func selectedBy(town *Town, task *Task) string {
	if nextIssue(town, time.Now()) == task {
		return "issue"
	}
	if nextTask(town, Simplifier, "simplifying", time.Now()) == task {
		return "simplifier"
	}
	if nextTask(town, Review, "queued", time.Now()) == task {
		return "review"
	}
	if nextJudgment(town, time.Now()) == task {
		return "mayor"
	}
	return ""
}

func TestReopenedIssueReturnsToItsIntake(t *testing.T) {
	for _, test := range []struct {
		name, mode, body, worker string
		house                    Role
		stage                    string
	}{
		{name: "outside issue awaiting simplifier", mode: "auto", worker: "simplifier", house: Simplifier, stage: "simplifying"},
		{name: "simplifier proposal awaiting mayor", mode: "suggest", body: "<!-- simplifier-bot:abc -->", worker: "mayor", house: Hall, stage: "awaiting_mayor"},
		{name: "simplifier proposal past intake", mode: "auto", body: "<!-- simplifier-bot:abc -->", worker: "issue", house: Issue, stage: "queued"},
	} {
		for _, transition := range []string{"closed", "locked"} {
			t.Run(test.name+" "+transition, func(t *testing.T) {
				state, town := reopenTown(t, test.mode)
				open := RemoteIssue{Number: 7, Title: "Arrival", State: "open", Body: test.body}
				away := open
				if transition == "closed" {
					away.State = "closed"
				} else {
					away.Locked = true
				}
				// The first inventory already holds the issue closed or
				// locked: Town has never processed it.
				reconcileIssues(t, state, town, away)
				task := town.Tasks["issue:7"]
				if transition == "locked" && test.house == Hall {
					// A lock cannot bypass or reopen a pending decision.
					if got := selectedBy(town, task); got != "mayor" {
						t.Fatalf("a lock displaced a pending decision: %+v", task)
					}
				} else if task.Stage != transition || selectedBy(town, task) != "" {
					t.Fatalf("issue was not held %s: %+v", transition, task)
				}
				reconcileIssues(t, state, town, open)
				if task.House != test.house || task.Stage != test.stage {
					t.Fatalf("reopened issue is %s/%s, want %s/%s", task.House, task.Stage, test.house, test.stage)
				}
				if got := selectedBy(town, task); got != test.worker {
					t.Fatalf("reopened issue selected by %q, want %q: house=%s stage=%s", got, test.worker, task.House, task.Stage)
				}
			})
		}
	}
}

func TestReopenedIssueKeepsItsDecision(t *testing.T) {
	state, town := reopenTown(t, "auto")
	open := RemoteIssue{Number: 8, Title: "Speculative matrix", State: "open"}
	closed := open
	closed.State = "closed"
	reconcileIssues(t, state, town, open)
	task := town.Tasks["issue:8"]
	applySimplification(state, town, task, &Simplification{Mode: "auto", Decision: "decline", Detail: "No callers."}, nil, time.Now())
	// A lock round trip leaves the decline in place.
	locked := open
	locked.Locked = true
	reconcileIssues(t, state, town, locked)
	reconcileIssues(t, state, town, open)
	if task.House != Hall || task.Stage != "declined" || selectedBy(town, task) != "" {
		t.Fatalf("unlocking overturned Simplifier's decline: %s/%s", task.House, task.Stage)
	}
	// Reopening after the closure is an appeal to the Mayor, not an admission.
	reconcileIssues(t, state, town, closed)
	reconcileIssues(t, state, town, open)
	if selectedBy(town, task) != "mayor" || closableDecline(task) {
		t.Fatalf("reopened declined issue was not appealed to the Mayor: %s/%s", task.House, task.Stage)
	}

	// A Mayoral admission is kept across a closure; only pending is cleared.
	state, town = reopenTown(t, "suggest")
	proposal := RemoteIssue{Number: 9, Title: "Remove the registry", State: "open", Body: "<!-- simplifier-bot:abc -->"}
	reconcileIssues(t, state, town, proposal)
	task = town.Tasks["issue:9"]
	if err := state.decideTask(town, task, "admit", "you", time.Now()); err != nil {
		t.Fatal(err)
	}
	proposal.State = "closed"
	reconcileIssues(t, state, town, proposal)
	proposal.State = "open"
	reconcileIssues(t, state, town, proposal)
	if task.MayoralDecision != "admitted" || selectedBy(town, task) != "issue" {
		t.Fatalf("admitted issue lost its decision or its worker: %+v", task)
	}
}

func TestStrandedIssueIsHealedOnTheNextInventory(t *testing.T) {
	// State written before the fix: a reopen left the issue queued in a house
	// that never selects queued work.
	for _, house := range []Role{Simplifier, Hall} {
		state, town := reopenTown(t, "auto")
		town.Initialized = true
		task := &Task{ID: "issue:7", Kind: "issue", Number: 7, Title: "Arrival", House: house, Stage: "queued", External: true, Updated: time.Now()}
		town.Tasks[task.ID] = task
		reconcileIssues(t, state, town, RemoteIssue{Number: 7, Title: "Arrival", State: "open"})
		if selectedBy(town, task) == "" {
			t.Fatalf("stranded %s issue is still unschedulable: %s/%s", house, task.House, task.Stage)
		}
	}
}

func TestReopenedPullReturnsToItsIntake(t *testing.T) {
	for _, test := range []struct {
		name, mode, worker string
		owned              bool
	}{
		{name: "outside PR awaiting mayor", mode: "suggest", worker: "mayor"},
		{name: "PR awaiting simplifier", mode: "auto", worker: "simplifier"},
		{name: "own PR awaiting simplifier", mode: "suggest", owned: true, worker: "simplifier"},
	} {
		for _, transition := range []string{"closed", "locked", "draft"} {
			t.Run(test.name+" "+transition, func(t *testing.T) {
				state, town := reopenTown(t, test.mode)
				open := pull(5)
				if test.owned {
					town.Owned[5] = Ownership{Branch: open.Head.Ref, Issue: 1}
				}
				reconcilePulls(t, state, town)
				reconcilePulls(t, state, town, open)
				task := town.Tasks["pr:5"]
				if got := selectedBy(town, task); got != test.worker {
					t.Fatalf("arrival selected by %q, want %q", got, test.worker)
				}
				away := open
				switch transition {
				case "closed":
					away.State = "closed"
				case "locked":
					away.Locked = true
				case "draft":
					away.Draft = true
				}
				reconcilePulls(t, state, town, away)
				// Reopened as a draft, then revised and marked ready: neither
				// step may carry the pull request out of intake.
				draft := open
				draft.Draft = true
				reconcilePulls(t, state, town, draft)
				revised := open
				revised.Head.SHA = strings.Repeat("c", 40)
				reconcilePulls(t, state, town, revised)
				if got := selectedBy(town, task); got != test.worker {
					t.Fatalf("after %s and reopen, selected by %q, want %q: house=%s stage=%s decision=%q", transition, got, test.worker, task.House, task.Stage, task.MayoralDecision)
				}
			})
		}
	}
}

func TestReopenedPullKeepsDecisionsAndReviewRouting(t *testing.T) {
	// A Mayoral decline survives the contributor closing and reopening.
	state, town := reopenTown(t, "suggest")
	open := pull(5)
	reconcilePulls(t, state, town)
	reconcilePulls(t, state, town, open)
	task := town.Tasks["pr:5"]
	if err := state.decideTask(town, task, "decline", "you", time.Now()); err != nil {
		t.Fatal(err)
	}
	closed := open
	closed.State = "closed"
	reconcilePulls(t, state, town, closed)
	reconcilePulls(t, state, town, open)
	if task.MayoralDecision != "declined" || task.Stage != "declined" || selectedBy(town, task) != "" {
		t.Fatalf("reopening overturned the Mayor's decline: %+v", task)
	}

	// Admitted work past intake goes back to Review, as before.
	state, town = reopenTown(t, "suggest")
	reconcilePulls(t, state, town)
	reconcilePulls(t, state, town, open)
	task = town.Tasks["pr:5"]
	if err := state.decideTask(town, task, "admit", "you", time.Now()); err != nil {
		t.Fatal(err)
	}
	reconcilePulls(t, state, town, closed)
	reconcilePulls(t, state, town, open)
	if selectedBy(town, task) != "review" {
		t.Fatalf("admitted PR did not return to Review: %s/%s", task.House, task.Stage)
	}

	// Town's own pull request that it closed after review goes to Review if
	// someone reopens it, as before.
	state, town = reopenTown(t, "auto")
	town.Owned[5] = Ownership{Branch: open.Head.Ref, Issue: 1}
	town.Initialized = true
	town.Tasks["pr:5"] = &Task{ID: "pr:5", Kind: "pr", Number: 5, Title: "Change 5", House: Hall, Stage: "closed", Head: headSHA, Base: baseSHA, Updated: time.Now()}
	reconcilePulls(t, state, town, open)
	if task := town.Tasks["pr:5"]; selectedBy(town, task) != "review" {
		t.Fatalf("reopened own PR did not return to Review: %s/%s", task.House, task.Stage)
	}
}

func TestAutoDeclinedPullStaysDeclined(t *testing.T) {
	state, town := reopenTown(t, "auto")
	open := pull(5)
	reconcilePulls(t, state, town)
	reconcilePulls(t, state, town, open)
	task := town.Tasks["pr:5"]
	applySimplification(state, town, task, &Simplification{Mode: "auto", Decision: "decline", Detail: "Out of scope."}, nil, time.Now())
	// A new revision, a draft round trip and a close and reopen all leave
	// Simplifier's decline in place.
	revised := open
	revised.Head.SHA = strings.Repeat("c", 40)
	reconcilePulls(t, state, town, revised)
	draft := revised
	draft.Draft = true
	reconcilePulls(t, state, town, draft)
	reconcilePulls(t, state, town, revised)
	closed := revised
	closed.State = "closed"
	reconcilePulls(t, state, town, closed)
	reconcilePulls(t, state, town, revised)
	if task.House != Hall || task.Stage != "declined" || selectedBy(town, task) != "" {
		t.Fatalf("Simplifier's decline was bypassed: %s/%s", task.House, task.Stage)
	}

	// State from before the decline was final: a revision carried the pull
	// request to Review and a failed review sent it to the Mayor. Closing
	// and reopening it must keep the Mayor's pending decision.
	state, town = reopenTown(t, "auto")
	town.Initialized = true
	task = &Task{ID: "pr:5", Kind: "pr", Number: 5, Title: "Change 5", House: Hall, Stage: "awaiting_mayor", MayoralDecision: "pending", External: true, Retired: true, Head: headSHA, Base: baseSHA, Simplification: &Simplification{Mode: "auto", Decision: "decline", Detail: "Out of scope."}, Updated: time.Now()}
	town.Tasks[task.ID] = task
	closed = open
	closed.State = "closed"
	reconcilePulls(t, state, town, closed)
	reconcilePulls(t, state, town, open)
	if selectedBy(town, task) != "mayor" {
		t.Fatalf("reopened PR lost its pending decision: %s/%s %q", task.House, task.Stage, task.MayoralDecision)
	}
}

func TestRetiredAutoDeclinedPullReturnsToTheMayor(t *testing.T) {
	// State from before the decline was final: a revision carried the pull
	// request to Review, retirePull sent it to the Mayor, and closing it
	// cleared the pending decision.
	state, town := reopenTown(t, "auto")
	town.Initialized = true
	task := &Task{ID: "pr:5", Kind: "pr", Number: 5, Title: "Change 5", House: Hall, Stage: "closed", External: true, Retired: true, Head: headSHA, Base: baseSHA, Simplification: &Simplification{Mode: "auto", Decision: "decline", Detail: "Out of scope."}, Updated: time.Now()}
	town.Tasks[task.ID] = task
	reconcilePulls(t, state, town, pull(5))
	if selectedBy(town, task) != "mayor" || task.Simplification != nil {
		t.Fatalf("retired PR did not return to the Mayor: %s/%s %q", task.House, task.Stage, task.MayoralDecision)
	}
}

func TestMayorCanAdmitWorkSimplifierDeclined(t *testing.T) {
	for _, kind := range []string{"pr", "issue"} {
		state, town := reopenTown(t, "auto")
		task := &Task{ID: kind + ":5", Kind: kind, Number: 5, Title: "Speculative matrix", House: Hall, Stage: "declined", External: true, Simplification: &Simplification{Mode: "auto", Decision: "decline", Detail: "No callers."}, Updated: time.Now()}
		town.Tasks[task.ID] = task
		// Mayor Bot judges only pending decisions, and the overrule is an
		// admission only.
		applyJudgment(state, town, task.ID, &Judgment{Decision: "admit", Reason: "Looks fine."}, nil, time.Now())
		if err := state.decideTask(town, task, "decline", "you", time.Now()); err == nil || task.Stage != "declined" {
			t.Fatalf("%s: a Simplifier decline was re-decided: %+v", kind, task)
		}
		if err := state.decideTask(town, task, "admit", "you", time.Now()); err != nil {
			t.Fatalf("%s: the Mayor could not admit a Simplifier decline: %v", kind, err)
		}
		want := "review"
		if kind == "issue" {
			want = "issue"
		}
		if task.MayoralDecision != "admitted" || task.Simplification != nil || selectedBy(town, task) != want {
			t.Fatalf("%s: admitted work did not reach its worker: %+v", kind, task)
		}
		if err := validateState(*state, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMayorBotSkipsBlockedDecisions(t *testing.T) {
	state, town := reopenTown(t, "suggest")
	open := pull(5)
	reconcilePulls(t, state, town)
	reconcilePulls(t, state, town, open)
	away := open
	away.Base.Ref = "develop"
	reconcilePulls(t, state, town, away)
	if task := nextJudgment(town, time.Now()); task != nil {
		t.Fatalf("Mayor Bot would judge an off-branch PR: %+v", task)
	}
	reconcilePulls(t, state, town, open)
	if task := nextJudgment(town, time.Now()); task == nil || task.ID != "pr:5" {
		t.Fatal("Mayor Bot does not judge the PR once it is back")
	}
}

func TestPullReturningToTheBranchKeepsIntake(t *testing.T) {
	for _, test := range []struct {
		name, mode, worker, stage string
		decline                   bool
	}{
		{name: "awaiting mayor", mode: "suggest", worker: "mayor", stage: "awaiting_mayor"},
		{name: "declined by mayor", mode: "suggest", decline: true, stage: "declined"},
		{name: "awaiting simplifier", mode: "auto", worker: "simplifier", stage: "simplifying"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, town := reopenTown(t, test.mode)
			open := pull(5)
			reconcilePulls(t, state, town)
			reconcilePulls(t, state, town, open)
			task := town.Tasks["pr:5"]
			if test.decline {
				if err := state.decideTask(town, task, "decline", "you", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			away := open
			away.Base.Ref = "develop"
			reconcilePulls(t, state, town, away)
			if !task.Offbranch {
				t.Fatalf("retargeted PR was not held: %+v", task)
			}
			reconcilePulls(t, state, town, open)
			if task.Offbranch || task.Blocked || task.Stage != test.stage || selectedBy(town, task) != test.worker {
				t.Fatalf("returning PR left intake: house=%s stage=%s decision=%q", task.House, task.Stage, task.MayoralDecision)
			}
		})
	}
}

func TestClosedDeclinedPullIsNotOpenWork(t *testing.T) {
	state, town := reopenTown(t, "suggest")
	open := pull(5)
	reconcilePulls(t, state, town)
	reconcilePulls(t, state, town, open)
	task := town.Tasks["pr:5"]
	if err := state.decideTask(town, task, "decline", "you", time.Now()); err != nil {
		t.Fatal(err)
	}
	closed := open
	closed.State = "closed"
	town.Reports = nil
	reconcilePulls(t, state, town, closed)
	if task.Stage != "closed" || task.MayoralDecision != "declined" {
		t.Fatalf("closed declined PR: %+v", task)
	}
	if body := town.Reports[len(town.Reports)-1].Body; !strings.Contains(body, "0 open PRs") {
		t.Fatalf("closed declined PR counted as open: %s", body)
	}
	// A later branch rename does not treat the closed PR as work to retire.
	renamed := closed
	renamed.Base.Ref = "develop"
	reconcilePulls(t, state, town, renamed)
	if task.Offbranch || task.Blocked {
		t.Fatalf("closed declined PR was marked off-branch: %+v", task)
	}
}

func TestResumeWakesTheHouseThatSelectsIt(t *testing.T) {
	state, town := reopenTown(t, "suggest")
	open := RemoteIssue{Number: 9, Title: "Remove the registry", State: "open", Body: "<!-- simplifier-bot:abc -->"}
	closed := open
	closed.State = "closed"
	reconcileIssues(t, state, town, closed)
	town.Workers[Hall].Next = time.Now().Add(time.Hour)
	reconcileIssues(t, state, town, open)
	if selectedBy(town, town.Tasks["issue:9"]) != "mayor" || !town.Workers[Hall].Next.IsZero() {
		t.Fatalf("reopened decision did not wake the Mayor: next=%v", town.Workers[Hall].Next)
	}
}

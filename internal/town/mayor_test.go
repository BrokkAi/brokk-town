package town

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOutsideWorkWaitsForDurableMayoralDecision(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	update(t, store, func(st *State) {
		current := st.Towns[x.ID]
		remote := inventory(pull(8))
		remote.Issues = []RemoteIssue{{Number: 7, Title: "Outside issue", State: "open"}}
		Reconcile(st, current, remote, time.Now())
		for _, id := range []string{"issue:7", "pr:8"} {
			applySimplification(st, current, current.Tasks[id], &Simplification{Mode: "suggest", Decision: "admit", Detail: "Focused work."}, nil, time.Now())
		}
	})
	state := store.Snapshot()
	for _, id := range []string{"issue:7", "pr:8"} {
		task := state.Towns[x.ID].Tasks[id]
		if task == nil || task.Stage != "awaiting_mayor" || task.House != Hall || task.MayoralDecision != "pending" {
			t.Fatalf("%s bypassed Town Hall: %+v", id, task)
		}
	}

	supervisor := NewSupervisor(store, nil, nil)
	if err := supervisor.Control(x.ID, Hall, "admit", "issue:7"); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Control(x.ID, Hall, "decline", "pr:8"); err != nil {
		t.Fatal(err)
	}
	state = store.Snapshot()
	issue := state.Towns[x.ID].Tasks["issue:7"]
	pr := state.Towns[x.ID].Tasks["pr:8"]
	if issue.MayoralDecision != "admitted" || issue.Stage != "queued" || issue.House != Issue {
		t.Fatalf("issue was not admitted to Issue Bot: %+v", issue)
	}
	if pr.MayoralDecision != "declined" || pr.Stage != "declined" || pr.House != Hall {
		t.Fatalf("PR was not durably declined: %+v", pr)
	}
	if err := supervisor.Control(x.ID, Hall, "admit", "pr:8"); err == nil {
		t.Fatal("a final Mayoral decision was overwritten")
	}
}

func TestFeatureProposalsPassThroughSimplifierIntake(t *testing.T) {
	state := NewState(false)
	town, err := state.Add(DefaultConfig("acme/orchard"))
	if err != nil {
		t.Fatal(err)
	}
	remote := inventory()
	remote.Issues = []RemoteIssue{{Number: 3, Title: "Feature proposal", Body: "<!-- feature-bot: proposal -->", State: "open"}}
	Reconcile(&state, town, remote, time.Now())
	task := town.Tasks["issue:3"]
	if task.Stage != "simplifying" || task.House != Simplifier {
		t.Fatalf("unexpected feature route: %+v", task)
	}
}

func TestDeclinedProposalIsClosedAtSourceExactlyOnce(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	proposal := RemoteIssue{Number: 12, Title: "Separate review model settings", Body: "<!-- feature-bot: proposal -->", State: "open"}
	gh := &fakeGH{snapshot: inventory()}
	gh.snapshot.Issues = []RemoteIssue{proposal}
	update(t, store, func(st *State) {
		remote := inventory()
		remote.Issues = []RemoteIssue{proposal}
		Reconcile(st, st.Towns[x.ID], remote, time.Now())
		applySimplification(st, st.Towns[x.ID], st.Towns[x.ID].Tasks["issue:12"], &Simplification{Mode: "suggest", Decision: "admit", Detail: "Needs Mayor review."}, nil, time.Now())
	})
	task := store.Snapshot().Towns[x.ID].Tasks["issue:12"]
	if task.External || task.Stage != "awaiting_mayor" {
		t.Fatalf("feature proposal was not an internal Hall task: %+v", task)
	}

	supervisor := NewSupervisor(store, gh, nil)
	if err := supervisor.Control(x.ID, Hall, "decline", "issue:12"); err != nil {
		t.Fatal(err)
	}
	task = store.Snapshot().Towns[x.ID].Tasks["issue:12"]
	if strings.Contains(task.Detail, "outside work") {
		t.Fatalf("internal proposal was declined as outside work: %q", task.Detail)
	}

	for pass := 0; pass < 2; pass++ {
		if err := supervisor.reconcile(context.Background(), store.Snapshot().Towns[x.ID]); err != nil {
			t.Fatal(err)
		}
	}
	if len(gh.closed) != 1 || gh.closed[0] != 12 {
		t.Fatalf("declined proposal was not closed exactly once: %v", gh.closed)
	}
	task = store.Snapshot().Towns[x.ID].Tasks["issue:12"]
	if task.MayoralDecision != "declined" || task.Stage != "declined" || task.House != Hall {
		t.Fatalf("closing the issue erased the Mayoral decision: %+v", task)
	}
	if !strings.Contains(task.Detail, "closed the issue") {
		t.Fatalf("closure was not reported to the Mayor: %q", task.Detail)
	}
}

func TestDeclinedOutsideIssueIsLeftOpen(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	outside := RemoteIssue{Number: 9, Title: "Outside request", State: "open"}
	gh := &fakeGH{snapshot: inventory()}
	gh.snapshot.Issues = []RemoteIssue{outside}
	update(t, store, func(st *State) {
		remote := inventory()
		remote.Issues = []RemoteIssue{outside}
		Reconcile(st, st.Towns[x.ID], remote, time.Now())
		applySimplification(st, st.Towns[x.ID], st.Towns[x.ID].Tasks["issue:9"], &Simplification{Mode: "suggest", Decision: "decline", Detail: "Low value."}, nil, time.Now())
	})
	supervisor := NewSupervisor(store, gh, nil)
	if err := supervisor.Control(x.ID, Hall, "decline", "issue:9"); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.reconcile(context.Background(), store.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closed) != 0 {
		t.Fatalf("Town closed an outside issue: %v", gh.closed)
	}
}

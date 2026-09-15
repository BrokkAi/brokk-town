package town

import (
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

func TestFeatureProposalsRequireMayorByDefaultAndCanBeExempted(t *testing.T) {
	for _, test := range []struct {
		name   string
		review *bool
		stage  string
		house  Role
	}{
		{name: "default", stage: "awaiting_mayor", house: Hall},
		{name: "disabled", review: boolPointer(false), stage: "queued", house: Issue},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := NewState(false)
			cfg := DefaultConfig("acme/orchard")
			cfg.MayoralFeatureReview = test.review
			town, err := state.Add(cfg)
			if err != nil {
				t.Fatal(err)
			}
			remote := inventory()
			remote.Issues = []RemoteIssue{{Number: 3, Title: "Feature proposal", Body: "<!-- feature-bot: proposal -->", State: "open"}}
			Reconcile(&state, town, remote, time.Now())
			task := town.Tasks["issue:3"]
			if task.Stage != test.stage || task.House != test.house {
				t.Fatalf("unexpected feature route: %+v", task)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }

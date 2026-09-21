package town

import (
	"context"
	"strings"
	"testing"
	"time"
)

// pendingAt places a task at Town Hall awaiting the Mayor.
func pendingAt(t *Town, task *Task) *Task {
	task.House, task.Stage, task.MayoralDecision = Hall, "awaiting_mayor", "pending"
	task.Updated = time.Now()
	t.Tasks[task.ID] = task
	return task
}

func TestAutoMayorFollowsTownAdviceForEveryKindOfArrival(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	update(t, store, func(st *State) {
		current := st.Towns[x.ID]
		pendingAt(current, &Task{ID: "issue:1", Kind: "issue", Number: 1, Title: "Bug Bot finding", Simplification: &Simplification{Mode: "suggest", Decision: "admit", Detail: "Focused work."}})
		pendingAt(current, &Task{ID: "issue:2", Kind: "issue", Number: 2, Title: "Complex proposal", Simplification: &Simplification{Mode: "suggest", Decision: "decline", Detail: "Adds a second registry for one caller."}})
		pendingAt(current, &Task{ID: "issue:3", Kind: "issue", Number: 3, Title: "Simplifier's own proposal"})
		pendingAt(current, &Task{ID: "pr:4", Kind: "pr", Number: 4, Title: "Outside PR without advice", External: true, Head: fixSHA, Base: baseSHA})
		pendingAt(current, &Task{ID: "pr:5", Kind: "pr", Number: 5, Title: "Outside PR needing changes", External: true, Head: fixSHA, Base: baseSHA, Audit: &Audit{Base: baseSHA, Head: fixSHA, Verdict: "changes_needed", Complete: true, Summary: "One open defect.", Checks: []string{"go test ./... passed"}, Findings: []Finding{{ID: "new:a", State: "open", Severity: "P2", Detail: "Nil map write on first use."}}}})
		pendingAt(current, &Task{ID: "pr:6", Kind: "pr", Number: 6, Title: "Outside PR with exhausted reviews", External: true, Head: fixSHA, Base: baseSHA, Retired: true})
		pendingAt(current, &Task{ID: upgradeTaskID(Feature), Kind: "upgrade", Title: "Feature Bot 0.1.2 is available", Upgrade: &BotUpgrade{Role: Feature, From: "0.1.1", To: "0.1.2"}})
	})
	supervisor := NewSupervisor(store, nil, nil)
	if err := supervisor.SetAutoMayor(x.ID, true); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	town := state.Towns[x.ID]
	if !town.Config.AutoMayor || !town.Config.Public().AutoMayor {
		t.Fatal("Auto-Mayor setting was not saved or published")
	}
	want := map[string]struct {
		stage    string
		house    Role
		decision string
	}{
		"issue:1": {"queued", Issue, "admitted"},
		"issue:2": {"declined", Hall, "declined"},
		"issue:3": {"queued", Issue, "admitted"},
		"pr:4":    {"simplifying", Simplifier, ""},
		"pr:5":    {"declined", Hall, "declined"},
		"pr:6":    {"declined", Hall, "declined"},
	}
	for id, w := range want {
		task := town.Tasks[id]
		if task.Stage != w.stage || task.House != w.house || task.MayoralDecision != w.decision {
			t.Fatalf("%s: got stage %s house %s decision %q, want %+v", id, task.Stage, task.House, task.MayoralDecision, w)
		}
	}
	if town.Config.BotVersion(Feature) != "0.1.2" {
		t.Fatalf("bot update was not approved: %+v", town.Config.BotVersions)
	}
	if town.Workers[Simplifier].Next != (time.Time{}) || town.Workers[Issue].Next != (time.Time{}) {
		t.Fatal("houses receiving Auto-Mayor's decisions were not woken")
	}
	decisions, referrals := 0, 0
	for _, e := range state.Events {
		if !strings.HasPrefix(e.Title, "Auto-Mayor ") {
			continue
		}
		if e.Kind == "decision" {
			decisions++
		} else if strings.HasPrefix(e.Title, "Auto-Mayor asked Simplifier Bot") {
			referrals++
		}
	}
	if decisions != 6 || referrals != 1 {
		t.Fatalf("expected six decisions and one referral attributed to Auto-Mayor, got %d and %d", decisions, referrals)
	}
}

func TestAutoMayorAppliesSimplifierAdviceReturnedLater(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	supervisor := NewSupervisor(store, nil, nil)
	if err := supervisor.SetAutoMayor(x.ID, true); err != nil {
		t.Fatal(err)
	}
	update(t, store, func(st *State) {
		current := st.Towns[x.ID]
		Reconcile(st, current, inventory(pull(8)), time.Now())
	})
	supervisor.schedule(context.Background())
	pr := store.Snapshot().Towns[x.ID].Tasks["pr:8"]
	if pr.Stage != "simplifying" || pr.House != Simplifier || pr.MayoralDecision != "" {
		t.Fatalf("outside PR was not sent for Simplifier advice: %+v", pr)
	}
	update(t, store, func(st *State) {
		current := st.Towns[x.ID]
		applySimplification(st, current, current.Tasks["pr:8"], &Simplification{Mode: "suggest", Decision: "admit", Detail: "Focused change."}, nil, time.Now())
	})
	if pr = store.Snapshot().Towns[x.ID].Tasks["pr:8"]; pr.Stage != "awaiting_mayor" {
		t.Fatalf("suggest mode should return the advice to Town Hall: %+v", pr)
	}
	supervisor.schedule(context.Background())
	pr = store.Snapshot().Towns[x.ID].Tasks["pr:8"]
	if pr.Stage != "queued" || pr.House != Review || pr.MayoralDecision != "admitted" {
		t.Fatalf("Auto-Mayor did not apply the returned advice: %+v", pr)
	}
}

func TestAutoMayorOffLeavesDecisionsToThePerson(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	update(t, store, func(st *State) {
		pendingAt(st.Towns[x.ID], &Task{ID: "issue:1", Kind: "issue", Number: 1, Title: "Proposal", Simplification: &Simplification{Mode: "suggest", Decision: "admit", Detail: "Focused work."}})
	})
	supervisor := NewSupervisor(store, nil, nil)
	supervisor.schedule(context.Background())
	if task := store.Snapshot().Towns[x.ID].Tasks["issue:1"]; task.MayoralDecision != "pending" {
		t.Fatalf("a town without Auto-Mayor decided on its own: %+v", task)
	}
	if err := supervisor.SetAutoMayor(x.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.SetAutoMayor(x.ID, false); err != nil {
		t.Fatal(err)
	}
	update(t, store, func(st *State) {
		pendingAt(st.Towns[x.ID], &Task{ID: "issue:2", Kind: "issue", Number: 2, Title: "Later proposal"})
	})
	supervisor.schedule(context.Background())
	state := store.Snapshot()
	if state.Towns[x.ID].Config.AutoMayor || state.Towns[x.ID].Tasks["issue:2"].MayoralDecision != "pending" {
		t.Fatalf("turning Auto-Mayor off did not stop it: %+v", state.Towns[x.ID].Tasks["issue:2"])
	}
	if state.Towns[x.ID].Tasks["issue:1"].MayoralDecision != "admitted" {
		t.Fatal("turning Auto-Mayor on did not judge the waiting arrival")
	}
}

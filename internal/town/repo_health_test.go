package town

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// healthObserver answers one inventory and the branch health report a test
// wants, and records what Town asked for.
type healthObserver struct {
	workerFunc
	gh     *fakeGH
	health *BranchHealth
	seen   []InventoryRequest
}

func (o *healthObserver) Run(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
	return RunResult{}, nil
}

func (o *healthObserver) Observe(ctx context.Context, t *Town, request InventoryRequest, p func(Progress), l *slog.Logger) (RunResult, error) {
	o.seen = append(o.seen, request)
	result, err := o.gh.Observe(ctx, t, request, p, l)
	result.Health = o.health
	return result, err
}

func repoTown(t *testing.T) (*Store, *Town, *fakeGH) {
	t.Helper()
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		st.Towns[x.ID].Initialized = true
		st.Towns[x.ID].Head = baseSHA
	})
	return s, x, newGH(1)
}

func TestFailingBranchIsCommittedAndAnnouncedOnce(t *testing.T) {
	s, x, gh := repoTown(t)
	workers := &healthObserver{gh: gh, health: &BranchHealth{State: "red", Head: baseSHA, Failing: []string{"build"}, Attempts: 1}}
	sup := NewSupervisor(s, gh, workers)
	for pass := 0; pass < 2; pass++ {
		if err := sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]); err != nil {
			t.Fatal(err)
		}
	}
	state := s.Snapshot()
	health := state.Towns[x.ID].Health
	if health == nil || health.State != "red" || health.Failing[0] != "build" || health.At.IsZero() {
		t.Fatalf("the failing branch was not recorded: %+v", health)
	}
	if task := state.Towns[x.ID].Workers[Repo].Task; !strings.Contains(task, "build") {
		t.Fatalf("the repo house does not say what is failing: %q", task)
	}
	announcements := 0
	for _, e := range state.Events {
		if e.From == string(Repo) && e.Kind == "error" {
			announcements++
		}
	}
	if announcements != 1 {
		t.Fatalf("a branch that stays red was announced %d times", announcements)
	}
}

func TestPublishedRepairIsRecordedAsAnOutcome(t *testing.T) {
	s, x, gh := repoTown(t)
	repair := strings.Repeat("c", 40)
	workers := &healthObserver{gh: gh, health: &BranchHealth{State: "repaired", Head: baseSHA, Failing: []string{"build"}, Pushed: repair, Attempts: 1}}
	sup := NewSupervisor(s, gh, workers)
	if err := sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	town := s.Snapshot().Towns[x.ID]
	found := false
	for _, outcome := range town.Outcomes {
		if outcome.Kind == "branch_repair" && outcome.Revision == repair && outcome.Role == Repo {
			found = true
		}
	}
	if !found {
		t.Fatalf("a published repair left no outcome: %+v", town.Outcomes)
	}
}

// A repair agent must never start inside another house's work: the merge path
// confirms a merge by observation alone.
func TestMergeConfirmationAsksForTheInventoryAlone(t *testing.T) {
	s, x, gh := repoTown(t)
	workers := &healthObserver{gh: gh}
	sup := NewSupervisor(s, gh, workers)
	if err := sup.reconcile(context.Background(), s.Snapshot().Towns[x.ID], false, func(Progress) {}, slog.Default()); err != nil {
		t.Fatal(err)
	}
	if len(workers.seen) != 1 || workers.seen[0].Health {
		t.Fatalf("a confirmation read asked for the repair duty: %+v", workers.seen)
	}
}

// The repo house observes whatever the service's agent capacity is, and asks
// for the repair only when a slot is free.
func TestBranchRepairWaitsForAnAgentSlot(t *testing.T) {
	s, x, _ := repoTown(t)
	update(t, s, func(st *State) { st.ServiceConfig.MaxWorkers = 1 })
	sup := NewSupervisor(s, nil, nil)
	sup.mu.Lock()
	sup.running[x.ID+":issue"] = func() {}
	sup.mu.Unlock()
	if sup.claimRepair(x.ID) {
		t.Fatal("a repair claimed an agent slot that was already taken")
	}
	sup.mu.Lock()
	delete(sup.running, x.ID+":issue")
	sup.mu.Unlock()
	if !sup.claimRepair(x.ID) {
		t.Fatal("a repair could not claim the free agent slot")
	}
	sup.mu.Lock()
	sup.running[x.ID+":repo"] = func() {}
	active := sup.activeWorkers()
	sup.mu.Unlock()
	if active != 1 {
		t.Fatalf("a repairing repo house holds %d agent slots, want one", active)
	}
	sup.releaseRepair(x.ID)
	sup.mu.Lock()
	active = sup.activeWorkers()
	sup.mu.Unlock()
	if active != 0 {
		t.Fatalf("an observing repo house holds %d agent slots, want none", active)
	}
}

func TestRepoWorkerResultIsValidated(t *testing.T) {
	inventory := &workerInventory{Branch: "main", DefaultBranch: "main", Head: baseSHA}
	cases := []struct {
		name   string
		result workerResult
		err    error
		want   string
	}{
		{"missing inventory", workerResult{}, nil, "omitted its inventory"},
		{"unusable head", workerResult{Inventory: &workerInventory{Branch: "main", Head: "main"}}, nil, "usable branch head"},
		{"invalid health", workerResult{Inventory: inventory, Health: &BranchHealth{State: "fine"}}, nil, "invalid branch health"},
		{"another role's result", workerResult{Inventory: inventory, Review: &workerReviewResult{}}, nil, "unexpected typed result"},
		{"valid", workerResult{Inventory: inventory, Health: &BranchHealth{State: "green", Head: baseSHA}}, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := repoResult(c.result, c.err)
			if c.want == "" {
				if err != nil {
					t.Fatalf("valid result rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want one about %q", err, c.want)
			}
		})
	}
}

// A run that failed still carries what it observed: Town's view of the
// repository must not depend on the health duty that follows it.
func TestFailedRepoRunStillCarriesItsInventory(t *testing.T) {
	result, err := repoResult(workerResult{Inventory: &workerInventory{Branch: "main", Head: baseSHA}}, errors.New("read branch checks: gh failed"))
	if err == nil {
		t.Fatal("a failed run must be reported as one")
	}
	if result.Inventory == nil || result.Inventory.Head != baseSHA {
		t.Fatalf("the inventory was lost with the error: %+v", result.Inventory)
	}
}

func TestBranchHealthValidation(t *testing.T) {
	valid := []*BranchHealth{nil, {State: "green"}, {State: "repaired", Head: baseSHA, Pushed: headSHA}}
	for _, h := range valid {
		if !ValidBranchHealth(h) {
			t.Fatalf("rejected valid health: %+v", h)
		}
	}
	invalid := []*BranchHealth{{State: ""}, {State: "shipped"}, {State: "red", Head: "main"}, {State: "repaired", Pushed: "abc"}}
	for _, h := range invalid {
		if ValidBranchHealth(h) {
			t.Fatalf("accepted invalid health: %+v", h)
		}
	}
	if (&BranchHealth{State: "red"}).Healthy() || !(&BranchHealth{State: "unreported"}).Healthy() {
		t.Fatal("branch health does not classify what is owed")
	}
}

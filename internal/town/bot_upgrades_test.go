package town

import (
	"context"
	"errors"
	"testing"
	"time"
)

func offer(t *testing.T, store *Store, versions map[Role]string, now time.Time) {
	t.Helper()
	update(t, store, func(st *State) { st.OfferBotUpgrades(versions, now) })
}

func upgradeTask(t *testing.T, store *Store, role Role) *Task {
	t.Helper()
	return store.Snapshot().Towns["acme/orchard"].Tasks[upgradeTaskID(role)]
}

func TestNewerBotVersionWaitsForMayorAndApprovalPinsIt(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	current := workerDefaultVersions[Feature]
	offer(t, store, map[Role]string{Feature: "9.9.9", Issue: workerDefaultVersions[Issue]}, now)

	task := upgradeTask(t, store, Feature)
	if task == nil || task.Kind != "upgrade" || task.House != Hall || task.Stage != "awaiting_mayor" || task.MayoralDecision != "pending" {
		t.Fatalf("newer feature-bot version did not wait for the Mayor: %+v", task)
	}
	if task.Upgrade.From != current || task.Upgrade.To != "9.9.9" {
		t.Fatalf("offer records the wrong versions: %+v", task.Upgrade)
	}
	if upgradeTask(t, store, Issue) != nil {
		t.Fatal("an up-to-date bot received an offer")
	}
	if got := store.Snapshot().Towns[x.ID].Config.BotVersion(Feature); got != current {
		t.Fatalf("offer changed the pin before a decision: %s", got)
	}
	// Repeated checks refresh, never duplicate or re-announce, a pending offer.
	events := len(store.Snapshot().Events)
	offer(t, store, map[Role]string{Feature: "9.9.9"}, now.Add(time.Hour))
	if len(store.Snapshot().Events) != events || upgradeTask(t, store, Feature).MayoralDecision != "pending" {
		t.Fatal("a repeated registry check disturbed the pending offer")
	}

	supervisor := NewSupervisor(store, nil, nil)
	supervisor.now = func() time.Time { return now.Add(2 * time.Hour) }
	if err := supervisor.Control(x.ID, Hall, "admit", upgradeTaskID(Feature)); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if got := state.Towns[x.ID].Config.BotVersions[Feature]; got != "9.9.9" {
		t.Fatalf("approval did not pin the new version: %q", got)
	}
	if state.Towns[x.ID].Tasks[upgradeTaskID(Feature)] != nil {
		t.Fatal("an applied offer stayed in Town Hall")
	}
	if got := state.Towns[x.ID].Config.Public().BotVersions[Feature]; got != "9.9.9" {
		t.Fatalf("public configuration does not show the new pin: %q", got)
	}
	// The registry now agrees with the pin; nothing new is offered.
	offer(t, store, map[Role]string{Feature: "9.9.9"}, now.Add(3*time.Hour))
	if upgradeTask(t, store, Feature) != nil {
		t.Fatal("an offer was created for the pinned version")
	}
}

func TestDeclinedBotUpgradeStaysDeclinedUntilANewerRelease(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	offer(t, store, map[Role]string{Review: "9.0.0"}, now)
	supervisor := NewSupervisor(store, nil, nil)
	supervisor.now = func() time.Time { return now }
	if err := supervisor.Control(x.ID, Hall, "decline", upgradeTaskID(Review)); err != nil {
		t.Fatal(err)
	}
	task := upgradeTask(t, store, Review)
	if task.Stage != "declined" || task.MayoralDecision != "declined" {
		t.Fatalf("decline is not durable: %+v", task)
	}
	if err := supervisor.Control(x.ID, Hall, "admit", upgradeTaskID(Review)); err == nil {
		t.Fatal("a declined upgrade was approved afterwards")
	}
	offer(t, store, map[Role]string{Review: "9.0.0"}, now.Add(24*time.Hour))
	if task = upgradeTask(t, store, Review); task.Stage != "declined" {
		t.Fatalf("the declined version was offered again: %+v", task)
	}
	if got := store.Snapshot().Towns[x.ID].Config.BotVersion(Review); got != workerDefaultVersions[Review] {
		t.Fatalf("decline changed the pin: %s", got)
	}
	offer(t, store, map[Role]string{Review: "9.1.0"}, now.Add(48*time.Hour))
	if task = upgradeTask(t, store, Review); task.Stage != "awaiting_mayor" || task.Upgrade.To != "9.1.0" {
		t.Fatalf("a newer release did not reopen the decision: %+v", task)
	}
}

func TestDelayedBotUpgradeReturnsAfterADay(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	offer(t, store, map[Role]string{Issue: "9.0.0"}, now)
	supervisor := NewSupervisor(store, nil, nil)
	supervisor.now = func() time.Time { return now }
	if err := supervisor.Control(x.ID, Hall, "delay", "issue:1"); err == nil {
		t.Fatal("delay applied to a task that is not a bot upgrade")
	}
	if err := supervisor.Control(x.ID, Hall, "delay", upgradeTaskID(Issue)); err != nil {
		t.Fatal(err)
	}
	task := upgradeTask(t, store, Issue)
	if task.Stage != "delayed" || task.MayoralDecision != "" || !task.RetryAt.Equal(now.Add(BotUpgradeDelay)) {
		t.Fatalf("delay did not schedule the next ask: %+v", task)
	}
	if err := supervisor.Control(x.ID, Hall, "admit", upgradeTaskID(Issue)); err == nil {
		t.Fatal("a delayed upgrade accepted a decision before its day passed")
	}
	// Neither the scheduler nor a registry check revives the offer early.
	supervisor.now = func() time.Time { return now.Add(23 * time.Hour) }
	supervisor.reviveDelayedBotUpgrades()
	offer(t, store, map[Role]string{Issue: "9.0.0"}, now.Add(23*time.Hour))
	if upgradeTask(t, store, Issue).Stage != "delayed" {
		t.Fatal("the delayed offer returned early")
	}
	supervisor.now = func() time.Time { return now.Add(BotUpgradeDelay) }
	supervisor.reviveDelayedBotUpgrades()
	task = upgradeTask(t, store, Issue)
	if task.Stage != "awaiting_mayor" || task.MayoralDecision != "pending" || !task.RetryAt.IsZero() {
		t.Fatalf("the delayed offer did not return to Town Hall: %+v", task)
	}
	if err := supervisor.Control(x.ID, Hall, "admit", upgradeTaskID(Issue)); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Towns[x.ID].Config.BotVersions[Issue]; got != "9.0.0" {
		t.Fatalf("approval after the delay did not pin the version: %q", got)
	}
}

func TestAutoUpdateAppliesNewBotVersionsWithoutADecision(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	supervisor := NewSupervisor(store, nil, nil)
	supervisor.now = func() time.Time { return now }
	on := true
	if err := supervisor.SettingsForRoleAndPolicy(x.ID, "", AgentSettings{}, nil, nil, nil, &on, nil); err != nil {
		t.Fatal(err)
	}
	if !store.Snapshot().Towns[x.ID].Config.Public().AutoUpdateBots {
		t.Fatal("automatic updates are not visible in the public configuration")
	}
	offer(t, store, map[Role]string{Bug: "9.0.0"}, now)
	state := store.Snapshot()
	if got := state.Towns[x.ID].Config.BotVersions[Bug]; got != "9.0.0" {
		t.Fatalf("auto-update did not pin the new version: %q", got)
	}
	if state.Towns[x.ID].Tasks[upgradeTaskID(Bug)] != nil {
		t.Fatal("auto-update left a decision in Town Hall")
	}
	found := false
	for _, e := range state.Events {
		if e.Kind == "upgrade" && e.Cargo == upgradeTaskID(Bug) {
			found = true
		}
	}
	if !found {
		t.Fatal("auto-update was not recorded as an event")
	}
}

func TestEnablingAutoUpdateAppliesOffersAlreadyWaiting(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	offer(t, store, map[Role]string{Release: "9.0.0"}, now)
	supervisor := NewSupervisor(store, nil, nil)
	supervisor.now = func() time.Time { return now }
	if err := supervisor.Control(x.ID, Hall, "delay", upgradeTaskID(Release)); err != nil {
		t.Fatal(err)
	}
	on := true
	if err := supervisor.SettingsForRoleAndPolicy(x.ID, "", AgentSettings{}, nil, nil, nil, &on, nil); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if got := state.Towns[x.ID].Config.BotVersions[Release]; got != "9.0.0" {
		t.Fatalf("enabling auto-update did not apply the waiting offer: %q", got)
	}
	if state.Towns[x.ID].Tasks[upgradeTaskID(Release)] != nil {
		t.Fatal("the applied offer stayed in Town Hall")
	}
}

func TestExplicitPinWithdrawsAnOfferItCaughtUpWith(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	offer(t, store, map[Role]string{Feature: "9.0.0"}, now)
	supervisor := NewSupervisor(store, nil, nil)
	supervisor.now = func() time.Time { return now }
	pin := "9.0.0"
	if err := supervisor.SettingsForRoleAndPolicy(x.ID, Feature, AgentSettings{}, nil, nil, &pin, nil, nil); err != nil {
		t.Fatal(err)
	}
	if upgradeTask(t, store, Feature) != nil {
		t.Fatal("a pin equal to the offered version left the offer waiting")
	}
}

func TestRegistryChecksAreQuietOnFailureAndSkipDemoTowns(t *testing.T) {
	store := testStore(t, false)
	addTown(t, store)
	supervisor := NewSupervisor(store, nil, nil)
	supervisor.BotVersions = func(context.Context) (map[Role]string, error) { return nil, errors.New("registry unavailable") }
	supervisor.checkBotUpgrades(context.Background())
	if upgradeTask(t, store, Feature) != nil {
		t.Fatal("a failed registry check produced an offer")
	}
	select {
	case err := <-supervisor.fatal:
		t.Fatalf("registry failure was fatal: %v", err)
	default:
	}
	supervisor.BotVersions = func(context.Context) (map[Role]string, error) { return map[Role]string{Feature: "9.0.0"}, nil }
	supervisor.checkBotUpgrades(context.Background())
	if upgradeTask(t, store, Feature) == nil {
		t.Fatal("a successful registry check produced no offer")
	}

	demo := testStore(t, true)
	update(t, demo, func(st *State) {
		if _, err := st.Add(DefaultConfig("acme/orchard")); err != nil {
			t.Fatal(err)
		}
	})
	demoSupervisor := NewSupervisor(demo, nil, nil)
	demoSupervisor.BotVersions = supervisor.BotVersions
	demoSupervisor.checkBotUpgrades(context.Background())
	if demo.Snapshot().Towns["acme/orchard"].Tasks[upgradeTaskID(Feature)] != nil {
		t.Fatal("a demo town received a real bot upgrade offer")
	}
}

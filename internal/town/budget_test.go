package town

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func budgetTown(t *testing.T, s *Store, repo string, budget *Budget) *Town {
	t.Helper()
	var out *Town
	if err := s.Update(func(st *State) error {
		cfg := DefaultConfig(repo)
		cfg.Budget = budget
		x, err := st.Add(cfg)
		if err != nil {
			return err
		}
		x.Initialized = true
		x.Workers[Repo].Enabled = false
		x.Workers[Issue].Enabled = true
		out = x
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// charge bills one finished attempt the way execute does.
func charge(t *testing.T, s *Store, id string, role Role, elapsedMS *int64, at time.Time) {
	t.Helper()
	if err := s.Update(func(st *State) error {
		x := st.Towns[id]
		record := OutcomeRecord{
			ID: string(role) + ":" + at.Format(time.RFC3339Nano), At: at, Class: "attempt", Kind: "worker_attempt",
			Status: "attempted", Role: role, ElapsedMS: elapsedMS, Agent: OccupiesAgentSlot(role),
		}
		x.RecordOutcome(record)
		x.chargeBudget(record, at)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func ms(v int64) *int64 { return &v }

func TestBudgetValidationRejectsUnenforceableLimits(t *testing.T) {
	if err := (&Budget{Period: "fortnight", MaxAttempts: 5}).Validate(); err == nil {
		t.Fatal("accepted an unknown accounting period")
	}
	if err := (&Budget{Period: "day"}).Validate(); err == nil {
		t.Fatal("accepted a budget with no enforceable limit")
	} else if !strings.Contains(err.Error(), "no bundled agent harness reports usage") {
		t.Fatalf("empty budget error does not state the telemetry limitation: %v", err)
	}
	if err := (&Budget{Period: "day", MaxAttempts: -1}).Validate(); err == nil {
		t.Fatal("accepted a negative attempt limit")
	}
	if err := (&Budget{Period: "day", MaxAttempts: MaximumBudgetAttempts + 1}).Validate(); err == nil {
		t.Fatal("accepted an attempt limit above the maximum")
	}
	if err := (*Budget)(nil).Validate(); err != nil {
		t.Fatalf("an absent budget must stay valid: %v", err)
	}
	for _, period := range []string{"day", "week", "month"} {
		if err := (&Budget{Period: period, MaxAgentMinutes: 30}).Validate(); err != nil {
			t.Fatalf("%s budget rejected: %v", period, err)
		}
	}
}

func TestBudgetWindowsCoverTheirPeriod(t *testing.T) {
	// A Wednesday, so the weekly window has to walk back to Monday.
	now := time.Date(2026, 9, 23, 14, 30, 0, 0, time.Local)
	for _, tc := range []struct {
		period string
		from   time.Time
		to     time.Time
	}{
		{"day", time.Date(2026, 9, 23, 0, 0, 0, 0, time.Local), time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)},
		{"week", time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local), time.Date(2026, 9, 28, 0, 0, 0, 0, time.Local)},
		{"month", time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)},
	} {
		from, to := BudgetWindow(tc.period, now)
		if !from.Equal(tc.from) || !to.Equal(tc.to) {
			t.Fatalf("%s window = %s..%s, want %s..%s", tc.period, from, to, tc.from, tc.to)
		}
		if from.After(now) || !to.After(now) {
			t.Fatalf("%s window does not contain now", tc.period)
		}
	}
	// An unknown period falls back to the day window rather than an empty one.
	from, to := BudgetWindow("", now)
	if !from.Equal(time.Date(2026, 9, 23, 0, 0, 0, 0, time.Local)) || !to.After(now) {
		t.Fatalf("fallback window = %s..%s", from, to)
	}
}

func TestBudgetAccumulatesAttemptsAndAgentTime(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/spend", &Budget{Period: "day", MaxAttempts: 3})
	now := time.Now()
	charge(t, s, x.ID, Issue, ms(90_000), now)
	charge(t, s, x.ID, Review, ms(30_000), now)
	state := s.Snapshot().Towns[x.ID].BudgetState(now)
	if state.Attempts != 2 || state.AgentSeconds != 120 {
		t.Fatalf("ledger = %d attempts / %ds, want 2 / 120", state.Attempts, state.AgentSeconds)
	}
	if state.Exhausted {
		t.Fatal("budget exhausted below its limit")
	}
	if state.Usage != nil || state.CostUSD != nil {
		t.Fatal("usage and cost must stay unavailable while no agent reports them")
	}
}

func TestBudgetDoesNotChargeInventoryAttempts(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/inventory", &Budget{Period: "day", MaxAttempts: 1})
	now := time.Now()
	// A repo attempt that never repaired holds no agent slot.
	charge(t, s, x.ID, Repo, ms(5_000), now)
	state := s.Snapshot().Towns[x.ID].BudgetState(now)
	if state.Attempts != 0 || state.AgentSeconds != 0 {
		t.Fatalf("inventory was billed: %d attempts / %ds", state.Attempts, state.AgentSeconds)
	}
	if state.Exhausted {
		t.Fatal("an inventory-only town exhausted its agent budget")
	}
}

func TestBudgetCountsMissingElapsedTimeAsUnmeasured(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/untimed", &Budget{Period: "day", MaxAgentMinutes: 10})
	now := time.Now()
	charge(t, s, x.ID, Issue, nil, now)
	charge(t, s, x.ID, Issue, ms(60_000), now)
	state := s.Snapshot().Towns[x.ID].BudgetState(now)
	if state.Attempts != 2 || state.Untimed != 1 {
		t.Fatalf("state = %d attempts / %d untimed, want 2 / 1", state.Attempts, state.Untimed)
	}
	if state.AgentSeconds != 60 {
		t.Fatalf("an untimed attempt contributed time: %ds", state.AgentSeconds)
	}
}

func TestBudgetExhaustionExplainsItselfAndNamesTheReset(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/limit", &Budget{Period: "day", MaxAttempts: 2})
	now := time.Now()
	charge(t, s, x.ID, Issue, ms(1_000), now)
	charge(t, s, x.ID, Issue, nil, now)
	state := s.Snapshot().Towns[x.ID].BudgetState(now)
	if !state.Exhausted {
		t.Fatal("reaching the attempt limit did not exhaust the budget")
	}
	for _, want := range []string{"2 of 2 agent attempts", "resumes at", "reported no elapsed time"} {
		if !strings.Contains(state.Reason, want) {
			t.Fatalf("reason %q does not mention %q", state.Reason, want)
		}
	}
	if state.Advice == "" || !strings.Contains(state.Advice, "cannot cap token") {
		t.Fatalf("budget state does not state the telemetry limitation: %q", state.Advice)
	}
}

func TestBudgetMinutesExhaustSeparatelyFromAttempts(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/minutes", &Budget{Period: "day", MaxAgentMinutes: 2})
	now := time.Now()
	charge(t, s, x.ID, Issue, ms(125_000), now)
	state := s.Snapshot().Towns[x.ID].BudgetState(now)
	if !state.Exhausted || !strings.Contains(state.Reason, "agent minutes") {
		t.Fatalf("minute ceiling did not stop work: %+v", state)
	}
}

func TestBudgetResetsWhenThePeriodRollsOver(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/rollover", &Budget{Period: "day", MaxAttempts: 1})
	now := time.Now()
	charge(t, s, x.ID, Issue, ms(1_000), now)
	if !s.Snapshot().Towns[x.ID].BudgetState(now).Exhausted {
		t.Fatal("budget was not spent")
	}
	tomorrow := now.AddDate(0, 0, 1)
	next := s.Snapshot().Towns[x.ID].BudgetState(tomorrow)
	if next.Exhausted || next.Attempts != 0 {
		t.Fatalf("the new period inherited spend: %+v", next)
	}
	// The stale ledger is replaced, not added to, once work resumes.
	charge(t, s, x.ID, Issue, ms(2_000), tomorrow)
	after := s.Snapshot().Towns[x.ID].BudgetState(tomorrow)
	if after.Attempts != 1 || after.AgentSeconds != 2 {
		t.Fatalf("rolled ledger = %d attempts / %ds, want 1 / 2", after.Attempts, after.AgentSeconds)
	}
}

func TestBudgetSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	x := budgetTown(t, s, "acme/restart", &Budget{Period: "day", MaxAttempts: 2})
	now := time.Now()
	charge(t, s, x.ID, Issue, ms(45_000), now)
	s.Close()

	reopened, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state := reopened.Snapshot().Towns[x.ID].BudgetState(now)
	if state.Attempts != 1 || state.AgentSeconds != 45 {
		t.Fatalf("restart lost the period total: %d attempts / %ds", state.Attempts, state.AgentSeconds)
	}
}

func TestBudgetHoldsAgentDispatchButKeepsTheInventoryRunning(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/gate", &Budget{Period: "day", MaxAttempts: 1})
	if err := s.Update(func(st *State) error {
		st.Towns[x.ID].Workers[Repo].Enabled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	charge(t, s, x.ID, Issue, ms(1_000), now)
	if eligible, _ := s.dispatchEligibility(x.ID, Issue, now); eligible {
		t.Fatal("an exhausted budget still dispatched agent work")
	}
	if eligible, _ := s.dispatchEligibility(x.ID, Repo, now); !eligible {
		t.Fatal("an exhausted budget stopped the repository inventory")
	}
	if !s.budgetExhausted(x.ID, now) {
		t.Fatal("budgetExhausted disagreed with the dispatch gate")
	}
	// Tomorrow's window releases the house again without operator action.
	if eligible, _ := s.dispatchEligibility(x.ID, Issue, now.AddDate(0, 0, 1)); !eligible {
		t.Fatal("the next period did not resume agent work")
	}
}

func TestBudgetIsPerTownAcrossSharedWorkerSlots(t *testing.T) {
	s := testStore(t, false)
	spent := budgetTown(t, s, "acme/spent", &Budget{Period: "day", MaxAttempts: 1})
	free := budgetTown(t, s, "acme/free", &Budget{Period: "day", MaxAttempts: 5})
	unlimited := budgetTown(t, s, "acme/unlimited", nil)
	now := time.Now()
	charge(t, s, spent.ID, Issue, ms(1_000), now)
	charge(t, s, free.ID, Issue, ms(1_000), now)
	charge(t, s, unlimited.ID, Issue, ms(1_000), now)
	if eligible, _ := s.dispatchEligibility(spent.ID, Issue, now); eligible {
		t.Fatal("the spent town kept dispatching")
	}
	for _, id := range []string{free.ID, unlimited.ID} {
		if eligible, _ := s.dispatchEligibility(id, Issue, now); !eligible {
			t.Fatalf("%s was held by another town's budget", id)
		}
	}
	state := s.Snapshot().Towns[unlimited.ID].BudgetState(now)
	if state.Configured || state.Exhausted {
		t.Fatalf("a town with no budget reads as limited: %+v", state)
	}
	if state.Attempts != 1 {
		t.Fatalf("a town with no budget stopped reporting spend: %+v", state)
	}
}

func TestBudgetRunningWorkFinishesAfterTheLimitIsReached(t *testing.T) {
	s := testStore(t, false)
	addCapacityTown(t, s, "acme/running", Issue)
	if err := s.Update(func(st *State) error {
		st.Towns["acme/running"].Config.Budget = &Budget{Period: "day", MaxAttempts: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w := newCapacityWorker(t, "acme/running:issue")
	sup := NewSupervisor(s, nil, w)
	ctx := context.Background()
	sup.schedule(ctx)
	started := waitCapacityStarts(t, w.started, 1)[0]
	// Spending the budget mid-run must not cancel the attempt in flight.
	charge(t, s, "acme/running", Issue, ms(1_000), time.Now())
	sup.schedule(ctx)
	select {
	case extra := <-w.started:
		t.Fatalf("dispatched past an exhausted budget: %s", extra)
	case <-time.After(100 * time.Millisecond):
	}
	close(w.release[started])
	sup.wg.Wait()
	if s.Snapshot().Towns["acme/running"].Workers[Issue].Status == "failed" {
		t.Fatal("the running attempt was failed by the budget instead of finishing")
	}
}

func TestBudgetReachesClientsAndNeverShowsMissingCostAsZero(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/public", &Budget{Period: "week", MaxAgentMinutes: 60})
	now := time.Now()
	charge(t, s, x.ID, Issue, ms(120_000), now)
	raw, err := json.Marshal(s.Snapshot().PublicAt(now))
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		Towns map[string]struct {
			Budget BudgetState `json:"budget"`
			Config struct {
				Budget *Budget `json:"budget"`
			} `json:"config"`
		} `json:"towns"`
	}
	if err := json.Unmarshal(raw, &public); err != nil {
		t.Fatal(err)
	}
	got := public.Towns[x.ID].Budget
	if got.Period != "week" || got.Attempts != 1 || got.AgentSeconds != 120 || got.MaxAgentMinutes != 60 {
		t.Fatalf("public budget = %+v", got)
	}
	if !got.Configured {
		t.Fatal("public budget does not report that a ceiling is set")
	}
	if public.Towns[x.ID].Config.Budget == nil {
		t.Fatal("public config dropped the configured budget")
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	budget := fields["towns"].(map[string]any)[x.ID].(map[string]any)["budget"].(map[string]any)
	for _, key := range []string{"cost_usd", "usage"} {
		value, present := budget[key]
		if !present {
			t.Fatalf("public budget omits %q; clients cannot tell unavailable from zero", key)
		}
		if value != nil {
			t.Fatalf("public budget reported %q as %v without any agent telemetry", key, value)
		}
	}
}

func TestBudgetSettingsRoundTripAndClear(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/settings", nil)
	sup := NewSupervisor(s, nil, nil)
	set := &Budget{Period: "month", MaxAttempts: 200}
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{Budget: &BudgetEdit{Budget: set}}); err != nil {
		t.Fatal(err)
	}
	saved := s.Snapshot().Towns[x.ID].Config.Budget
	if saved == nil || saved.Period != "month" || saved.MaxAttempts != 200 {
		t.Fatalf("budget was not saved: %+v", saved)
	}
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{Budget: &BudgetEdit{Budget: &Budget{Period: "day"}}}); err == nil {
		t.Fatal("saved a budget with no enforceable limit")
	}
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{Budget: &BudgetEdit{}}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Towns[x.ID].Config.Budget != nil {
		t.Fatal("clearing the budget left one saved")
	}
	// An unrelated edit leaves the budget alone.
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{Budget: &BudgetEdit{Budget: set}}); err != nil {
		t.Fatal(err)
	}
	manual := "manual"
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{MergePolicy: &manual}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Towns[x.ID].Config.Budget == nil {
		t.Fatal("a merge-policy edit discarded the budget")
	}
}

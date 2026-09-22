package town

import (
	"errors"
	"fmt"
	"time"
)

// Budget bounds the agent automation one town may start inside a repeating
// accounting period.
//
// Town enforces the two quantities it measures itself: the number of attempts
// that started an agent, and the wall-clock time those attempts ran. There is
// deliberately no token or monetary ceiling. No released acp-go runner reports
// usage back to a bot -- runner.Execute hands back the agent's final text and
// nothing else -- so every bundled bot returns a result with no usage and no
// cost, and a spend cap here would be enforced against numbers Town never
// receives. BudgetState carries the measured attempt and time totals, and
// leaves Usage and CostUSD null rather than presenting absent telemetry as
// zero.
type Budget struct {
	Period          string `json:"period"`
	MaxAttempts     int    `json:"max_attempts,omitempty"`
	MaxAgentMinutes int    `json:"max_agent_minutes,omitempty"`
}

// DefaultBudgetPeriod is the window Town accounts in when no budget is set. The
// totals are still reported; nothing is withheld.
const DefaultBudgetPeriod = "day"

const (
	MaximumBudgetAttempts     = 100000
	MaximumBudgetAgentMinutes = 1000000
)

// BudgetLimitAdvice explains what Town can and cannot cap. Operators meet it
// wherever a cost ceiling might be expected.
const BudgetLimitAdvice = "Town cannot cap token or dollar spend: no bundled agent harness reports usage back through the worker protocol. Limit agent attempts or agent minutes instead."

func ValidBudgetPeriod(p string) bool { return p == "day" || p == "week" || p == "month" }

func (b *Budget) Validate() error {
	if b == nil {
		return nil
	}
	if !ValidBudgetPeriod(b.Period) {
		return errors.New("budget period must be day, week or month")
	}
	if b.MaxAttempts < 0 || b.MaxAgentMinutes < 0 {
		return errors.New("budget limits cannot be negative")
	}
	if b.MaxAttempts == 0 && b.MaxAgentMinutes == 0 {
		return errors.New("a budget needs max_attempts or max_agent_minutes. " + BudgetLimitAdvice)
	}
	if b.MaxAttempts > MaximumBudgetAttempts {
		return fmt.Errorf("max_attempts must be at most %d", MaximumBudgetAttempts)
	}
	if b.MaxAgentMinutes > MaximumBudgetAgentMinutes {
		return fmt.Errorf("max_agent_minutes must be at most %d", MaximumBudgetAgentMinutes)
	}
	return nil
}

// BudgetWindow is the accounting period containing now. Windows are local wall
// clock, because that is the calendar the operator reads their bill against:
// a day starts at local midnight, a week on Monday, a month on the first.
// AddDate rather than a fixed duration keeps the boundary correct across a
// daylight-saving change.
func BudgetWindow(period string, now time.Time) (time.Time, time.Time) {
	now = now.Local()
	year, month, day := now.Date()
	midnight := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
	switch period {
	case "week":
		start := midnight.AddDate(0, 0, -((int(midnight.Weekday()) + 6) % 7))
		return start, start.AddDate(0, 0, 7)
	case "month":
		start := time.Date(year, month, 1, 0, 0, 0, 0, now.Location())
		return start, start.AddDate(0, 1, 0)
	default:
		return midnight, midnight.AddDate(0, 0, 1)
	}
}

// BudgetLedger accumulates one town's measured spend for the current period.
// It is durable state rather than a scan of the outcome history: the totals
// survive restarts, and reading them costs nothing on the snapshot path.
type BudgetLedger struct {
	Period   string        `json:"period"`
	From     time.Time     `json:"from"`
	Attempts int           `json:"attempts"`
	AgentMS  int64         `json:"agent_ms"`
	Untimed  int           `json:"untimed"`
	Usage    *OutcomeUsage `json:"usage,omitempty"`
	CostUSD  *float64      `json:"cost_usd,omitempty"`
}

// budgetPeriod is the window this town accounts in, configured or default.
func (t *Town) budgetPeriod() string {
	if t.Config.Budget != nil && ValidBudgetPeriod(t.Config.Budget.Period) {
		return t.Config.Budget.Period
	}
	return DefaultBudgetPeriod
}

// rollBudget returns the ledger covering now, resetting it when the period
// rolled over or the operator changed the period. Caller holds the state write.
func (t *Town) rollBudget(now time.Time) *BudgetLedger {
	period := t.budgetPeriod()
	from, _ := BudgetWindow(period, now)
	if t.Budget == nil || t.Budget.Period != period || !t.Budget.From.Equal(from) {
		t.Budget = &BudgetLedger{Period: period, From: from}
	}
	return t.Budget
}

// chargeBudget adds one finished attempt to the current period. Only attempts
// that held an agent slot are charged: a repository inventory reads GitHub
// without starting an agent, so it costs nothing to bill. An attempt whose
// elapsed time was not measured is counted in Untimed, never as zero time.
func (t *Town) chargeBudget(record OutcomeRecord, now time.Time) {
	if !record.Agent {
		return
	}
	ledger := t.rollBudget(now)
	ledger.Attempts++
	if record.ElapsedMS == nil {
		ledger.Untimed++
	} else {
		ledger.AgentMS += *record.ElapsedMS
	}
	if record.Usage != nil {
		if ledger.Usage == nil {
			ledger.Usage = &OutcomeUsage{}
		}
		ledger.Usage.InputTokens += record.Usage.InputTokens
		ledger.Usage.OutputTokens += record.Usage.OutputTokens
	}
	if record.CostUSD != nil {
		total := *record.CostUSD
		if ledger.CostUSD != nil {
			total += *ledger.CostUSD
		}
		ledger.CostUSD = &total
	}
}

// BudgetState is the operator-facing view: what this period measured, what the
// ceilings are, and whether new agent work is held. It is computed for display
// and for the scheduling gate, and never written back to state.
type BudgetState struct {
	// Configured reports whether the operator set a ceiling. Totals are
	// reported either way.
	Configured      bool      `json:"configured"`
	Period          string    `json:"period"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	Attempts        int       `json:"attempts"`
	MaxAttempts     int       `json:"max_attempts,omitempty"`
	AgentSeconds    int64     `json:"agent_seconds"`
	MaxAgentMinutes int       `json:"max_agent_minutes,omitempty"`
	// Untimed counts attempts this period whose elapsed time never arrived.
	// Their time is missing from AgentSeconds rather than known to be zero.
	Untimed int `json:"untimed"`
	// Exhausted holds new agent dispatches. Work already running finishes.
	Exhausted bool   `json:"exhausted"`
	Reason    string `json:"reason,omitempty"`
	// Advice states what Town cannot cap, so a missing cost ceiling reads as a
	// known limitation rather than an oversight.
	Advice string `json:"advice"`
	// Usage and CostUSD stay null unless an agent actually reported them. A
	// client must render null as unavailable, never as zero.
	Usage   *OutcomeUsage `json:"usage"`
	CostUSD *float64      `json:"cost_usd"`
}

// BudgetState reports this town's spend for the period containing now. A
// ledger left over from an earlier period reads as an empty current one; the
// reset is committed the next time an attempt is charged.
func (t *Town) BudgetState(now time.Time) BudgetState {
	period := t.budgetPeriod()
	from, to := BudgetWindow(period, now)
	state := BudgetState{Period: period, From: from, To: to, Advice: BudgetLimitAdvice}
	if ledger := t.Budget; ledger != nil && ledger.Period == period && ledger.From.Equal(from) {
		state.Attempts, state.AgentSeconds, state.Untimed = ledger.Attempts, ledger.AgentMS/1000, ledger.Untimed
		state.Usage, state.CostUSD = ledger.Usage, ledger.CostUSD
	}
	budget := t.Config.Budget
	if budget == nil {
		return state
	}
	state.Configured = true
	state.MaxAttempts, state.MaxAgentMinutes = budget.MaxAttempts, budget.MaxAgentMinutes
	resumes := to.Format("15:04 on 2 Jan")
	switch {
	case budget.MaxAttempts > 0 && state.Attempts >= budget.MaxAttempts:
		state.Exhausted = true
		state.Reason = fmt.Sprintf("Budget reached: %d of %d agent attempts this %s. New agent work resumes at %s.", state.Attempts, budget.MaxAttempts, period, resumes)
	case budget.MaxAgentMinutes > 0 && state.AgentSeconds >= int64(budget.MaxAgentMinutes)*60:
		state.Exhausted = true
		state.Reason = fmt.Sprintf("Budget reached: %d of %d agent minutes this %s. New agent work resumes at %s.", state.AgentSeconds/60, budget.MaxAgentMinutes, period, resumes)
	}
	if state.Exhausted && state.Untimed > 0 {
		state.Reason += fmt.Sprintf(" %d attempt(s) this period reported no elapsed time and are not counted in the minutes.", state.Untimed)
	}
	return state
}

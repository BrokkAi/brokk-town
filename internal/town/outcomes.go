package town

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// OutcomeRecord is one durable fact used to evaluate automation. Artifact
// submission is deliberately separate from confirmed repository outcomes.
// Metrics remain nil when the worker/provider did not report them; JSON then
// exposes an explicit null instead of silently treating missing data as zero.
type OutcomeRecord struct {
	ID            string    `json:"id"`
	Town          string    `json:"town,omitempty"`
	At            time.Time `json:"at"`
	Class         string    `json:"class"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	Role          Role      `json:"role,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	RelatedTaskID string    `json:"related_task_id,omitempty"`
	Revision      string    `json:"revision,omitempty"`
	URL           string    `json:"url,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	ElapsedMS     *int64    `json:"elapsed_ms"`
	// Agent reports whether this attempt held one of the service's agent
	// slots. A repository inventory reads GitHub without starting an agent;
	// a branch repair starts one. Budgets charge only the attempts that did.
	// For a repair the elapsed time covers the whole repo attempt, so the
	// charged minutes are an upper bound on the agent's own share.
	Agent    bool             `json:"agent,omitempty"`
	Usage    *OutcomeUsage    `json:"usage"`
	CostUSD  *float64         `json:"cost_usd"`
	Judgment *OutcomeJudgment `json:"judgment"`
}

type OutcomeUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type OutcomeJudgment struct {
	Value       string    `json:"value"`
	Explanation string    `json:"explanation"`
	RecordedAt  time.Time `json:"recorded_at"`
}

type OutcomeSummary struct {
	Attempts         int `json:"attempts"`
	FindingsFiled    int `json:"findings_filed"`
	PRsSubmitted     int `json:"prs_submitted"`
	MergesConfirmed  int `json:"merges_confirmed"`
	RepairRounds     int `json:"repair_rounds"`
	BlockedAbandoned int `json:"blocked_abandoned"`
	ReleasesVerified int `json:"releases_verified"`
	Useful           int `json:"useful"`
	FalsePositives   int `json:"false_positives"`
	UnjudgedFindings int `json:"unjudged_findings"`
}

type OutcomeReport struct {
	From      time.Time       `json:"from"`
	To        time.Time       `json:"to"`
	Generated time.Time       `json:"generated_at"`
	Towns     []string        `json:"towns"`
	Summary   OutcomeSummary  `json:"summary"`
	Records   []OutcomeRecord `json:"records"`
}

var outcomeClasses = map[string]bool{"attempt": true, "artifact": true, "outcome": true}
var outcomeStatuses = map[string]bool{"attempted": true, "submitted": true, "confirmed": true, "blocked": true, "abandoned": true, "external": true}

func (r OutcomeRecord) validate() error {
	if strings.TrimSpace(r.ID) == "" || r.At.IsZero() || !outcomeClasses[r.Class] || !outcomeStatuses[r.Status] || strings.TrimSpace(r.Kind) == "" {
		return errors.New("invalid outcome record")
	}
	if r.ElapsedMS != nil && *r.ElapsedMS < 0 {
		return errors.New("invalid outcome elapsed time")
	}
	if r.Usage != nil && (r.Usage.InputTokens < 0 || r.Usage.OutputTokens < 0) {
		return errors.New("invalid outcome usage")
	}
	if r.CostUSD != nil && *r.CostUSD < 0 {
		return errors.New("invalid outcome cost")
	}
	if r.Judgment != nil && (r.Kind != "finding_filed" || (r.Judgment.Value != "useful" && r.Judgment.Value != "false_positive") || strings.TrimSpace(r.Judgment.Explanation) == "" || r.Judgment.RecordedAt.IsZero()) {
		return errors.New("invalid outcome judgment")
	}
	return nil
}

// RecordOutcome makes repeated polls and restart reconciliation idempotent.
func (t *Town) RecordOutcome(record OutcomeRecord) {
	for i := range t.Outcomes {
		if t.Outcomes[i].ID == record.ID {
			// A bot receipt can establish an artifact before the next GitHub
			// inventory supplies its revision and URL. Enrich only absent facts;
			// the first observed time and provenance remain stable on every poll.
			existing := &t.Outcomes[i]
			if existing.Role == "" {
				existing.Role = record.Role
			}
			if existing.TaskID == "" {
				existing.TaskID = record.TaskID
			}
			if existing.RelatedTaskID == "" {
				existing.RelatedTaskID = record.RelatedTaskID
			}
			if existing.Revision == "" {
				existing.Revision = record.Revision
			}
			if existing.URL == "" {
				existing.URL = record.URL
			}
			if existing.Detail == "" {
				existing.Detail = record.Detail
			}
			if existing.ElapsedMS == nil {
				existing.ElapsedMS = record.ElapsedMS
			}
			if existing.Usage == nil {
				existing.Usage = record.Usage
			}
			if existing.CostUSD == nil {
				existing.CostUSD = record.CostUSD
			}
			return
		}
	}
	t.Outcomes = append(t.Outcomes, record)
}

func (t *Town) JudgeOutcome(id, value, explanation string, now time.Time) error {
	explanation = strings.TrimSpace(explanation)
	if value != "useful" && value != "false_positive" {
		return errors.New("judgment must be useful or false_positive")
	}
	if explanation == "" {
		return errors.New("a judgment explanation is required")
	}
	for i := range t.Outcomes {
		if t.Outcomes[i].ID == id {
			if t.Outcomes[i].Kind != "finding_filed" {
				return errors.New("only filed findings can receive usefulness judgments")
			}
			t.Outcomes[i].Judgment = &OutcomeJudgment{Value: value, Explanation: explanation, RecordedAt: now}
			return nil
		}
	}
	return errors.New("unknown outcome record")
}

func BuildOutcomeReport(state State, townID string, from, to, now time.Time) (OutcomeReport, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return OutcomeReport{}, errors.New("outcome period must have a start before its end")
	}
	report := OutcomeReport{From: from, To: to, Generated: now, Records: []OutcomeRecord{}, Towns: []string{}}
	ids := make([]string, 0, len(state.Towns))
	for id, t := range state.Towns {
		if t != nil && !t.Deleted && (townID == "" || townID == id) {
			ids = append(ids, id)
		}
	}
	if townID != "" && len(ids) == 0 {
		return OutcomeReport{}, fmt.Errorf("unknown town")
	}
	sort.Strings(ids)
	report.Towns = ids
	for _, id := range ids {
		for _, record := range state.Towns[id].Outcomes {
			if !record.At.Before(from) && record.At.Before(to) {
				record.Town = id
				report.Records = append(report.Records, record)
			}
		}
	}
	sort.Slice(report.Records, func(i, j int) bool {
		if report.Records[i].At.Equal(report.Records[j].At) {
			return report.Records[i].ID < report.Records[j].ID
		}
		return report.Records[i].At.Before(report.Records[j].At)
	})
	for _, r := range report.Records {
		switch r.Kind {
		case "worker_attempt":
			report.Summary.Attempts++
		case "finding_filed":
			report.Summary.FindingsFiled++
			if r.Judgment == nil {
				report.Summary.UnjudgedFindings++
			} else if r.Judgment.Value == "useful" {
				report.Summary.Useful++
			} else {
				report.Summary.FalsePositives++
			}
		case "implementation_pr":
			report.Summary.PRsSubmitted++
		case "merge":
			report.Summary.MergesConfirmed++
		case "repair_round":
			report.Summary.RepairRounds++
		case "blocked", "abandoned":
			report.Summary.BlockedAbandoned++
		case "release":
			report.Summary.ReleasesVerified++
		}
	}
	return report, nil
}

func elapsedMillis(start, end time.Time) *int64 {
	if start.IsZero() || end.Before(start) {
		return nil
	}
	value := end.Sub(start).Milliseconds()
	return &value
}

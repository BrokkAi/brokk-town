package town

import (
	"strings"
	"testing"
	"time"
)

func TestOutcomeLedgerCoversLifecycleAndDeduplicatesReconciliation(t *testing.T) {
	start := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	state := NewState(false)
	town, err := state.Add(DefaultConfig("acme/widgets"))
	if err != nil {
		t.Fatal(err)
	}
	town.Initialized = true
	issue := RemoteIssue{Number: 8, Title: "Prevent stale reports", Body: "<!-- bug-bot: finding -->", URL: "https://github.com/acme/widgets/issues/8", State: "open"}
	Reconcile(&state, town, RepoSnapshot{Branch: "master", Head: baseSHA, Issues: []RemoteIssue{issue}, Released: map[string]bool{}}, start)
	if err := town.JudgeOutcome("finding-filed:issue:8", "false_positive", "Expected behavior for archived runs", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	town.Owned[18] = Ownership{Branch: "issue-8", Issue: 8}
	town.RecordOutcome(OutcomeRecord{ID: "implementation-pr:pr:18", At: start.Add(90 * time.Second), Class: "artifact", Kind: "implementation_pr", Status: "submitted", Role: Issue, TaskID: "pr:18", RelatedTaskID: "issue:8"})
	open := Pull{Number: 18, Title: "Prevent stale reports", URL: "https://github.com/acme/widgets/pull/18", State: "open"}
	open.Base.Ref, open.Base.SHA = "master", baseSHA
	open.Head.Ref, open.Head.SHA, open.Head.Repo.FullName = "issue-8", headSHA, "acme/widgets"
	Reconcile(&state, town, RepoSnapshot{Branch: "master", Head: baseSHA, Issues: []RemoteIssue{issue}, Pulls: []Pull{open}, Released: map[string]bool{}}, start.Add(2*time.Minute))

	town.Intents[18] = &Intent{Kind: "repair", PR: 18, Base: baseSHA, Head: headSHA, NewHead: fixSHA, Branch: "repair", Directory: "/tmp/repair", Status: "uncertain", At: start}
	repaired := open
	repaired.Head.SHA = fixSHA
	Reconcile(&state, town, RepoSnapshot{Branch: "master", Head: baseSHA, Issues: []RemoteIssue{issue}, Pulls: []Pull{repaired}, Released: map[string]bool{}}, start.Add(3*time.Minute))

	mergedAt := start.Add(4 * time.Minute)
	merged := repaired
	merged.State, merged.MergedAt, merged.MergeCommit = "closed", &mergedAt, strings.Repeat("d", 40)
	Reconcile(&state, town, RepoSnapshot{Branch: "master", Head: merged.MergeCommit, Issues: []RemoteIssue{issue}, Pulls: []Pull{merged}, Released: map[string]bool{}}, mergedAt)

	releaseAt := start.Add(5 * time.Minute)
	release := RemoteRelease{Tag: "v1.2.3", Name: "v1.2.3", URL: "https://github.com/acme/widgets/releases/tag/v1.2.3", At: releaseAt}
	remote := RepoSnapshot{Branch: "master", Head: merged.MergeCommit, Issues: []RemoteIssue{issue}, Pulls: []Pull{merged}, Releases: []RemoteRelease{release}, Released: map[string]bool{merged.MergeCommit: true}}
	Reconcile(&state, town, remote, releaseAt)
	count := len(town.Outcomes)
	Reconcile(&state, town, remote, releaseAt.Add(time.Minute))
	if len(town.Outcomes) != count {
		t.Fatalf("poll duplicated outcomes: %d became %d", count, len(town.Outcomes))
	}

	town.RecordOutcome(OutcomeRecord{ID: "blocked:issue:9:3", At: start.Add(6 * time.Minute), Class: "outcome", Kind: "blocked", Status: "blocked", TaskID: "issue:9", Revision: strings.Repeat("e", 40), Detail: "attempt budget exhausted"})
	town.Owned[19] = Ownership{Branch: "issue-9", Issue: 9}
	rejected := Pull{Number: 19, Title: "Rejected implementation", URL: "https://github.com/acme/widgets/pull/19", State: "closed"}
	rejected.Base.Ref, rejected.Base.SHA = "master", baseSHA
	rejected.Head.Ref, rejected.Head.SHA, rejected.Head.Repo.FullName = "issue-9", strings.Repeat("e", 40), "acme/widgets"
	external := Pull{Number: 20, Title: "Contributor update", URL: "https://github.com/acme/widgets/pull/20", State: "open"}
	external.Base.Ref, external.Base.SHA = "master", baseSHA
	external.Head.Ref, external.Head.SHA, external.Head.Repo.FullName = "contributor", headSHA, "someone/fork"
	Reconcile(&state, town, RepoSnapshot{Branch: "master", Head: merged.MergeCommit, Pulls: []Pull{rejected, external}, Releases: []RemoteRelease{release}, Released: map[string]bool{}}, start.Add(7*time.Minute))
	external.Head.SHA = strings.Repeat("f", 40)
	Reconcile(&state, town, RepoSnapshot{Branch: "master", Head: merged.MergeCommit, Pulls: []Pull{rejected, external}, Releases: []RemoteRelease{release}, Released: map[string]bool{}}, start.Add(8*time.Minute))
	elapsed, cost := int64(12_000), 0.42
	town.RecordOutcome(OutcomeRecord{ID: "attempt:review:1:pr:18", At: start.Add(9 * time.Minute), Class: "attempt", Kind: "worker_attempt", Status: "attempted", Role: Review, TaskID: "pr:18", Revision: fixSHA, ElapsedMS: &elapsed, Usage: &OutcomeUsage{InputTokens: 1200, OutputTokens: 300}, CostUSD: &cost})

	report, err := BuildOutcomeReport(state, town.ID, start.Add(-time.Minute), start.Add(time.Hour), start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Attempts != 1 || report.Summary.FindingsFiled != 1 || report.Summary.PRsSubmitted != 2 || report.Summary.RepairRounds != 1 || report.Summary.MergesConfirmed != 1 || report.Summary.ReleasesVerified != 1 || report.Summary.BlockedAbandoned != 2 || report.Summary.FalsePositives != 1 || report.Summary.Useful != 0 {
		t.Fatalf("unexpected lifecycle summary: %+v", report.Summary)
	}
	foundExternal, foundMeasuredAttempt, foundEnrichedPR := false, false, false
	for _, record := range report.Records {
		if record.Kind == "external_change" {
			foundExternal = true
		}
		if record.Kind == "worker_attempt" {
			foundMeasuredAttempt = record.ElapsedMS != nil && *record.ElapsedMS == elapsed && record.Usage != nil && record.Usage.InputTokens == 1200 && record.CostUSD != nil && *record.CostUSD == cost
		} else if record.ID == "implementation-pr:pr:18" {
			foundEnrichedPR = record.Revision == headSHA && record.URL == open.URL
		} else if record.ElapsedMS != nil || record.Usage != nil || record.CostUSD != nil {
			t.Fatalf("unreported metrics were invented: %+v", record)
		}
	}
	if !foundExternal {
		t.Fatal("external revision change was not retained")
	}
	if !foundMeasuredAttempt {
		t.Fatal("available attempt time, usage, and cost were not retained")
	}
	if !foundEnrichedPR {
		t.Fatal("receipt-derived PR artifact was not enriched with GitHub provenance")
	}
}

func TestOutcomeJudgmentRequiresExplicitExplanation(t *testing.T) {
	town := &Town{Outcomes: []OutcomeRecord{{ID: "finding-filed:issue:1", At: time.Now(), Class: "artifact", Kind: "finding_filed", Status: "confirmed"}}}
	if err := town.JudgeOutcome("finding-filed:issue:1", "useful", "", time.Now()); err == nil {
		t.Fatal("empty explanation accepted")
	}
	if err := town.JudgeOutcome("finding-filed:issue:1", "useful", "Caught a real regression", time.Now()); err != nil {
		t.Fatal(err)
	}
	if town.Outcomes[0].Judgment == nil || town.Outcomes[0].Judgment.Value != "useful" {
		t.Fatal("judgment not saved")
	}
}

func TestWorkerResultRetainsAvailableUsageAndCost(t *testing.T) {
	stream := strings.NewReader("{\"type\":\"result\",\"seq\":1,\"result\":{\"issue\":{\"owned\":[]},\"usage\":{\"input_tokens\":80,\"output_tokens\":20},\"cost_usd\":0.125}}\n{\"type\":\"complete\",\"seq\":2}\n")
	result, err := consumeWorkerEvents(stream, 0, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkerResult(result, Issue); err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil || result.Usage.InputTokens != 80 || result.Usage.OutputTokens != 20 || result.CostUSD == nil || *result.CostUSD != 0.125 {
		t.Fatalf("worker metrics were not retained: %+v", result)
	}
}

func TestOutcomeLedgerPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	if err := store.Update(func(state *State) error {
		town, err := state.Add(DefaultConfig("acme/restart"))
		if err != nil {
			return err
		}
		town.RecordOutcome(OutcomeRecord{ID: "finding-filed:issue:4", At: at, Class: "artifact", Kind: "finding_filed", Status: "confirmed", Role: Bug, TaskID: "issue:4", Revision: headSHA})
		return town.JudgeOutcome("finding-filed:issue:4", "useful", "Confirmed after deployment", at.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	records := store.Snapshot().Towns["acme/restart"].Outcomes
	if len(records) != 1 || records[0].Revision != headSHA || records[0].Judgment == nil || records[0].Judgment.Explanation != "Confirmed after deployment" {
		t.Fatalf("restart lost outcome provenance or judgment: %+v", records)
	}
}

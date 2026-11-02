package reviewbot

import (
	"strings"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

func TestReviewResultRequiresSubmittedExactRevision(t *testing.T) {
	req := botapi.Request{PR: 7, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	job := &Job{Status: "stale", Failure: "PR revision changed or is no longer eligible"}
	job.PR.Number, job.PR.Base.SHA, job.PR.Head.SHA = req.PR, req.BaseSHA, req.HeadSHA
	job.Candidates = []Candidate{{Verdict: "confirmed", Finding: Finding{Title: "old concern", Severity: "P2"}}}
	saved := &State{Jobs: []*Job{job}}
	got := reviewResult(saved, req)
	if got.Complete || got.Status != "stale" || got.Detail != job.Failure || len(got.Findings) != 0 {
		t.Fatalf("stale result: %+v", got)
	}
	job.Status = "submitted"
	got = reviewResult(saved, req)
	if !got.Complete || len(got.Findings) != 1 {
		t.Fatalf("submitted result: %+v", got)
	}
	for id, text := range got.Findings {
		if got.Severities[id] != "P2" || !strings.HasPrefix(text, "[P2] ") {
			t.Fatalf("finding severity not reported: %q %+v", text, got.Severities)
		}
	}
	job.PR.Head.SHA = strings.Repeat("c", 40)
	got = reviewResult(saved, req)
	if got.Complete || len(got.Findings) != 0 {
		t.Fatalf("wrong revision certified: %+v", got)
	}
	job.PR.Head.SHA, job.DryRun = req.HeadSHA, true
	if reviewResult(saved, req).Complete {
		t.Fatal("dry run certified")
	}
}

func TestDryRunReviewReportsCompletionWithoutCertifyingLiveWork(t *testing.T) {
	req := botapi.Request{PR: 7, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), DryRun: true}
	job := &Job{Status: "dry_run", DryRun: true}
	job.PR.Number, job.PR.Base.SHA, job.PR.Head.SHA = req.PR, req.BaseSHA, req.HeadSHA
	saved := &State{Jobs: []*Job{job}}
	got := reviewResult(saved, req)
	if got.Status != "dry_run" || got.Complete || len(got.Findings) != 0 || !strings.Contains(got.Detail, "no review was published") {
		t.Fatalf("dry-run outcome was lost or certified: %+v", got)
	}
	req.DryRun = false
	if got := reviewResult(saved, req); got.Status != "stale" || got.Complete {
		t.Fatalf("dry run consumed live review: %+v", got)
	}
	req.DryRun, req.HeadSHA = true, strings.Repeat("c", 40)
	if got := reviewResult(saved, req); got.Status != "stale" || got.Complete {
		t.Fatalf("dry run accepted a different revision: %+v", got)
	}
}

package town

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"

	issuebot "github.com/BrokkAi/brokk-town/bots/issue-bot"
	releasebot "github.com/BrokkAi/brokk-town/bots/release-bot"
	reviewbot "github.com/BrokkAi/brokk-town/bots/review-bot"
)

func TestReleasePolicyAppliesCadence(t *testing.T) {
	defaults := releasebot.DefaultConfig()

	unchanged := releaseConfig(workerRequest{})
	if unchanged.Daily != defaults.Daily || unchanged.Quiet != defaults.Quiet || unchanged.Triage != defaults.Triage || unchanged.Attempts != defaults.Attempts {
		t.Fatalf("a request with no policy changed the defaults: %+v", unchanged)
	}
	empty := releaseConfig(workerRequest{Policy: &BotPolicy{}})
	if empty.Daily != defaults.Daily || empty.Burst != defaults.Burst {
		t.Fatalf("an empty policy changed the defaults: %+v", empty)
	}

	off := false
	dir := t.TempDir()
	request := workerRequest{
		Remote: "https://github.com/o/r.git", Branch: "main",
		Directory: filepath.Join(dir, "checkout"), StateDirectory: filepath.Join(dir, "state"),
		Repo: "o/r", Host: "github.com",
		Agent: runner.AgentConfig{Command: []string{"simulated"}},
		Policy: &BotPolicy{
			Attempts: 5,
			Verify:   []string{"house-verify"},
			Release: &ReleasePolicy{
				DailySeconds: 7200, MinimumGapSeconds: 900, QuietSeconds: 300,
				Burst: 4, BurstWindowSeconds: 1800, Triage: &off,
				Preflight: []string{"make", "preflight"}, VerificationTimeoutSeconds: 120,
				Workflows: []string{"ci"}, Assets: []string{"dist/*.tar.gz"},
			},
		},
	}
	cfg := releaseConfig(request)
	for _, tc := range []struct {
		name string
		got  releasebot.Duration
		want time.Duration
	}{
		{"daily", cfg.Daily, 2 * time.Hour},
		{"minimum gap", cfg.MinimumGap, 15 * time.Minute},
		{"quiet", cfg.Quiet, 5 * time.Minute},
		{"burst window", cfg.BurstWindow, 30 * time.Minute},
		{"verification timeout", cfg.VerificationTimeout, 2 * time.Minute},
	} {
		if time.Duration(tc.got) != tc.want {
			t.Fatalf("%s = %v, want %v", tc.name, time.Duration(tc.got), tc.want)
		}
	}
	if cfg.Burst != 4 || cfg.Attempts != 5 || cfg.Triage {
		t.Fatalf("policy scalars not applied: burst %d attempts %d triage %v", cfg.Burst, cfg.Attempts, cfg.Triage)
	}
	if !reflect.DeepEqual(cfg.Preflight, []string{"make", "preflight"}) || !reflect.DeepEqual(cfg.Verify, []string{"house-verify"}) {
		t.Fatalf("commands not applied: preflight %v verify %v", cfg.Preflight, cfg.Verify)
	}
	if !reflect.DeepEqual(cfg.GitHub.Workflows, []string{"ci"}) || !reflect.DeepEqual(cfg.GitHub.Assets, []string{"dist/*.tar.gz"}) {
		t.Fatalf("release gating not applied: workflows %v assets %v", cfg.GitHub.Workflows, cfg.GitHub.Assets)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a policy produced a configuration the bot rejects: %v", err)
	}

	on := true
	if !releaseConfig(workerRequest{Policy: &BotPolicy{Release: &ReleasePolicy{Triage: &on}}}).Triage {
		t.Fatal("triage could not be turned on")
	}
}

func TestReviewResultRequiresSubmittedExactRevision(t *testing.T) {
	req := workerRequest{PR: 7, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	job := &reviewbot.Job{Status: "stale", Failure: "PR revision changed or is no longer eligible"}
	job.PR.Number, job.PR.Base.SHA, job.PR.Head.SHA = req.PR, req.BaseSHA, req.HeadSHA
	job.Candidates = []reviewbot.Candidate{{Verdict: "confirmed", Finding: reviewbot.Finding{Title: "old concern", Severity: "P2"}}}
	saved := &reviewbot.State{Jobs: []*reviewbot.Job{job}}
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
	req := workerRequest{PR: 7, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), DryRun: true}
	job := &reviewbot.Job{Status: "dry_run", DryRun: true}
	job.PR.Number, job.PR.Base.SHA, job.PR.Head.SHA = req.PR, req.BaseSHA, req.HeadSHA
	saved := &reviewbot.State{Jobs: []*reviewbot.Job{job}}
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

func TestIssueJobSummaryAndTargetedRetryPreserveSavedEvidence(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Join(dir, "checkout"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	saved := issuebot.State{Format: 1, Remote: "https://github.com/acme/fixture.git", Branch: "main", Directory: filepath.Join(dir, "checkout"), Repo: "acme/fixture", Host: "github.com", Jobs: map[int]*issuebot.Job{
		7: {Issue: issuebot.Issue{Number: 7}, Status: "blocked", Tries: 3, RetryAt: time.Now().Add(time.Hour), ClaimPending: true, Failure: "uncertain publication", Result: &issuebot.Result{Status: "blocked", Detail: "saved evidence"}},
		8: {Issue: issuebot.Issue{Number: 8}, Status: "blocked", Tries: 2},
	}}
	data, _ := json.Marshal(saved)
	if err := os.WriteFile(filepath.Join(state, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	request := workerRequest{Remote: saved.Remote, Branch: saved.Branch, Directory: saved.Directory, StateDirectory: state, Repo: saved.Repo, Host: saved.Host}
	query := func(mode string, issue int) map[int]*issueJobSummary {
		t.Helper()
		r := request
		r.Mode, r.Issue = mode, issue
		result, err := runIssueBot(context.Background(), r, func(Progress) {})
		if err != nil {
			t.Fatal(err)
		}
		return result.Jobs
	}
	jobs := query("jobs", 0)
	if jobs[7].Status != "blocked" || !jobs[7].ClaimPending || jobs[7].Result.Detail != "saved evidence" {
		t.Fatalf("summary lost evidence: %+v", jobs[7])
	}
	jobs = query("retry-issue", 7)
	if jobs[7].Status != "pending" || jobs[7].Tries != 0 || !jobs[7].RetryAt.IsZero() || jobs[8].Tries != 2 {
		t.Fatalf("incorrect retry: %+v", jobs)
	}
	after, err := issuebot.ReadState(issuebot.Config{StateDirectory: state, Remote: saved.Remote, Branch: saved.Branch, Directory: saved.Directory, GitHub: issuebot.GitHubConfig{Repo: saved.Repo, Host: saved.Host}})
	if err != nil {
		t.Fatal(err)
	}
	if !after.Jobs[7].ClaimPending || after.Jobs[7].Result.Detail != "saved evidence" {
		t.Fatal("retry lost durable evidence")
	}
}

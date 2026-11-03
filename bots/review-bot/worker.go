package reviewbot

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Worker serves this bot through the bot contract, keeping its workspaces
// under root.
func Worker(root string) botapi.Worker {
	return botapi.Worker{Name: "review-bot", Role: "review", Root: root, Handle: handle}
}

func handle(ctx context.Context, job botapi.Job) (botapi.Result, error) {
	if job.PR < 1 {
		return botapi.Result{}, fmt.Errorf("review worker requires a positive PR number")
	}
	cfg := DefaultConfig()
	cfg.Remote = job.Remote
	cfg.Branch = job.Branch
	cfg.Directory = job.Directory
	cfg.StateDirectory = job.StateDirectory
	cfg.Agent = job.Launch
	cfg.RemoteAgent = job.RemoteAgent
	cfg.RemoteAgentToken = job.RemoteAgentToken
	cfg.DryRun = job.DryRun
	cfg.GitHub.Repo = job.Repo
	cfg.GitHub.Host = job.Host
	cfg.PR = job.PR
	cfg.Verify = job.Verify
	if policy := job.Policy; policy != nil {
		cfg.Labels = botapi.AppendLabels(cfg.Labels, policy.Labels)
		cfg.ExcludeLabels = botapi.AppendLabels(cfg.ExcludeLabels, policy.ExcludeLabels)
		if policy.Only > 0 && cfg.PR == 0 {
			cfg.PR = policy.Only
		}
		if policy.Focus != "" {
			cfg.Focus = policy.Focus
		}
		if policy.Limit > 0 {
			cfg.MaxFindings = policy.Limit
		}
		if policy.Attempts > 0 {
			cfg.Attempts = policy.Attempts
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	ctx = WithProgress(ctx, func(p Progress) { job.Progress(p.Phase, p.Task) })
	if err := Run(ctx, cfg, job.Log, true); err != nil {
		return botapi.Result{}, err
	}
	saved, err := ReadState(cfg)
	if err != nil {
		return botapi.Result{}, err
	}
	if saved == nil {
		return botapi.Result{}, fmt.Errorf("review did not produce a durable result")
	}
	return botapi.Result{Review: reviewResult(saved, job.Request)}, nil
}

func findingID(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:24]
}

// reviewResult only certifies a submitted review of the requested revision.
// Other revisions' findings must never be attributed to this dispatch.
func reviewResult(saved *State, request botapi.Request) *botapi.ReviewResult {
	result := &botapi.ReviewResult{Status: "stale", Detail: "No submitted review matches the requested revision; refresh PR eligibility and revisions", Findings: map[string]string{}, Severities: map[string]string{}, ExactBase: request.BaseSHA, ExactHead: request.HeadSHA}
	for _, job := range saved.Jobs {
		if job.PR.Number != request.PR || job.DryRun != request.DryRun || job.PR.Head.SHA != request.HeadSHA || job.PR.Base.SHA != request.BaseSHA {
			continue
		}
		result.Status, result.Detail = job.Status, job.Failure
		if job.DryRun {
			if job.Status == "dry_run" {
				result.Detail = "Review completed in dry-run mode; no review was published"
			}
			continue // A dry-run result never grants publication or merge authority.
		}
		if job.Status != "submitted" {
			continue
		}
		result.Complete = true
		for _, candidate := range job.Candidates {
			if candidate.Verdict == "invalid" {
				continue
			}
			id := findingID(candidate.Finding.Path + candidate.Finding.Title + candidate.Finding.Trigger)
			result.Findings[id] = fmt.Sprintf("[%s] %s: %s\n%s\nTrigger: %s\nEvidence: %s\nVerifier: %s", candidate.Finding.Severity, candidate.Finding.Path, candidate.Finding.Title, candidate.Finding.Explanation, candidate.Finding.Trigger, strings.Join(candidate.Finding.Evidence, "; "), candidate.Reason)
			result.Severities[id] = candidate.Finding.Severity
		}
	}
	return result
}

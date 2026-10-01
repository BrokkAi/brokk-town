package town

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	bugbot "github.com/BrokkAi/brokk-town/bots/bug-bot"
	featurebot "github.com/BrokkAi/brokk-town/bots/feature-bot"
	issuebot "github.com/BrokkAi/brokk-town/bots/issue-bot"
	mayorbot "github.com/BrokkAi/brokk-town/bots/mayor-bot"
	releasebot "github.com/BrokkAi/brokk-town/bots/release-bot"
	repobot "github.com/BrokkAi/brokk-town/bots/repo-bot"
	reviewbot "github.com/BrokkAi/brokk-town/bots/review-bot"
	simplifierbot "github.com/BrokkAi/brokk-town/bots/simplifier-bot"
)

// botVersion is the identity Town records for an in-process bot dispatch. The
// bots are packages of this module now, so a house no longer has a separately
// versioned executable to probe and every dispatch records this placeholder.
const botVersion = "0.0.0"

// dispatchBot runs one bot operation. It replaces the worker API: instead of
// launching a bot binary and exchanging JSON over a socket, Town calls the same
// bot functions the worker command used to wrap. Tests may replace it with
// botRun.
func (b *BotWorkers) dispatchBot(ctx context.Context, role Role, request workerRequest, retry bool, observe func(Progress)) (workerResult, error) {
	if b.botRun != nil {
		return b.botRun(ctx, role, request, retry, observe)
	}
	if observe == nil {
		observe = func(Progress) {}
	}
	switch role {
	case Bug:
		return runBugBot(ctx, request, observe)
	case Feature:
		return runFeatureBot(ctx, request, observe)
	case Issue:
		return runIssueBot(ctx, request, observe)
	case Review:
		return runReviewBot(ctx, request, observe)
	case Release:
		return runReleaseBot(ctx, request, retry, observe)
	case Simplifier:
		return runSimplifierBot(ctx, request, observe)
	case Repo:
		return runRepoBot(ctx, request, observe)
	case Hall:
		return runMayorBot(ctx, request, observe)
	default:
		return workerResult{}, fmt.Errorf("unsupported worker %s", role)
	}
}

// observe bot progress through the same callbacks the worker protocol used to
// stream as events.
func botObserve(observe func(Progress)) func(phase, task string) {
	return func(phase, task string) {
		phase, task = strings.TrimSpace(phase), strings.TrimSpace(task)
		if phase == "" {
			phase = "running"
		}
		observe(Progress{Phase: phase, Task: truncate(task, 4096)})
	}
}

func runBugBot(ctx context.Context, request workerRequest, observe func(Progress)) (workerResult, error) {
	cfg := bugbot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	cfg.Verify = request.Verify
	if policy := request.Policy; policy != nil {
		cfg.Labels = appendLabels(cfg.Labels, policy.Labels)
		if policy.Focus != "" {
			cfg.Focus = policy.Focus
		}
		if policy.Limit > 0 {
			cfg.MaxIssues = policy.Limit
		}
		if policy.Attempts > 0 {
			cfg.Attempts = policy.Attempts
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	forward := botObserve(observe)
	ctx = bugbot.WithProgress(ctx, func(p bugbot.Progress) { forward(p.Phase, p.Task) })
	return workerResult{}, bugbot.Run(ctx, cfg, slog.Default(), true)
}

func runFeatureBot(ctx context.Context, request workerRequest, observe func(Progress)) (workerResult, error) {
	cfg := featurebot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	cfg.Verify = request.Verify
	if policy := request.Policy; policy != nil {
		cfg.Labels = appendLabels(cfg.Labels, policy.Labels)
		if policy.Focus != "" {
			cfg.Focus = policy.Focus
		}
		if policy.Limit > 0 {
			cfg.MaxIssues = policy.Limit
		}
		if policy.Attempts > 0 {
			cfg.Attempts = policy.Attempts
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	forward := botObserve(observe)
	ctx = featurebot.WithProgress(ctx, func(p featurebot.Progress) { forward(p.Phase, p.Task) })
	return workerResult{}, featurebot.Run(ctx, cfg, slog.Default(), true)
}

func runIssueBot(ctx context.Context, request workerRequest, observe func(Progress)) (workerResult, error) {
	cfg := issuebot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	cfg.Draft = false
	cfg.Verify = request.Verify
	cfg.Issue = request.Issue
	switch request.Mode {
	case "jobs", "retry-issue":
		if request.Mode == "retry-issue" {
			if request.Issue < 1 {
				return workerResult{}, fmt.Errorf("retry requires an issue")
			}
			if err := issuebot.Retry(cfg); err != nil {
				return workerResult{}, err
			}
		}
		saved, err := issuebot.ReadState(cfg)
		if err != nil {
			return workerResult{}, err
		}
		jobs := map[int]*issueJobSummary{}
		if saved != nil {
			for number, j := range saved.Jobs {
				v := &issueJobSummary{Status: j.Status, Failure: j.Failure, ClaimPending: j.ClaimPending, Tries: j.Tries, RetryAt: j.RetryAt, URL: j.URL, Branch: j.Branch}
				if j.Result != nil {
					v.Result = &issueJobResult{Status: j.Result.Status, Detail: j.Result.Detail}
				}
				jobs[number] = v
			}
		}
		return workerResult{Jobs: jobs}, nil
	case "":
	default:
		return workerResult{}, fmt.Errorf("unknown issue worker mode %q", request.Mode)
	}
	if policy := request.Policy; policy != nil {
		cfg.Labels = appendLabels(cfg.Labels, policy.Labels)
		cfg.ExcludeLabels = appendLabels(cfg.ExcludeLabels, policy.ExcludeLabels)
		// Town selects the issue it wants; Only is the operator's standing
		// restriction and applies when no exact issue was dispatched.
		if policy.Only > 0 && cfg.Issue == 0 {
			cfg.Issue = policy.Only
		}
		if policy.Attempts > 0 {
			cfg.Attempts = policy.Attempts
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	if request.SupersededPR > 0 {
		if request.Issue < 1 {
			return workerResult{}, fmt.Errorf("requeue requires an exact issue")
		}
		if err := issuebot.Requeue(cfg, request.SupersededPR); err != nil {
			return workerResult{}, fmt.Errorf("requeue issue #%d: %w", request.Issue, err)
		}
	}
	forward := botObserve(observe)
	ctx = issuebot.WithProgress(ctx, func(p issuebot.Progress) { forward(p.Phase, p.Task) })
	runErr := issuebot.Run(ctx, cfg, slog.Default(), true)
	result := workerResult{Issue: &workerIssueResult{Owned: []workerIssueOwnership{}}}
	saved, readErr := issuebot.ReadState(cfg)
	if readErr != nil {
		return result, readErr
	}
	if saved != nil {
		for _, job := range saved.Jobs {
			if job.Status != "submitted" {
				continue
			}
			parsed, err := url.Parse(job.URL)
			if err != nil || parsed.Host != request.Host {
				continue
			}
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(parts) != 4 || !strings.EqualFold(strings.Join(parts[:2], "/"), request.Repo) || parts[2] != "pull" {
				continue
			}
			if number, err := strconv.Atoi(parts[3]); err == nil && number > 0 {
				result.Issue.Owned = append(result.Issue.Owned, workerIssueOwnership{PR: number, Branch: job.Branch, Issue: job.Issue.Number})
			}
		}
	}
	return result, runErr
}

func runReviewBot(ctx context.Context, request workerRequest, observe func(Progress)) (workerResult, error) {
	if request.PR < 1 {
		return workerResult{}, fmt.Errorf("review worker requires a positive PR number")
	}
	cfg := reviewbot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.RemoteAgent = request.RemoteAgent
	cfg.DryRun = request.DryRun
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	cfg.PR = request.PR
	cfg.Verify = request.Verify
	if policy := request.Policy; policy != nil {
		cfg.Labels = appendLabels(cfg.Labels, policy.Labels)
		cfg.ExcludeLabels = appendLabels(cfg.ExcludeLabels, policy.ExcludeLabels)
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
	forward := botObserve(observe)
	ctx = reviewbot.WithProgress(ctx, func(p reviewbot.Progress) { forward(p.Phase, p.Task) })
	if err := reviewbot.Run(ctx, cfg, slog.Default(), true); err != nil {
		return workerResult{}, err
	}
	saved, err := reviewbot.ReadState(cfg)
	if err != nil {
		return workerResult{}, err
	}
	if saved == nil {
		return workerResult{}, fmt.Errorf("review did not produce a durable result")
	}
	return workerResult{Review: reviewResult(saved, request)}, nil
}

func findingID(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:24]
}

// reviewResult only certifies a submitted review of the requested revision.
// Other revisions' findings must never be attributed to this dispatch.
func reviewResult(saved *reviewbot.State, request workerRequest) *workerReviewResult {
	result := &workerReviewResult{Status: "stale", Detail: "No submitted review matches the requested revision; refresh PR eligibility and revisions", Findings: map[string]string{}, Severities: map[string]string{}, ExactBase: request.BaseSHA, ExactHead: request.HeadSHA}
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

func runReleaseBot(ctx context.Context, request workerRequest, retry bool, observe func(Progress)) (workerResult, error) {
	cfg := releaseConfig(request)
	if retry {
		if err := releasebot.Retry(cfg); err != nil {
			return workerResult{}, err
		}
	}
	forward := botObserve(observe)
	ctx = releasebot.WithProgress(ctx, func(p releasebot.Progress) { forward(p.Phase, p.Task) })
	result := workerResult{retried: retry}
	if err := releasebot.Run(ctx, cfg, slog.Default(), true, false); err != nil {
		return result, err
	}
	return result, nil
}

// releaseConfig applies the bot's defaults to the workspace Town named.
func releaseConfig(request workerRequest) releasebot.Config {
	cfg := releasebot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	cfg.Verify = request.Verify
	applyReleasePolicy(&cfg, request.Policy)
	return cfg
}

// applyReleasePolicy overlays Town's release cadence and gating. An unset field
// keeps this bot's default, so a request without a policy is unchanged.
func applyReleasePolicy(cfg *releasebot.Config, policy *BotPolicy) {
	if policy == nil {
		return
	}
	if policy.Attempts > 0 {
		cfg.Attempts = policy.Attempts
	}
	if len(policy.Verify) > 0 {
		cfg.Verify = policy.Verify
	}
	release := policy.Release
	if release == nil {
		return
	}
	seconds := func(v int, into *releasebot.Duration) {
		if v > 0 {
			*into = releasebot.Duration(time.Duration(v) * time.Second)
		}
	}
	seconds(release.DailySeconds, &cfg.Daily)
	seconds(release.MinimumGapSeconds, &cfg.MinimumGap)
	seconds(release.QuietSeconds, &cfg.Quiet)
	seconds(release.BurstWindowSeconds, &cfg.BurstWindow)
	seconds(release.VerificationTimeoutSeconds, &cfg.VerificationTimeout)
	if release.Burst > 0 {
		cfg.Burst = release.Burst
	}
	if release.Triage != nil {
		cfg.Triage = *release.Triage
	}
	if len(release.Preflight) > 0 {
		cfg.Preflight = release.Preflight
	}
	if len(release.Workflows) > 0 {
		cfg.GitHub.Workflows = release.Workflows
	}
	if len(release.Assets) > 0 {
		cfg.GitHub.Assets = release.Assets
	}
}

func runSimplifierBot(ctx context.Context, request workerRequest, observe func(Progress)) (workerResult, error) {
	cfg := simplifierbot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	forward := botObserve(observe)
	ctx = simplifierbot.WithProgress(ctx, func(p simplifierbot.Progress) { forward(p.Phase, p.Task) })
	if policy := request.Policy; policy != nil {
		cfg.Labels = appendLabels(cfg.Labels, policy.Labels)
		if policy.Limit > 0 {
			cfg.MaxProposals = policy.Limit
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	if request.Issue == 0 && request.PR == 0 {
		return workerResult{}, simplifierbot.Run(ctx, cfg, slog.Default(), true)
	}
	assessment, err := simplifierbot.Assess(ctx, cfg, request.Mode, request.Issue, request.PR, slog.Default())
	if err != nil {
		return workerResult{}, err
	}
	return workerResult{Simplification: &workerSimplification{
		Mode: request.Mode, Decision: assessment.Decision, Summary: assessment.Summary, Detail: assessment.Detail,
	}}, nil
}

func runRepoBot(ctx context.Context, request workerRequest, observe func(Progress)) (workerResult, error) {
	cfg := repobot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	cfg.Verify = request.Verify
	if policy := request.Policy; policy != nil {
		if policy.Limit > 0 {
			cfg.MaxRepairs = policy.Limit
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	forward := botObserve(observe)
	ctx = repobot.WithProgress(ctx, func(p repobot.Progress) { forward(p.Phase, p.Task) })
	observed, err := repobot.Run(ctx, cfg, repobot.Request{SinceHead: request.SinceHead, Commits: request.Commits, InventorySince: request.InventorySince, InventoryBranch: request.InventoryBranch}, nil, slog.Default())
	// The repo bot returns its own observation types. Town copies the fields it
	// reconciles rather than describing the same shape a second time.
	result := workerResult{}
	if inventory := observed.Inventory; inventory != nil {
		converted := &workerInventory{
			Incremental: inventory.Incremental, StartedAt: inventory.StartedAt, Branch: inventory.Branch,
			DefaultBranch: inventory.DefaultBranch, Head: inventory.Head, Released: inventory.Released,
		}
		for _, issue := range inventory.Issues {
			remote := RemoteIssue{Number: issue.Number, Title: issue.Title, Body: issue.Body, URL: issue.URL, State: issue.State, Locked: issue.Locked, Pull: issue.Pull, Updated: issue.Updated}
			for _, label := range issue.Labels {
				remote.Labels = append(remote.Labels, RemoteLabel{Name: label.Name})
			}
			converted.Issues = append(converted.Issues, remote)
		}
		for _, pull := range inventory.Pulls {
			remote := Pull{Number: pull.Number, Title: pull.Title, Body: pull.Body, URL: pull.URL, State: pull.State, Draft: pull.Draft, Locked: pull.Locked, Comments: pull.Comments, ReviewComments: pull.ReviewComments, MergedAt: pull.MergedAt, MergeCommit: pull.MergeCommit, Updated: pull.Updated}
			for _, label := range pull.Labels {
				remote.Labels = append(remote.Labels, RemoteLabel{Name: label.Name})
			}
			remote.Head = Ref{Ref: pull.Head.Ref, SHA: pull.Head.SHA}
			remote.Head.Repo.FullName = pull.Head.Repo.FullName
			remote.Base = Ref{Ref: pull.Base.Ref, SHA: pull.Base.SHA}
			remote.Base.Repo.FullName = pull.Base.Repo.FullName
			converted.Pulls = append(converted.Pulls, remote)
		}
		for _, release := range inventory.Releases {
			converted.Releases = append(converted.Releases, RemoteRelease{Tag: release.Tag, Name: release.Name, URL: release.URL, Draft: release.Draft, Prerelease: release.Prerelease, At: release.At})
		}
		for _, commit := range inventory.Commits {
			remote := RemoteCommit{SHA: commit.SHA, URL: commit.URL}
			remote.Commit.Message = commit.Commit.Message
			converted.Commits = append(converted.Commits, remote)
		}
		result.Inventory = converted
	}
	if health := observed.Health; health != nil {
		result.Health = &BranchHealth{State: health.State, Head: health.Head, Failing: health.Failing, Pushed: health.Pushed, Attempts: health.Attempts, Detail: health.Detail}
	}
	return result, err
}

func runMayorBot(ctx context.Context, request workerRequest, observe func(Progress)) (workerResult, error) {
	cfg := mayorbot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	cfg.Verify = request.Verify
	if policy := request.Policy; policy != nil {
		if policy.Limit > 0 {
			cfg.MaxItems = policy.Limit
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	forward := botObserve(observe)
	ctx = mayorbot.WithProgress(ctx, func(p mayorbot.Progress) { forward(p.Phase, p.Task) })
	switch request.Mode {
	case "judge":
		judgment, err := mayorbot.Judge(ctx, cfg, mayorbot.JudgeRequest{Issue: request.Issue, PR: request.PR, HeadSHA: request.HeadSHA, BaseSHA: request.BaseSHA, Arrival: request.Arrival}, slog.Default())
		if err != nil {
			return workerResult{}, err
		}
		return workerResult{Judgment: &Judgment{Decision: judgment.Decision, Reason: judgment.Reason}}, nil
	case "bulletin":
		var window mayorbot.Window
		if request.Since != nil {
			window.Since = *request.Since
		}
		if request.Until != nil {
			window.Until = *request.Until
		}
		report, err := mayorbot.WriteBulletin(ctx, cfg, window, slog.Default())
		if err != nil {
			return workerResult{}, err
		}
		items := make([]BulletinItem, 0, len(report.Items))
		for _, item := range report.Items {
			items = append(items, BulletinItem{Kind: item.Kind, Title: item.Title, Detail: item.Detail, Pulls: item.Pulls, Issues: item.Issues})
		}
		return workerResult{Bulletin: &Bulletin{Since: report.Since, Until: report.Until, Title: report.Title, Summary: report.Summary, Items: items, Pulls: report.Pulls}}, nil
	default:
		return workerResult{}, fmt.Errorf("mayor worker requires mode judge or bulletin, got %q", request.Mode)
	}
}

// appendLabels adds Town's filter to the bot's own default without duplicating
// an entry the configuration already carries.
func appendLabels(existing, extra []string) []string {
	if len(extra) == 0 {
		return existing
	}
	seen := map[string]bool{}
	for _, l := range existing {
		seen[strings.ToLower(strings.TrimSpace(l))] = true
	}
	for _, l := range extra {
		key := strings.ToLower(strings.TrimSpace(l))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		existing = append(existing, strings.TrimSpace(l))
	}
	return existing
}

package issuebot

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Worker serves this bot through the bot contract, keeping its workspaces
// under root.
func Worker(root string) botapi.Worker {
	return botapi.Worker{Name: "issue-bot", Role: "issue", Root: root, Handle: handle}
}

func handle(ctx context.Context, job botapi.Job) (botapi.Result, error) {
	cfg := DefaultConfig()
	cfg.Remote = job.Remote
	cfg.Branch = job.Branch
	cfg.Directory = job.Directory
	cfg.StateDirectory = job.StateDirectory
	cfg.Agent = job.Launch
	cfg.GitHub.Repo = job.Repo
	cfg.GitHub.Host = job.Host
	cfg.Draft = false
	cfg.Verify = job.Verify
	cfg.Issue = job.Issue
	switch job.Mode {
	case "jobs", "retry-issue":
		if job.Mode == "retry-issue" {
			if job.Issue < 1 {
				return botapi.Result{}, fmt.Errorf("retry requires an issue")
			}
			if err := Retry(cfg); err != nil {
				return botapi.Result{}, err
			}
		}
		return jobSummaries(cfg)
	case "":
	default:
		return botapi.Result{}, fmt.Errorf("unknown issue worker mode %q", job.Mode)
	}
	if policy := job.Policy; policy != nil {
		cfg.Labels = botapi.AppendLabels(cfg.Labels, policy.Labels)
		cfg.ExcludeLabels = botapi.AppendLabels(cfg.ExcludeLabels, policy.ExcludeLabels)
		// The service selects the issue it wants; Only is the operator's
		// standing restriction and applies when no exact issue was dispatched.
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
	if job.SupersededPR > 0 {
		if job.Issue < 1 {
			return botapi.Result{}, fmt.Errorf("requeue requires an exact issue")
		}
		if err := Requeue(cfg, job.SupersededPR); err != nil {
			return botapi.Result{}, fmt.Errorf("requeue issue #%d: %w", job.Issue, err)
		}
	}
	ctx = WithProgress(ctx, func(p Progress) { job.Progress(p.Phase, p.Task) })
	runErr := Run(ctx, cfg, job.Log, true)
	result := botapi.Result{Issue: &botapi.IssueResult{Owned: []botapi.IssueOwnership{}}}
	saved, readErr := ReadState(cfg)
	if readErr != nil {
		return result, readErr
	}
	if saved != nil {
		for _, j := range saved.Jobs {
			if j.Status != "submitted" {
				continue
			}
			if number := ownedPR(j.URL, job.Host, job.Repo); number > 0 {
				result.Issue.Owned = append(result.Issue.Owned, botapi.IssueOwnership{PR: number, Branch: j.Branch, Issue: j.Issue.Number})
			}
		}
	}
	return result, runErr
}

// jobSummaries reports the public summary of every saved job, not the state
// schema behind it.
func jobSummaries(cfg Config) (botapi.Result, error) {
	saved, err := ReadState(cfg)
	if err != nil {
		return botapi.Result{}, err
	}
	jobs := map[int]*botapi.IssueJob{}
	if saved != nil {
		for number, j := range saved.Jobs {
			v := &botapi.IssueJob{Status: j.Status, Failure: j.Failure, ClaimPending: j.ClaimPending, Tries: j.Tries, RetryAt: j.RetryAt, URL: j.URL, Branch: j.Branch}
			if j.Result != nil {
				v.Result = &botapi.IssueJobResult{Status: j.Result.Status, Detail: j.Result.Detail}
			}
			jobs[number] = v
		}
	}
	return botapi.Result{Jobs: jobs}, nil
}

// ownedPR returns the pull request number a submitted job's URL names on this
// repository, or 0.
func ownedPR(raw, host, repo string) int {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host != host {
		return 0
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 4 || !strings.EqualFold(strings.Join(parts[:2], "/"), repo) || parts[2] != "pull" {
		return 0
	}
	if number, err := strconv.Atoi(parts[3]); err == nil && number > 0 {
		return number
	}
	return 0
}

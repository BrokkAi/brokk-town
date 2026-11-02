package mayorbot

import (
	"context"
	"fmt"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Worker serves this bot through the bot contract, keeping its workspaces
// under root.
func Worker(root string) botapi.Worker {
	return botapi.Worker{Name: "mayor-bot", Role: "hall", Root: root, Handle: handle}
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
	cfg.Verify = job.Verify
	if policy := job.Policy; policy != nil {
		if policy.Limit > 0 {
			cfg.MaxItems = policy.Limit
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	ctx = WithProgress(ctx, func(p Progress) { job.Progress(p.Phase, p.Task) })
	switch job.Mode {
	case "judge":
		judgment, err := Judge(ctx, cfg, JudgeRequest{Issue: job.Issue, PR: job.PR, HeadSHA: job.HeadSHA, BaseSHA: job.BaseSHA, Arrival: job.Arrival}, job.Log)
		if err != nil {
			return botapi.Result{}, err
		}
		return botapi.Result{Judgment: &botapi.Judgment{Decision: judgment.Decision, Reason: judgment.Reason}}, nil
	case "bulletin":
		var window Window
		if job.Since != nil {
			window.Since = *job.Since
		}
		if job.Until != nil {
			window.Until = *job.Until
		}
		report, err := WriteBulletin(ctx, cfg, window, job.Log)
		if err != nil {
			return botapi.Result{}, err
		}
		items := make([]botapi.BulletinItem, 0, len(report.Items))
		for _, item := range report.Items {
			items = append(items, botapi.BulletinItem{Kind: item.Kind, Title: item.Title, Detail: item.Detail, Pulls: item.Pulls, Issues: item.Issues})
		}
		return botapi.Result{Bulletin: &botapi.Bulletin{Since: report.Since, Until: report.Until, Title: report.Title, Summary: report.Summary, Items: items, Pulls: report.Pulls}}, nil
	default:
		return botapi.Result{}, fmt.Errorf("judge worker requires mode judge or bulletin, got %q", job.Mode)
	}
}

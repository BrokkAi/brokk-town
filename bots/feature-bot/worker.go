package featurebot

import (
	"context"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Worker serves this bot through the bot contract, keeping its workspaces
// under root.
func Worker(root string) botapi.Worker {
	return botapi.Worker{Name: "feature-bot", Role: "feature", Root: root, Handle: handle}
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
		cfg.Labels = botapi.AppendLabels(cfg.Labels, policy.Labels)
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
	ctx = WithProgress(ctx, func(p Progress) { job.Progress(p.Phase, p.Task) })
	return botapi.Result{}, Run(ctx, cfg, job.Log, true)
}

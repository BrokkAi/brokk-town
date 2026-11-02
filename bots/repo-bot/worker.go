package repobot

import (
	"context"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Worker serves this bot through the bot contract, keeping its workspaces
// under root.
func Worker(root string) botapi.Worker {
	return botapi.Worker{Name: "repo-bot", Role: "repo", Root: root, Handle: handle}
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
			cfg.MaxRepairs = policy.Limit
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	ctx = WithProgress(ctx, func(p Progress) { job.Progress(p.Phase, p.Task) })
	observed, err := Run(ctx, cfg, Request{SinceHead: job.SinceHead, Commits: job.Commits, InventorySince: job.InventorySince, InventoryBranch: job.InventoryBranch}, nil, job.Log)
	return botapi.Result{Inventory: observed.Inventory, Health: observed.Health}, err
}

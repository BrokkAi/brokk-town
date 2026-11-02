package simplifierbot

import (
	"context"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Worker serves this bot through the bot contract, keeping its workspaces
// under root.
func Worker(root string) botapi.Worker {
	return botapi.Worker{Name: "simplifier-bot", Role: "simplifier", Root: root, Handle: handle}
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
	ctx = WithProgress(ctx, func(p Progress) { job.Progress(p.Phase, p.Task) })
	if policy := job.Policy; policy != nil {
		cfg.Labels = botapi.AppendLabels(cfg.Labels, policy.Labels)
		if policy.Limit > 0 {
			cfg.MaxProposals = policy.Limit
		}
		if len(policy.Verify) > 0 {
			cfg.Verify = policy.Verify
		}
	}
	if job.Issue == 0 && job.PR == 0 {
		return botapi.Result{}, Run(ctx, cfg, job.Log, true)
	}
	assessment, err := Assess(ctx, cfg, job.Mode, job.Issue, job.PR, job.Log)
	if err != nil {
		return botapi.Result{}, err
	}
	return botapi.Result{Simplification: &botapi.Simplification{
		Mode: job.Mode, Decision: assessment.Decision, Summary: assessment.Summary, Detail: assessment.Detail,
	}}, nil
}

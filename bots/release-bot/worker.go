package releasebot

import (
	"context"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Worker serves this bot through the bot contract, keeping its workspaces
// under root.
func Worker(root string) botapi.Worker {
	return botapi.Worker{Name: "release-bot", Role: "release", Root: root, Handle: handle}
}

func handle(ctx context.Context, job botapi.Job) (botapi.Result, error) {
	cfg := jobConfig(job)
	if job.Retry {
		if err := Retry(cfg); err != nil {
			return botapi.Result{}, err
		}
	}
	ctx = WithProgress(ctx, func(p Progress) { job.Progress(p.Phase, p.Task) })
	return botapi.Result{}, Run(ctx, cfg, job.Log, true, false)
}

// jobConfig applies this bot's defaults to the workspace the job names.
func jobConfig(job botapi.Job) Config {
	cfg := DefaultConfig()
	cfg.Remote = job.Remote
	cfg.Branch = job.Branch
	cfg.Directory = job.Directory
	cfg.StateDirectory = job.StateDirectory
	cfg.Agent = job.Launch
	cfg.GitHub.Repo = job.Repo
	cfg.GitHub.Host = job.Host
	cfg.Verify = job.Verify
	applyPolicy(&cfg, job.Policy)
	return cfg
}

// applyPolicy overlays the operator's release cadence and gating. An unset
// field keeps this bot's default, so a request without a policy is unchanged.
func applyPolicy(cfg *Config, policy *botapi.Policy) {
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
	seconds := func(v int, into *Duration) {
		if v > 0 {
			*into = Duration(time.Duration(v) * time.Second)
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

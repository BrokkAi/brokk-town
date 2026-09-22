package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"time"

	bot "github.com/BrokkAi/release-bot"
	"github.com/BrokkAi/release-bot/internal/worker"
)

func workerCommand(ctx context.Context, args []string, version string) error {
	fs := flag.NewFlagSet("brb worker", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: brb worker --socket PATH\n\nServe versioned one-shot release checks to Brokk Town over a private Unix socket.")
		fs.PrintDefaults()
	}
	socket := fs.String("socket", "", "private Unix-domain socket path (required)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *socket == "" {
		return fmt.Errorf("worker requires exactly one --socket PATH")
	}
	run := func(ctx context.Context, request worker.Request, progress func(worker.Progress)) (worker.Result, error) {
		ctx = bot.WithProgress(ctx, func(p bot.Progress) {
			progress(worker.Progress{Phase: p.Phase, Task: p.Task})
		})
		return worker.Result{}, bot.Run(ctx, workerConfig(request), slog.Default(), true, false)
	}
	retry := func(_ context.Context, request worker.Request) error {
		return bot.Retry(workerConfig(request))
	}
	return worker.Serve(ctx, *socket, worker.Initialize{
		Protocol: worker.ProtocolVersion, MinimumProtocol: worker.MinimumProtocol,
		Bot: "release-bot", Version: version, Capabilities: []string{"policy", "run", "progress", "release", "retry"},
	}, run, retry, slog.Default())
}

// workerConfig applies this bot's defaults to the workspace Town named.
func workerConfig(request worker.Request) bot.Config {
	cfg := bot.DefaultConfig()
	cfg.Remote = request.Remote
	cfg.Branch = request.Branch
	cfg.Directory = request.Directory
	cfg.StateDirectory = request.StateDirectory
	cfg.Agent = request.Agent
	cfg.GitHub.Repo = request.Repo
	cfg.GitHub.Host = request.Host
	cfg.Verify = request.Verify
	applyPolicy(&cfg, request.Policy)
	return cfg
}

// applyPolicy overlays Town's release cadence and gating. An unset field keeps
// this bot's default, so a request without a policy is unchanged.
func applyPolicy(cfg *bot.Config, policy *worker.Policy) {
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
	seconds := func(v int, into *bot.Duration) {
		if v > 0 {
			*into = bot.Duration(time.Duration(v) * time.Second)
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

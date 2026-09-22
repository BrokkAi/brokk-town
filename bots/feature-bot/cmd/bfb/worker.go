package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"

	bot "github.com/BrokkAi/feature-bot"
	"github.com/BrokkAi/feature-bot/internal/worker"
)

func workerCommand(ctx context.Context, args []string, version string) error {
	fs := flag.NewFlagSet("bfb worker", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: bfb worker --socket PATH\n\nServe versioned one-shot feature research to Brokk Town over a private Unix socket.")
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
	return worker.Serve(ctx, *socket, workerInfo(version), workerRun(bot.Run), slog.Default())
}

func workerInfo(version string) worker.Initialize {
	return worker.Initialize{
		Protocol: worker.ProtocolVersion, MinimumProtocol: worker.MinimumProtocol,
		Bot: "feature-bot", Version: version, Capabilities: []string{"policy", "run", "progress", "feature-research", "feature-research-controls"},
	}
}

func workerRun(run runFunc) worker.RunFunc {
	return func(ctx context.Context, request worker.Request, progress func(worker.Progress)) (worker.Result, error) {
		cfg := bot.DefaultConfig()
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
		if options := request.FeatureResearch; options != nil {
			cfg.Focus = options.Focus
			if options.MaxIssues != nil {
				cfg.MaxIssues = *options.MaxIssues
			}
		}
		if err := cfg.Validate(); err != nil {
			return worker.Result{}, err
		}
		ctx = bot.WithProgress(ctx, func(p bot.Progress) {
			progress(worker.Progress{Phase: p.Phase, Task: p.Task})
		})
		return worker.Result{}, run(ctx, cfg, slog.Default(), true)
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

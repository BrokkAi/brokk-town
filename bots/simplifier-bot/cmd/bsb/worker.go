package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"

	bot "github.com/BrokkAi/simplifier-bot"
	"github.com/BrokkAi/simplifier-bot/internal/worker"
)

func workerCommand(ctx context.Context, args []string, version string) error {
	fs := flag.NewFlagSet("bsb worker", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: bsb worker --socket PATH\n\nServe versioned one-shot simplifier operations to Brokk Town over a private Unix socket.")
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
	return worker.Serve(ctx, *socket, worker.Initialize{
		Protocol: worker.ProtocolVersion, MinimumProtocol: worker.MinimumProtocol,
		Bot: "simplifier-bot", Version: version, Capabilities: []string{"run", "progress", "simplifier-review"},
	}, func(ctx context.Context, request worker.Request, progress func(worker.Progress)) (worker.Result, error) {
		cfg := bot.DefaultConfig()
		cfg.Remote = request.Remote
		cfg.Branch = request.Branch
		cfg.Directory = request.Directory
		cfg.StateDirectory = request.StateDirectory
		cfg.Agent = request.Agent
		cfg.GitHub.Repo = request.Repo
		cfg.GitHub.Host = request.Host
		ctx = bot.WithProgress(ctx, func(p bot.Progress) {
			progress(worker.Progress{Phase: p.Phase, Task: p.Task})
		})
		if request.Issue == 0 && request.PR == 0 {
			return worker.Result{}, bot.Run(ctx, cfg, slog.Default(), true)
		}
		assessment, err := bot.Assess(ctx, cfg, request.Mode, request.Issue, request.PR, slog.Default())
		if err != nil {
			return worker.Result{}, err
		}
		return worker.Result{Simplification: &worker.SimplificationResult{
			Mode: request.Mode, Decision: assessment.Decision, Summary: assessment.Summary, Detail: assessment.Detail,
		}}, nil
	}, slog.Default())
}

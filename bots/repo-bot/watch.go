package repobot

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

// Watch runs the bot on its own: it observes the repository every poll
// interval, repairing a failing branch as Town's runs would, and logs what each
// observation found. Each run names the commits gained since the previous one.
func Watch(ctx context.Context, cfg Config, log *slog.Logger, once bool) error {
	if log == nil {
		log = slog.Default()
	}
	since := ""
	for {
		result, err := Run(ctx, cfg, Request{SinceHead: since}, nil, log)
		if inventory := result.Inventory; inventory != nil {
			since = inventory.Head
			issues, pulls := 0, 0
			for _, i := range inventory.Issues {
				if i.State == "open" && len(i.Pull) == 0 {
					issues++
				}
			}
			for _, p := range inventory.Pulls {
				if p.State == "open" {
					pulls++
				}
			}
			log.Info("Observed repository", "repository", cfg.GitHubRepo(), "branch", inventory.Branch, "head", short(inventory.Head), "open_issues", issues, "open_pulls", pulls, "releases", len(inventory.Releases), "new_commits", len(inventory.Commits))
		}
		if health := result.Health; health != nil {
			attrs := []any{"state", health.State, "head", short(health.Head)}
			if len(health.Failing) > 0 {
				attrs = append(attrs, "failing", health.Failing)
			}
			if health.Pushed != "" {
				attrs = append(attrs, "pushed", short(health.Pushed))
			}
			if health.Detail != "" {
				attrs = append(attrs, "detail", health.Detail)
			}
			log.Info("Branch health", attrs...)
		}
		var startup *runner.SetupError
		if once || errors.As(err, &startup) || ctx.Err() != nil {
			return err
		}
		if err != nil {
			log.Error("Repository observation paused", "error", err)
		}
		timer := time.NewTimer(time.Duration(cfg.Poll))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

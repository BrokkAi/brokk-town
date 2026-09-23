package repobot

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/repo-bot/internal/worker"
)

// Watch runs the bot on its own: it observes the repository every poll
// interval, repairing a failing branch as Town's runs would, and logs and saves
// what each observation found. Each run names the commits gained since the
// previous one.
func Watch(ctx context.Context, cfg Config, log *slog.Logger, once bool) error {
	if log == nil {
		log = slog.Default()
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg, err := cfg.resolved()
	if err != nil {
		return err
	}
	saved, err := ReadState(cfg)
	if err != nil {
		return err
	}
	// Run reports its phase alone; the display also needs the history.
	outer := observe(ctx)
	var mu sync.Mutex
	var observations []Observation
	var wake time.Time
	if saved != nil {
		observations = saved.History
	}
	report := func(p Progress) {
		mu.Lock()
		p = history(observations, p)
		p.WakeAt = wake
		mu.Unlock()
		outer(p)
	}
	ctx = WithProgress(ctx, report)
	report(Progress{Phase: "starting", Task: "Loading saved observations"})
	since := ""
	if n := len(observations); n > 0 {
		since = observations[n-1].Head
	}
	for {
		result, err := Run(ctx, cfg, Request{SinceHead: since}, nil, log)
		o := observation(result, err)
		if o.Head != "" {
			since = o.Head
		}
		logObservation(log, cfg, o)
		if ctx.Err() == nil {
			var saveErr error
			if saveErr = updateState(cfg, func(s *State) {
				s.History = append(s.History, o)
				if len(s.History) > maxHistory {
					s.History = s.History[len(s.History)-maxHistory:]
				}
				mu.Lock()
				observations = append([]Observation(nil), s.History...)
				mu.Unlock()
			}); saveErr != nil && err == nil {
				err = saveErr
			}
		}
		mu.Lock()
		wake = time.Now().Add(time.Duration(cfg.Poll))
		mu.Unlock()
		if err == nil {
			report(Progress{Phase: "complete", Task: "Observed " + short(o.Head)})
		}
		var startup *runner.SetupError
		if once || errors.As(err, &startup) || ctx.Err() != nil {
			return err
		}
		if err != nil {
			log.Error("Repository observation paused", "error", err)
			report(Progress{Phase: "paused", Task: err.Error()})
		} else {
			report(Progress{Phase: "waiting", Task: "Next observation"})
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

// observation summarizes one run for the saved history.
func observation(result worker.Result, err error) Observation {
	o := Observation{At: time.Now().UTC()}
	if inventory := result.Inventory; inventory != nil {
		o.Head, o.Releases, o.NewCommits = inventory.Head, len(inventory.Releases), len(inventory.Commits)
		for _, i := range inventory.Issues {
			if i.State == "open" && len(i.Pull) == 0 {
				o.OpenIssues++
			}
		}
		for _, p := range inventory.Pulls {
			if p.State == "open" {
				o.OpenPulls++
			}
		}
	}
	if health := result.Health; health != nil {
		o.Health, o.Failing, o.Pushed, o.Attempts, o.Detail = health.State, health.Failing, health.Pushed, health.Attempts, health.Detail
	}
	if err != nil {
		o.Error = truncate(err.Error(), 2000)
	}
	return o
}

func logObservation(log *slog.Logger, cfg Config, o Observation) {
	if o.Head != "" {
		log.Info("Observed repository", "repository", cfg.GitHubRepo(), "branch", cfg.Branch, "head", short(o.Head), "open_issues", o.OpenIssues, "open_pulls", o.OpenPulls, "releases", o.Releases, "new_commits", o.NewCommits)
	}
	if o.Health != "" {
		attrs := []any{"state", o.Health}
		if len(o.Failing) > 0 {
			attrs = append(attrs, "failing", o.Failing)
		}
		if o.Pushed != "" {
			attrs = append(attrs, "pushed", short(o.Pushed))
		}
		if o.Detail != "" {
			attrs = append(attrs, "detail", o.Detail)
		}
		log.Info("Branch health", attrs...)
	}
}

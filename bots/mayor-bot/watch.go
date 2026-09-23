package mayorbot

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

// Watch runs the Mayor on its own: every poll interval it writes a bulletin for
// the pull requests merged since the previous bulletin it recorded, or over the
// last interval when it has none, and logs it. It never writes to GitHub.
func Watch(ctx context.Context, cfg Config, log *slog.Logger, once bool) error {
	if log == nil {
		log = slog.Default()
	}
	if err := prepareConfig(&cfg); err != nil {
		return err
	}
	for {
		s, err := ReadState(cfg)
		if err != nil {
			return err
		}
		since := time.Now().Add(-time.Duration(cfg.Poll))
		if s != nil {
			if last := latestUntil(s); !last.IsZero() {
				since = last
			}
		}
		if wait := time.Until(since.Add(time.Duration(cfg.Poll))); !once && wait > 0 {
			if err := pause(ctx, wait); err != nil {
				return err
			}
			continue
		}
		report, err := WriteBulletin(ctx, cfg, Window{Since: since, Until: time.Now()}, log)
		if err == nil {
			logBulletin(log, report)
		}
		var startup *runner.SetupError
		if once || errors.As(err, &startup) || ctx.Err() != nil {
			return err
		}
		if err != nil {
			log.Error("Bulletin paused", "error", err)
		}
		// A window with nothing merged records no bulletin, so the next one
		// still starts where the last recorded bulletin ended.
		if err := pause(ctx, time.Duration(cfg.Poll)); err != nil {
			return err
		}
	}
}

// latestUntil is where the most recent recorded bulletin ended.
func latestUntil(s *State) time.Time {
	var last time.Time
	for _, r := range s.Bulletins {
		if r.Until.After(last) {
			last = r.Until
		}
	}
	return last
}

func logBulletin(log *slog.Logger, report Report) {
	log.Info(report.Title, "summary", report.Summary, "since", report.Since.UTC().Format(time.RFC3339), "until", report.Until.UTC().Format(time.RFC3339), "pulls", len(report.Pulls))
	for _, item := range report.Items {
		log.Info(item.Title, "kind", item.Kind, "detail", item.Detail, "pulls", item.Pulls)
	}
}

package mayorbot

import (
	"context"
	"errors"
	"log/slog"
	"sync"
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
	// Duties report their phase alone; the display also needs the history.
	outer := observe(ctx)
	var mu sync.Mutex
	var saved *State
	var wake time.Time
	report := func(p Progress) {
		mu.Lock()
		p = history(saved, p)
		p.WakeAt = wake
		mu.Unlock()
		outer(p)
	}
	ctx = WithProgress(ctx, report)
	load := func() (*State, error) {
		s, err := ReadState(cfg)
		mu.Lock()
		saved = s
		mu.Unlock()
		return s, err
	}
	s, err := load()
	if err != nil {
		return err
	}
	report(Progress{Phase: "starting", Task: "Loading recorded bulletins"})
	// covered is where this process's last window left off when that window
	// recorded no bulletin: an empty window ends at its Until, a failed one
	// still owes everything from its Since.
	var covered time.Time
	for {
		since := windowStart(s, covered, time.Now(), time.Duration(cfg.Poll))
		if wait := time.Until(since.Add(time.Duration(cfg.Poll))); !once && wait > 0 {
			mu.Lock()
			wake = since.Add(time.Duration(cfg.Poll))
			mu.Unlock()
			report(Progress{Phase: "waiting", Task: "Next bulletin"})
			if err := pause(ctx, wait); err != nil {
				return err
			}
		} else {
			window := Window{Since: since, Until: time.Now()}
			result, err := WriteBulletin(ctx, cfg, window, log)
			if err == nil {
				logBulletin(log, result)
				covered = window.Until
			} else {
				covered = window.Since
			}
			if _, loadErr := load(); loadErr != nil && err == nil {
				err = loadErr
			}
			if err == nil {
				report(Progress{Phase: "complete", Task: result.Title})
			}
			var startup *runner.SetupError
			if once || errors.As(err, &startup) || ctx.Err() != nil {
				return err
			}
			if err != nil {
				log.Error("Bulletin paused", "error", err)
				mu.Lock()
				wake = time.Now().Add(time.Duration(cfg.Poll))
				mu.Unlock()
				report(Progress{Phase: "paused", Task: err.Error()})
			} else {
				// A window with nothing merged records no bulletin, so the next
				// one still starts where the last recorded bulletin ended.
				mu.Lock()
				wake = time.Now().Add(time.Duration(cfg.Poll))
				mu.Unlock()
				report(Progress{Phase: "waiting", Task: "Next bulletin"})
			}
			if err := pause(ctx, time.Duration(cfg.Poll)); err != nil {
				return err
			}
		}
		if s, err = load(); err != nil {
			return err
		}
	}
}

// windowStart is where the next bulletin begins: the later of the newest
// recorded bulletin's end and the window this process already covered without
// recording one, or one interval ago when there is neither.
func windowStart(s *State, covered, now time.Time, poll time.Duration) time.Time {
	since := covered
	if s != nil {
		if last := latestUntil(s); last.After(since) {
			since = last
		}
	}
	if since.IsZero() {
		since = now.Add(-poll)
	}
	return since
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

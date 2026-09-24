package mayorbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func filepathAbs(path string) (string, error) { return filepath.Abs(path) }

// Window is the span one bulletin covers: pull requests merged after Since and
// no later than Until.
type Window struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// Report is one finished bulletin with the window and pull requests it covered,
// so the town can advance its cursor without trusting the prose.
type Report struct {
	Window
	Bulletin
	Pulls []int `json:"pulls"`
}

type windowPull struct {
	Number      int           `json:"number"`
	Title       string        `json:"title"`
	Body        string        `json:"body"`
	URL         string        `json:"url"`
	Author      string        `json:"author"`
	MergedAt    time.Time     `json:"merged_at"`
	MergeCommit string        `json:"merge_commit,omitempty"`
	Files       []ChangedFile `json:"files,omitempty"`
	Closes      []windowIssue `json:"closes,omitempty"`
}
type windowIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
}

// WriteBulletin summarizes the pull requests merged in the window for users of
// the software. Windows with nothing merged return an empty bulletin without
// starting an agent.
func WriteBulletin(ctx context.Context, cfg Config, window Window, log *slog.Logger) (Report, error) {
	if err := prepareConfig(&cfg); err != nil {
		return Report{}, err
	}
	if log == nil {
		log = slog.Default()
	}
	unlock, err := lockConfig(cfg)
	if err != nil {
		return Report{}, err
	}
	defer unlock()
	s, err := ReadState(cfg)
	if err != nil {
		return Report{}, err
	}
	if s == nil {
		s = newState(cfg)
	}
	return engine{config: cfg, source: githubClient{cfg}, log: log, agent: func(c Config) Agent { return agentProcess{c, log} }, sleep: pause, observe: observe(ctx)}.bulletin(ctx, s, window)
}

type engine struct {
	config  Config
	source  source
	log     *slog.Logger
	agent   func(Config) Agent
	sleep   func(context.Context, time.Duration) error
	observe func(Progress)
}

func (e engine) bulletin(ctx context.Context, s *State, window Window) (Report, error) {
	report := Report{Window: window, Pulls: []int{}}
	if window.Since.IsZero() || !window.Until.After(window.Since) {
		return report, errors.New("bulletin window needs a start before its end")
	}
	e.observe(Progress{Phase: "loading", Task: "Refreshing repository and merged pull requests"})
	base := checkout{config: e.config}
	if err := base.open(ctx); err != nil {
		return report, err
	}
	head, err := base.head(ctx)
	if err != nil {
		return report, err
	}
	merged, err := e.source.mergedPulls(ctx, window.Since, window.Until)
	if err != nil {
		return report, err
	}
	if len(merged) == 0 {
		report.Bulletin = Bulletin{Title: "Nothing new this time", Summary: fmt.Sprintf("No pull requests merged into %s between %s and %s.", e.config.Branch, window.Since.UTC().Format(time.RFC3339), window.Until.UTC().Format(time.RFC3339)), Items: []BulletinItem{}}
		return report, nil
	}
	pulls := make([]windowPull, 0, len(merged))
	for _, p := range merged {
		entry := windowPull{Number: p.Number, Title: p.Title, Body: bounded(p.Body, 16<<10), URL: p.URL, Author: p.User.Login, MergedAt: *p.MergedAt, MergeCommit: p.MergeCommit}
		if files, err := e.source.files(ctx, p.Number); err == nil {
			if len(files) > 100 {
				files = files[:100]
			}
			entry.Files = files
		} else {
			e.log.Warn("could not list changed files", "pr", p.Number, "error", err)
		}
		for i, n := range linkedIssues(p.Body) {
			if i >= 5 {
				break
			}
			issue := windowIssue{Number: n}
			if detail, err := e.source.issue(ctx, n); err == nil {
				issue.Title, issue.Body = detail.Title, bounded(detail.Body, 4<<10)
			} else {
				e.log.Warn("could not read a linked issue", "issue", n, "error", err)
			}
			entry.Closes = append(entry.Closes, issue)
		}
		pulls = append(pulls, entry)
		report.Pulls = append(report.Pulls, p.Number)
	}
	worktree, err := base.itemWorktree(ctx, "bulletin", head)
	if err != nil {
		return report, err
	}
	instructions, err := instructionFiles(worktree.config)
	if err != nil {
		return report, err
	}
	e.observe(Progress{Phase: "writing", Task: fmt.Sprintf("Summarizing %d merged pull request(s) for users", len(pulls))})
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.config.Timeout))
	defer cancel()
	text, err := execute(ctx, e.agent(worktree.config), instructions+bulletinPrompt(struct {
		Branch string       `json:"branch"`
		Since  time.Time    `json:"since"`
		Until  time.Time    `json:"until"`
		Pulls  []windowPull `json:"pulls"`
	}{e.config.Branch, window.Since, window.Until, pulls}, e.config.MaxItems), e.log, e.sleep)
	if err != nil {
		return report, err
	}
	if err = worktree.verify(ctx, head); err != nil {
		return report, err
	}
	if report.Bulletin, err = parseBulletin(text, report.Pulls, e.config.MaxItems); err != nil {
		return report, err
	}
	s.record(BulletinRecord{Since: window.Since, Until: window.Until, At: time.Now(), Title: report.Title, Items: len(report.Items), Pulls: report.Pulls, Summary: report.Summary, Entries: report.Items})
	if err = writeState(e.config, s); err != nil {
		return report, err
	}
	e.observe(Progress{Phase: "reporting", Task: report.Title})
	return report, nil
}
func instructionFiles(cfg Config) (string, error) {
	var sections []string
	for _, name := range cfg.InstructionFiles {
		b, err := os.ReadFile(filepath.Join(cfg.Directory, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if len(b) > 256<<10 {
			b = b[:256<<10]
		}
		sections = append(sections, "# "+name+"\n"+string(b))
	}
	if len(sections) == 0 {
		return "", nil
	}
	return strings.Join(sections, "\n\n") + "\n\n", nil
}

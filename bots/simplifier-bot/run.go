package simplifierbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

var startupRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second}

type engine struct {
	config  Config
	source  source
	log     *slog.Logger
	agent   func(Config) Agent
	sleep   func(context.Context, time.Duration) error
	observe func(Progress)
}

func Run(ctx context.Context, cfg Config, log *slog.Logger, once bool) error {
	if err := prepareConfig(&cfg); err != nil {
		return err
	}
	if log == nil {
		log = slog.Default()
	}
	unlock, err := lockConfig(cfg)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := ReadState(cfg)
	if err != nil {
		return err
	}
	if s == nil {
		s = newState(cfg)
	}
	e := engine{config: cfg, source: githubClient{cfg}, log: log, agent: func(c Config) Agent { return agentProcess{c, log} }, sleep: pause, observe: observe(ctx)}
	for {
		// Worker dispatches are one scheduler step, not a command that bypasses
		// this bot's own saved scan interval.
		err := e.scan(ctx, s, false)
		var startup *runner.SetupError
		if once || errors.As(err, &startup) || ctx.Err() != nil {
			return err
		}
		if err != nil {
			log.Error("Simplifier scan paused", "error", err)
			e.observe(Progress{Phase: "paused", Task: err.Error()})
		} else {
			e.observe(Progress{Phase: "waiting", Task: "Next simplification scan"})
		}
		if err := pause(ctx, time.Duration(cfg.Poll)); err != nil {
			return err
		}
	}
}
func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func prepareConfig(cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	for _, path := range []*string{&cfg.Directory, &cfg.StateDirectory} {
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path, err = canonical(absolute)
		if err != nil {
			return err
		}
	}
	if cfg.GitHubRepo() == "" {
		return errors.New("GitHub repository required; set github.repo for a local mirror")
	}
	return nil
}
func (e engine) execute(ctx context.Context, a Agent, prompt string) (string, error) {
	for i := 0; ; i++ {
		text, err := a.Execute(ctx, prompt)
		var setup *runner.SetupError
		if err == nil || !errors.As(err, &setup) || errors.Is(err, exec.ErrNotFound) || i >= len(startupRetryDelays) || ctx.Err() != nil {
			return text, err
		}
		delay := startupRetryDelays[i]
		e.log.Warn("Agent failed to start; retrying", "error", err, "delay", delay)
		if sleepErr := e.sleep(ctx, delay); sleepErr != nil {
			return "", errors.Join(err, sleepErr)
		}
	}
}
func (e engine) save(s *State) error { return writeState(e.config, s) }

// Assess independently reviews one issue or PR in either mode. It performs no
// GitHub writes; Town applies the decision and closing authority.
func Assess(ctx context.Context, cfg Config, mode string, issue, pr int, log *slog.Logger) (Assessment, error) {
	if err := prepareConfig(&cfg); err != nil {
		return Assessment{}, err
	}
	if mode != "suggest" && mode != "auto" || (issue < 0) || (pr < 0) || issue*pr != 0 {
		return Assessment{}, errors.New("invalid simplifier target or mode")
	}
	if log == nil {
		log = slog.Default()
	}
	unlock, err := lockConfig(cfg)
	if err != nil {
		return Assessment{}, err
	}
	defer unlock()
	progress := observe(ctx)
	progress(Progress{Phase: "loading", Task: "Refreshing repository and target"})
	base := checkout{config: cfg}
	if err = base.open(ctx); err != nil {
		return Assessment{}, err
	}
	head, err := base.head(ctx)
	if err != nil {
		return Assessment{}, err
	}
	source := githubClient{cfg}
	worktree, revision, target := base, head, any(nil)
	if issue > 0 {
		var i Issue
		if i, err = source.issue(ctx, issue); err != nil {
			return Assessment{}, err
		}
		if worktree, err = base.itemWorktree(ctx, fmt.Sprintf("issue-%d", issue), head); err != nil {
			return Assessment{}, err
		}
		target = i
	} else {
		var p Pull
		if p, err = source.pull(ctx, pr); err != nil {
			return Assessment{}, err
		}
		if p.Head.SHA == "" {
			return Assessment{}, errors.New("GitHub omitted the pull request head")
		}
		if revision, err = base.fetchItem(ctx, fmt.Sprintf("refs/pull/%d/head", pr), p.Head.SHA); err != nil {
			return Assessment{}, err
		}
		if worktree, err = base.itemWorktree(ctx, fmt.Sprintf("pr-%d", pr), revision); err != nil {
			return Assessment{}, err
		}
		target = p
	}
	wc := worktree.config
	progress(Progress{Phase: "assessing", Task: "Checking complexity, value, and simpler alternatives"})
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Timeout))
	defer cancel()
	text, err := engine{config: wc, log: log, sleep: pause, observe: observe(ctx)}.execute(ctx, agentProcess{wc, log}, assessmentPrompt(mode, target))
	if err != nil {
		return Assessment{}, err
	}
	if err = worktree.verify(ctx, revision); err != nil {
		return Assessment{}, err
	}
	progress(Progress{Phase: "reporting", Task: "Returning a bounded simplification assessment"})
	return parseAssessment(text)
}
func (e engine) scan(ctx context.Context, s *State, force bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, p := range s.Proposals {
		if p.Status != "posting" {
			continue
		}
		e.observe(Progress{Phase: "reconciling", Task: "Checking an interrupted simplification issue"})
		issues, err := e.source.issues(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, i := range issues {
			if strings.Contains(i.Body, marker(p.RequestID)) {
				p.Status, p.URL = "submitted", i.URL
				found = true
				break
			}
		}
		if !found {
			return errors.New("issue creation outcome is unknown; no marker visible yet, refusing to repost")
		}
	}
	if err := e.save(s); err != nil {
		return err
	}
	if !force && !time.Now().Before(s.NextScan) {
		return nil
	}
	e.observe(Progress{Phase: "loading", Task: "Refreshing repository and issue history"})
	base := checkout{config: e.config}
	if err := base.open(ctx); err != nil {
		return err
	}
	commit, err := base.head(ctx)
	if err != nil {
		return err
	}
	if commit == s.LastCommit && len(s.Proposals) > 0 {
		s.NextScan = time.Now().Add(time.Duration(e.config.Poll))
		return e.save(s)
	}
	issues, err := e.source.issues(ctx)
	if err != nil {
		return err
	}
	snapshot, err := json.Marshal(issues)
	if err != nil {
		return err
	}
	instructions, err := instructionFiles(ctx, e.config)
	if err != nil {
		return err
	}
	e.observe(Progress{Phase: "scanning", Task: "Looking for disproportionate complexity and low-value subsystems"})
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.config.Timeout))
	defer cancel()
	snapshotText := string(snapshot)
	if len(snapshotText) > 512<<10 {
		snapshotText = strings.ToValidUTF8(snapshotText[:512<<10], "�") + "\n…issue history truncated by simplifier-bot…"
	}
	text, err := e.execute(ctx, agentProcess{e.config, e.log}, instructions+scanPrompt(snapshotText, e.config.MaxProposals))
	if err != nil {
		return err
	}
	if err = base.verify(ctx, commit); err != nil {
		return err
	}
	proposals, summary, err := parseScan(text, e.config.MaxProposals)
	if err != nil {
		return err
	}
	e.observe(Progress{Phase: "reviewing", Task: summary})
	for i := range proposals {
		id, err := requestID()
		if err != nil {
			return err
		}
		proposals[i].RequestID = id
		proposals[i].Status = "dry_run"
		if !e.config.DryRun {
			proposals[i].Status = "pending"
		}
		s.Proposals = append(s.Proposals, &proposals[i])
	}
	s.LastCommit = commit
	s.NextScan = time.Now().Add(time.Duration(e.config.Poll))
	if err = e.save(s); err != nil {
		return err
	}
	if e.config.DryRun {
		return nil
	}
	for _, p := range s.Proposals {
		if p.Status != "pending" {
			continue
		}
		p.Status = "posting"
		if err = e.save(s); err != nil {
			return err
		}
		e.observe(Progress{Phase: "filing", Task: "Filing " + p.Title})
		created, err := e.source.create(ctx, p, commit)
		if err != nil {
			return err
		}
		p.Status, p.URL = "submitted", created.URL
		if err = e.save(s); err != nil {
			return err
		}
	}
	return nil
}
func instructionFiles(_ context.Context, cfg Config) (string, error) {
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
	return strings.Join(sections, "\n\n") + "\n\n", nil
}

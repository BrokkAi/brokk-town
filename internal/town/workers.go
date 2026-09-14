package town

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

type BotWorkers struct {
	Root         string
	Store        *Store
	GitHub       GitHub
	BotCommands  map[Role]string
	remoteURL    func(string) string
	executeAgent func(context.Context, *Town, sessionTree, string, *slog.Logger, string) (string, error)
}

func (b *BotWorkers) Run(ctx context.Context, t *Town, r Role, observe func(Progress), log *slog.Logger) (RunResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	result := RunResult{}
	t = clone(t)
	t.Config = t.Config.ForRole(r)
	dir, state := Workspace(b.Root, t.ID, r)
	remote := b.remote(t.Config.Repo)
	agent, err := agentConfig(ctx, t.Config, b.Root)
	if err != nil {
		return result, err
	}
	t.Config.Agent = agent
	if profile, overridden := t.Config.BotAgents[r]; overridden {
		// Nested certification/repair sessions reuse this run's prepared command
		// and environment, including any resolved registry distribution values.
		profile.Agent = agent
		t.Config.BotAgents[r] = profile
	}
	switch r {
	case Bug:
		return result, b.runBot(ctx, t, r, agent, dir, state, remote, 0, observe, log)
	case Feature:
		return result, b.runBot(ctx, t, r, agent, dir, state, remote, 0, observe, log)
	case Issue:
		if task := nextTask(t, Issue, "fixes"); task != nil {
			result.PR = task.Number
			return result, b.repair(ctx, t, task, observe, log)
		}
		branch := t.Config.Branch
		if branch == "" {
			branch = "master"
		}
		runErr := b.runBot(ctx, t, r, agent, dir, state, remote, 0, observe, log)
		saved, readErr := readIssueExternalState(state, remote, branch, dir, t.Config.Repo)
		if readErr != nil {
			return result, readErr
		}
		result.Owned = issueOwnership(saved, t.Config.Repo)
		return result, runErr
	case Review:
		task := nextTask(t, Review, "queued")
		if task == nil {
			return result, nil
		}
		result.PR = task.Number
		if err := b.runBot(ctx, t, r, agent, dir, state, remote, task.Number, observe, log); err != nil {
			return result, err
		}
		branch := t.Config.Branch
		if branch == "" {
			branch = "master"
		}
		saved, err := readReviewExternalState(state, remote, branch, dir, t.Config.Repo)
		if err != nil {
			return result, err
		}
		if saved == nil {
			return result, fmt.Errorf("review did not produce a durable result")
		}
		complete := false
		for _, job := range saved.Jobs {
			if job.PR.Number != task.Number || job.DryRun {
				continue
			}
			if job.PR.Head.SHA == task.Head && job.PR.Base.SHA == task.Base && job.Status == "submitted" {
				complete = true
			}
		}
		if !complete {
			return result, fmt.Errorf("no completed review for the exact base/head revision")
		}
		observe(Progress{"certifying", "Checking all outstanding findings on this revision"})
		result.Audit, err = b.certify(ctx, t, task, reviewKnownFindings(saved), log)
		return result, err
	case Release:
		return result, b.runBot(ctx, t, r, agent, dir, state, remote, 0, observe, log)
	}
	return result, fmt.Errorf("unsupported worker %s", r)
}

func (b *BotWorkers) runBot(ctx context.Context, t *Town, role Role, agent runner.AgentConfig, dir, state, remote string, pr int, observe func(Progress), log *slog.Logger) error {
	bot, err := b.externalBot(ctx, role)
	if err != nil {
		return err
	}
	log.Info("Using external bot", "role", string(role), "version", bot.version)
	observe(Progress{Phase: "starting", Task: "Using " + string(role) + "-bot " + bot.version})
	config, err := writeExternalConfig(state, agent, bot, t, dir, remote, pr)
	if err != nil {
		return err
	}
	defer os.Remove(config)
	runErr := runExternalBot(ctx, bot, config, observe, log)
	verifyErr := bot.verify(ctx)
	return errors.Join(runErr, verifyErr)
}
func nextTask(t *Town, role Role, stage string) *Task {
	tasks := []*Task{}
	for _, task := range t.Tasks {
		if task.Kind == "pr" && task.House == role && task.Stage == stage && !task.Blocked && !task.RetryAt.After(time.Now()) {
			tasks = append(tasks, task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Number < tasks[j].Number })
	if len(tasks) > 0 {
		return tasks[0]
	}
	return nil
}

func (b *BotWorkers) remote(repo string) string {
	if b.remoteURL != nil {
		return b.remoteURL(repo)
	}
	return "https://github.com/" + repo + ".git"
}
func (b *BotWorkers) agent(ctx context.Context, t *Town, tree sessionTree, role string, log *slog.Logger, prompt string) (string, error) {
	t = clone(t)
	t.Config = t.Config.ForRole(Role(role))
	if b.executeAgent != nil {
		return b.executeAgent(ctx, t, tree, role, log, prompt)
	}
	return b.runAgent(ctx, t, tree, role, log, prompt)
}

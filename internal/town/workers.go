package town

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	bugbot "github.com/BrokkAi/bug-bot"
	issuebot "github.com/BrokkAi/issue-bot"
	releasebot "github.com/BrokkAi/release-bot"
	reviewbot "github.com/BrokkAi/review-bot"
)

type BotWorkers struct {
	Root         string
	Store        *Store
	GitHub       GitHub
	remoteURL    func(string) string
	executeAgent func(context.Context, *Town, sessionTree, string, *slog.Logger, string) (string, error)
	issueMu      sync.Mutex // Serialize local job reads/imports and explicit retries.
}

func (b *BotWorkers) Run(ctx context.Context, t *Town, r Role, observe func(Progress), log *slog.Logger) (result RunResult, err error) {
	if r == Issue {
		// Durable outcomes matter even after cancellation or agent setup failure.
		defer func() { err = errors.Join(err, b.SyncIssues(t)) }()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	dir, state := Workspace(b.Root, t.ID, r)
	remote := "https://github.com/" + t.Config.Repo + ".git"
	agent, err := agentConfig(ctx, t.Config, b.Root)
	if err != nil {
		return result, err
	}
	t = clone(t)
	t.Config.Agent = agent
	switch r {
	case Bug:
		c := bugbot.DefaultConfig()
		c.Remote = remote
		c.Branch = t.Config.Branch
		c.Directory = dir
		c.StateDirectory = state
		c.Agent = agent
		c.GitHub.Repo = t.Config.Repo
		c.Verify = t.Config.Verify
		ctx = bugbot.WithProgress(ctx, func(p bugbot.Progress) { observe(Progress{p.Phase, p.Task}) })
		return result, bugbot.Run(ctx, c, log, true)
	case Issue:
		if task := nextTask(t, Issue, "fixes"); task != nil {
			result.PR = task.Number
			return result, b.repair(ctx, t, task, observe, log)
		}
		c := b.issueConfig(t)
		c.Agent = agent
		ctx = issuebot.WithProgress(ctx, func(p issuebot.Progress) { observe(Progress{p.Phase, p.Task}) })
		return result, issuebot.Run(ctx, c, log, true)
	case Review:
		task := nextTask(t, Review, "queued")
		if task == nil {
			return result, nil
		}
		result.PR = task.Number
		c := reviewbot.DefaultConfig()
		c.Remote = remote
		c.Branch = t.Config.Branch
		c.Directory = dir
		c.StateDirectory = state
		c.Agent = agent
		c.GitHub.Repo = t.Config.Repo
		c.PR = task.Number
		c.Verify = t.Config.Verify
		ctx = reviewbot.WithProgress(ctx, func(p reviewbot.Progress) { observe(Progress{p.Phase, p.Task}) })
		if err := reviewbot.Run(ctx, c, log, true); err != nil {
			return result, err
		}
		saved, err := reviewbot.ReadState(c)
		if err != nil {
			return result, err
		}
		if saved == nil {
			return result, fmt.Errorf("review did not produce a durable result")
		}
		complete := false
		known := map[string]string{}
		for _, job := range saved.Jobs {
			if job.PR.Number != task.Number || job.DryRun {
				continue
			}
			if job.PR.Head.SHA == task.Head && job.PR.Base.SHA == task.Base && job.Status == "submitted" {
				complete = true
			}
			for _, candidate := range job.Candidates {
				if candidate.Verdict == "invalid" {
					continue
				}
				id := Key(candidate.Finding.Path + candidate.Finding.Title + candidate.Finding.Trigger)
				known[id] = fmt.Sprintf("%s: %s\n%s\nTrigger: %s\nEvidence: %s\nVerifier: %s", candidate.Finding.Path, candidate.Finding.Title, candidate.Finding.Explanation, candidate.Finding.Trigger, strings.Join(candidate.Finding.Evidence, "; "), candidate.Reason)
			}
		}
		if !complete {
			return result, fmt.Errorf("no completed review for the exact base/head revision")
		}
		observe(Progress{"certifying", "Checking all outstanding findings on this revision"})
		result.Audit, err = b.certify(ctx, t, task, known, log)
		return result, err
	case Release:
		c := releasebot.DefaultConfig()
		c.Remote = remote
		c.Branch = t.Config.Branch
		c.Directory = dir
		c.StateDirectory = state
		c.Agent = agent
		c.GitHub.Repo = t.Config.Repo
		c.Verify = t.Config.Verify
		ctx = releasebot.WithProgress(ctx, func(p releasebot.Progress) { observe(Progress{p.Phase, p.Task}) })
		return result, releasebot.Run(ctx, c, log, true, false)
	}
	return result, fmt.Errorf("unsupported worker %s", r)
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
	if b.executeAgent != nil {
		return b.executeAgent(ctx, t, tree, role, log, prompt)
	}
	return b.runAgent(ctx, t, tree, role, log, prompt)
}

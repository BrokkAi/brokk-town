package town

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
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
}

func (b *BotWorkers) Run(ctx context.Context, t *Town, r Role, observe func(Progress), log *slog.Logger) (RunResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	result := RunResult{}
	dir, state := Workspace(b.Root, t.ID, r)
	remote := "https://github.com/" + t.Config.Repo + ".git"
	agent := agentConfig(t.Config)
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
		c := issuebot.DefaultConfig()
		c.Remote = remote
		c.Branch = t.Config.Branch
		c.Directory = dir
		c.StateDirectory = state
		c.Agent = agent
		c.GitHub.Repo = t.Config.Repo
		c.Draft = false
		c.Verify = t.Config.Verify
		ctx = issuebot.WithProgress(ctx, func(p issuebot.Progress) { observe(Progress{p.Phase, p.Task}) })
		err := issuebot.Run(ctx, c, log, true)
		saved, readErr := issuebot.ReadState(c)
		if readErr != nil {
			return result, readErr
		}
		result.Owned = map[int]Ownership{}
		if saved != nil {
			for _, job := range saved.Jobs {
				if job.Status != "submitted" {
					continue
				}
				u, e := url.Parse(job.URL)
				if e != nil || u.Host != "github.com" {
					continue
				}
				parts := strings.Split(strings.Trim(u.Path, "/"), "/")
				if len(parts) != 4 || !strings.EqualFold(strings.Join(parts[:2], "/"), t.Config.Repo) || parts[2] != "pull" {
					continue
				}
				n, e := strconv.Atoi(parts[3])
				if e == nil {
					result.Owned[n] = Ownership{job.Branch, job.Issue.Number}
				}
			}
		}
		return result, err
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
	return runAgent(ctx, t, tree, role, log, prompt)
}

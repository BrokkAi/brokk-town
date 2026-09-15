package town

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	issuebot "github.com/BrokkAi/issue-bot"
)

type BotWorkers struct {
	Root         string
	Store        *Store
	GitHub       GitHub
	BotCommands  map[Role]string
	remoteURL    func(string) string
	executeAgent func(context.Context, *Town, sessionTree, string, *slog.Logger, string) (string, error)
}

func (b *BotWorkers) RetryIssue(t *Town, issue int) error {
	dir, state := Workspace(b.Root, t.ID, Issue)
	cfg := issuebot.DefaultConfig()
	cfg.Remote = b.remote(t.Config.Repo)
	cfg.Branch = t.Config.Branch
	cfg.Directory = dir
	cfg.StateDirectory = state
	cfg.GitHub.Repo = t.Config.Repo
	cfg.GitHub.Host = "github.com"
	cfg.Issue = issue
	return issuebot.Retry(cfg)
}

// ReviewAttemptError means the reviewer did not produce evidence Town can use.
// It is retryable, but it is not a negative review of the pull request.
type ReviewAttemptError struct {
	Complete                   bool
	ExpectedBase, ExpectedHead string
	ReturnedBase, ReturnedHead string
}

func (e *ReviewAttemptError) Error() string {
	return fmt.Sprintf("reviewer returned unusable evidence: complete=%t expected=%s/%s returned=%s/%s",
		e.Complete, e.ExpectedBase, e.ExpectedHead, emptyRevision(e.ReturnedBase), emptyRevision(e.ReturnedHead))
}

func emptyRevision(value string) string {
	if value == "" {
		return "<missing>"
	}
	return value
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
		profile.Agent = agent
		t.Config.BotAgents[r] = profile
	}
	switch r {
	case Bug, Feature, Release:
		workerResult, err := b.runBot(ctx, t, r, agent, dir, state, remote, 0, 0, "", "", observe)
		if err != nil {
			return result, err
		}
		return result, validateWorkerResult(workerResult, r)
	case Issue:
		if task := nextTask(t, Issue, "fixes"); task != nil {
			result.PR = task.Number
			return result, b.repair(ctx, t, task, observe, log)
		}
		task := nextIssue(t)
		if task == nil {
			return result, nil
		}
		workerResult, err := b.runBot(ctx, t, r, agent, dir, state, remote, task.Number, 0, "", "", observe)
		if workerResult.Issue != nil {
			result.Owned = make(map[int]Ownership, len(workerResult.Issue.Owned))
			for _, owned := range workerResult.Issue.Owned {
				if owned.PR > 0 && owned.Issue > 0 {
					result.Owned[owned.PR] = Ownership{Branch: owned.Branch, Issue: owned.Issue}
				}
			}
		}
		if err != nil {
			return result, err
		}
		return result, validateWorkerResult(workerResult, r)
	case Review:
		task := nextTask(t, Review, "queued")
		if task == nil {
			return result, nil
		}
		result.PR = task.Number
		workerResult, err := b.runBot(ctx, t, r, agent, dir, state, remote, 0, task.Number, task.Base, task.Head, observe)
		if err != nil {
			return result, err
		}
		if err = validateWorkerResult(workerResult, r); err != nil {
			return result, err
		}
		review := workerResult.Review
		if !review.Complete || review.ExactBase != task.Base || review.ExactHead != task.Head {
			return result, &ReviewAttemptError{Complete: review.Complete, ExpectedBase: task.Base, ExpectedHead: task.Head, ReturnedBase: review.ExactBase, ReturnedHead: review.ExactHead}
		}
		observe(Progress{"certifying", "Checking all outstanding findings on this revision"})
		result.Audit, err = b.certify(ctx, t, task, review.Findings, log)
		return result, err
	}
	return result, fmt.Errorf("unsupported worker %s", r)
}

func (b *BotWorkers) runBot(ctx context.Context, t *Town, role Role, agent runner.AgentConfig, dir, state, remote string, issue, pr int, base, head string, observe func(Progress)) (workerResult, error) {
	bot, err := b.externalBot(ctx, t.Config, role)
	if err != nil {
		return workerResult{}, err
	}
	observe(Progress{Phase: "starting", Task: "Using " + string(role) + "-bot " + bot.version})
	branch := t.Config.Branch
	request := workerRequest{
		Protocol: workerProtocolVersion, Remote: remote, Branch: branch,
		Directory: dir, StateDirectory: state, Repo: t.Config.Repo, Host: "github.com",
		Agent: agent, Verify: t.Config.Verify, Issue: issue, PR: pr, BaseSHA: base, HeadSHA: head,
	}
	return runWorker(ctx, bot, request, observe, nil)
}

func validateWorkerResult(result workerResult, role Role) error {
	switch role {
	case Issue:
		if result.Issue == nil {
			return errors.New("issue worker omitted its result")
		}
		if result.Review != nil {
			return errors.New("issue worker returned review data")
		}
	case Review:
		if result.Review == nil {
			return errors.New("review worker omitted its result")
		}
		if result.Issue != nil {
			return errors.New("review worker returned issue data")
		}
	default:
		if result.Issue != nil || result.Review != nil {
			return errors.New("worker returned an unexpected typed result")
		}
	}
	return nil
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

func nextIssue(t *Town) *Task {
	tasks := []*Task{}
	for _, task := range t.Tasks {
		if task.Kind == "issue" && task.House == Issue && task.Stage == "queued" && !task.Blocked && !task.RetryAt.After(time.Now()) {
			tasks = append(tasks, task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Number < tasks[j].Number })
	if len(tasks) == 0 {
		return nil
	}
	return tasks[0]
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

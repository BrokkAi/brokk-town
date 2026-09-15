package town

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
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
	// confirmWait and confirmInterval bound how long a repair publish waits
	// for GitHub's pull request head to catch up with the pushed branch.
	// Zero values use the production defaults.
	confirmWait     time.Duration
	confirmInterval time.Duration
	issueMu         sync.Mutex // Serialize durable issue-job reads and imports.
}

func (b *BotWorkers) CanRetryIssue(t *Town, issue int) (bool, error) {
	cfg := b.issueRetryConfig(t, issue)
	state, err := issuebot.ReadState(cfg)
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, nil
	}
	job := state.Jobs[issue]
	return job != nil && !job.ClaimPending && (job.Status == "blocked" || job.Status == "pending"), nil
}

func (b *BotWorkers) RetryIssue(t *Town, issue int) error {
	return issuebot.Retry(b.issueRetryConfig(t, issue))
}

func (b *BotWorkers) issueRetryConfig(t *Town, issue int) issuebot.Config {
	dir, state := Workspace(b.Root, t.ID, Issue)
	cfg := issuebot.DefaultConfig()
	cfg.Remote = b.remote(t.Config.Repo)
	cfg.Branch = t.Config.Branch
	cfg.Directory = dir
	cfg.StateDirectory = state
	cfg.GitHub.Repo = t.Config.Repo
	cfg.GitHub.Host = "github.com"
	cfg.Issue = issue
	return cfg
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

// workerDeadline bounds one external bot dispatch, including time spent
// reconnecting to it after a service restart.
const workerDeadline = 2 * time.Hour

// dispatch identifies what one bot run was asked to do. Adoption rebuilds it
// from the durable run handle so results are applied to the same task.
type dispatch struct {
	issue, pr  int
	base, head string
}

func (b *BotWorkers) Run(ctx context.Context, t *Town, r Role, observe func(Progress), log *slog.Logger) (result RunResult, err error) {
	if r == Issue {
		defer func() { err = errors.Join(err, b.SyncIssues(t)) }()
	}
	deadline := time.Now().Add(workerDeadline)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
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
	var d dispatch
	switch r {
	case Bug, Feature, Release:
	case Issue:
		if task := nextTask(t, Issue, "fixes"); task != nil {
			result.PR = task.Number
			return result, b.repair(ctx, t, task, observe, log)
		}
		task := nextIssue(t)
		if task == nil {
			return result, nil
		}
		d.issue = task.Number
	case Review:
		task := nextTask(t, Review, "queued")
		if task == nil {
			return result, nil
		}
		d = dispatch{pr: task.Number, base: task.Base, head: task.Head}
	default:
		return result, fmt.Errorf("unsupported worker %s", r)
	}
	workerResult, err := b.runBot(ctx, t, r, agent, dir, state, remote, d, deadline, observe)
	return b.complete(ctx, t, r, d, workerResult, err, observe, log)
}

// Adopt resumes a bot process that an earlier service left running. The run
// handle names the process, its socket, and the exact task it was given.
func (b *BotWorkers) Adopt(ctx context.Context, t *Town, r Role, run WorkerRun, observe func(Progress), log *slog.Logger) (result RunResult, err error) {
	if r == Issue {
		defer func() { err = errors.Join(err, b.SyncIssues(t)) }()
	}
	t = clone(t)
	d := dispatch{issue: run.Issue, pr: run.PR, base: run.BaseSHA, head: run.HeadSHA}
	workerResult, err := adoptWorker(ctx, r, run, observe)
	return b.complete(ctx, t, r, d, workerResult, err, observe, log)
}

// complete turns a raw worker outcome into Town's result for one dispatch. It
// is shared by fresh runs and adopted runs so both apply identical rules.
func (b *BotWorkers) complete(ctx context.Context, t *Town, r Role, d dispatch, workerResult workerResult, err error, observe func(Progress), log *slog.Logger) (RunResult, error) {
	result := RunResult{}
	switch r {
	case Bug, Feature, Release:
		if err != nil {
			return result, err
		}
		return result, validateWorkerResult(workerResult, r)
	case Issue:
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
		result.PR = d.pr
		if err != nil {
			return result, err
		}
		if err = validateWorkerResult(workerResult, r); err != nil {
			return result, err
		}
		task := t.Tasks[fmt.Sprintf("pr:%d", d.pr)]
		if task == nil {
			return result, fmt.Errorf("PR #%d left the town while review-bot was working", d.pr)
		}
		review := workerResult.Review
		if !review.Complete || review.ExactBase != d.base || review.ExactHead != d.head || task.Base != d.base || task.Head != d.head {
			return result, &ReviewAttemptError{Complete: review.Complete, ExpectedBase: task.Base, ExpectedHead: task.Head, ReturnedBase: review.ExactBase, ReturnedHead: review.ExactHead}
		}
		observe(Progress{Phase: "certifying", Task: "Checking all outstanding findings on this revision"})
		result.Audit, err = b.certify(ctx, t, task, review.Findings, log)
		return result, err
	}
	return result, fmt.Errorf("unsupported worker %s", r)
}

func (b *BotWorkers) runBot(ctx context.Context, t *Town, role Role, agent runner.AgentConfig, dir, state, remote string, d dispatch, deadline time.Time, observe func(Progress)) (workerResult, error) {
	bot, err := b.externalBot(ctx, t.Config, role)
	if err != nil {
		return workerResult{}, err
	}
	observe(Progress{Phase: "starting", Task: "Using " + string(role) + "-bot " + bot.version})
	branch := t.Config.Branch
	request := workerRequest{
		Protocol: workerProtocolVersion, Remote: remote, Branch: branch,
		Directory: dir, StateDirectory: state, Repo: t.Config.Repo, Host: "github.com",
		Agent: agent, Verify: t.Config.Verify, Issue: d.issue, PR: d.pr, BaseSHA: d.base, HeadSHA: d.head,
	}
	// The handle is committed before the run request so a service that stops
	// at any later point can find the process again.
	started := func(run WorkerRun) error {
		if b.Store == nil {
			return nil
		}
		return b.Store.Update(func(st *State) error {
			town := st.Towns[t.ID]
			if town == nil {
				return errors.New("town disappeared before the worker started")
			}
			town.Workers[role].Run = &run
			return nil
		})
	}
	return runWorker(ctx, bot, request, deadline, observe, started)
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

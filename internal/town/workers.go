package town

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

type BotWorkers struct {
	Root           string
	Store          *Store
	GitHub         GitHub
	Mjolnir        *mjolnir.Catalog
	MjolnirCommand []string
	botCommands    map[Role]string
	poolMu         sync.Mutex
	pool           map[string]*workerProcess
	poolContext    context.Context
	jobsQuery      func(*Town, string, int) (map[int]*issueJobSummary, error)
	remoteURL      func(string) string
	executeAgent   func(context.Context, *Town, sessionTree, string, *slog.Logger, string) (string, error)
	// confirmWait and confirmInterval bound how long a repair publish waits
	// for GitHub's pull request head to catch up with the pushed branch.
	// Zero values use the production defaults.
	confirmWait     time.Duration
	confirmInterval time.Duration
	issueMu         sync.Mutex // Serialize durable issue-job reads and imports.
}

// ReviewAttemptError means the reviewer did not produce evidence Town can use.
// It is retryable, but it is not a negative review of the pull request.
type ReviewAttemptError struct {
	Status, Detail             string
	Complete                   bool
	ExpectedBase, ExpectedHead string
	ReturnedBase, ReturnedHead string
}

func (e *ReviewAttemptError) Error() string {
	if e.Status != "" {
		return fmt.Sprintf("reviewer returned %s for %s/%s: %s", e.Status, e.ExpectedBase, e.ExpectedHead, e.Detail)
	}
	return fmt.Sprintf("reviewer returned unusable evidence: complete=%t expected=%s/%s returned=%s/%s",
		e.Complete, e.ExpectedBase, e.ExpectedHead, emptyRevision(e.ReturnedBase), emptyRevision(e.ReturnedHead))
}

func emptyRevision(value string) string {
	if value == "" {
		return "<missing>"
	}
	return value
}

// workerDeadline bounds one external bot job.
const workerDeadline = 2 * time.Hour

// dispatch identifies the exact work requested of one bot.
type dispatch struct {
	inventorySince  *time.Time
	inventoryBranch string
	issue, pr       int
	base, head      string
	mode            string
	// supersededPR is the closed pull request an issue run starts over from.
	supersededPR int
	// sinceHead and commits are the repo worker's inventory inputs.
	sinceHead string
	commits   []string
	// judged names the Town Hall task a Mayor judgment decides; arrival is
	// the town's description of it. since and until bound a Mayor bulletin.
	judged  string
	arrival json.RawMessage
	since   time.Time
	until   time.Time
}

func (b *BotWorkers) Run(ctx context.Context, t *Town, r Role, observe func(Progress), log *slog.Logger) (result RunResult, err error) {
	if t.Config.ExecutionForRole(r).Managed() {
		managed, e := b.beginManaged(ctx, t, r, observe)
		if e != nil {
			return result, managedSetupError(e)
		}
		ctx = context.WithValue(ctx, managedContextKey{}, managed)
		defer func() {
			if err == nil {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				err = managed.finish(cleanup)
			}
		}()
	}
	if r == Release && t.Config.MergePolicy == "manual" {
		return result, manualReleaseError()
	}
	before := transcriptPaths(b.Root, t.ID, r)
	defer func() { b.recordTranscripts(t.ID, r, before, result, err == nil, log) }()
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
	if r == Issue {
		// Repairs own the private worktrees, so the house that creates them
		// collects the ones no saved intent needs. Cleanup is advisory: a
		// failure here must not stop the town from working.
		if err := b.CollectWorktrees(ctx, t); err != nil {
			log.Warn("could not collect finished worktrees", "error", err)
		}
	}
	agent := t.Config.Agent
	if !t.Config.ExecutionForRole(r).Managed() {
		var e error
		agent, e = agentConfig(ctx, t.Config, b.Root)
		if e != nil {
			return result, e
		}
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
		if task := nextTask(t, Issue, "fixes", time.Now()); task != nil {
			result.PR = task.Number
			return result, b.repair(ctx, t, task, observe, log)
		}
		task := nextIssue(t, time.Now())
		if task == nil {
			return result, nil
		}
		d.issue = task.Number
		d.supersededPR = task.Requeue
	case Review:
		task := nextTask(t, Review, "queued", time.Now())
		if task == nil {
			return result, nil
		}
		d = dispatch{pr: task.Number, base: task.Base, head: task.Head}
	case Hall:
		now := time.Now()
		if task := nextJudgment(t, now); task != nil {
			d = dispatch{mode: "judge", judged: task.ID, arrival: arrivalContext(task)}
			if task.Kind == "pr" {
				d.pr, d.base, d.head = task.Number, task.Base, task.Head
			} else if task.Kind == "issue" {
				d.issue = task.Number
			}
			break
		}
		since, until, due := bulletinWindow(t, now)
		if !due {
			return result, nil
		}
		d = dispatch{mode: "bulletin", since: since, until: until}
	case Simplifier:
		task := nextTask(t, Simplifier, "simplifying", time.Now())
		if task == nil {
			return result, nil
		}
		if task.Kind == "issue" {
			d.issue = task.Number
		} else {
			d = dispatch{pr: task.Number, base: task.Base, head: task.Head}
		}
		d.mode = t.Config.SimplifierModeOrDefault()
	default:
		return result, fmt.Errorf("unsupported worker %s", r)
	}
	workerResult, err := b.runBot(ctx, t, r, agent, dir, state, remote, d, deadline, observe)
	return b.complete(ctx, t, r, d, workerResult, err, observe, log)
}

// Observe runs the repo worker for one repository inventory. The worker is the
// only reader of GitHub's repository state; Town applies what it reports.
func (b *BotWorkers) Observe(ctx context.Context, t *Town, request InventoryRequest, observe func(Progress), log *slog.Logger) (RunResult, error) {
	deadline := time.Now().Add(workerDeadline)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	t = clone(t)
	t.Config = t.Config.ForRole(Repo)
	if t.Config.ExecutionForRole(Repo).Managed() || (t.Workers[Repo] != nil && t.Workers[Repo].Recovery != nil) {
		request.Health = false
	}
	dir, state := Workspace(b.Root, t.ID, Repo)
	agent := runner.AgentConfig{}
	if request.Health {
		// Only the branch-health duty needs an agent, and only that duty may be
		// told how to start one.
		resolved, err := agentConfig(ctx, t.Config, b.Root)
		if err != nil {
			return RunResult{}, err
		}
		agent = resolved
		t.Config.Agent = agent
	}
	d := dispatch{mode: inventoryMode(request.Health && len(agent.Command) > 0), sinceHead: request.SinceHead, commits: request.Commits, inventorySince: request.Since, inventoryBranch: request.Branch}
	result, err := b.runBot(ctx, t, Repo, agent, dir, state, b.remote(t.Config.Repo), d, deadline, observe)
	run, err := repoResult(result, err)
	if err == nil && run.Inventory.Incremental && (request.Since == nil || run.Inventory.StartedAt.IsZero()) {
		return RunResult{}, errors.New("repo worker returned an incremental inventory without its baseline")
	}
	return run, err
}

// inventoryMode names the duty the repo worker is asked for. A confirmation
// read asks for the observation alone.
func inventoryMode(health bool) string {
	if health {
		return "full"
	}
	return "inventory"
}

// repoResult keeps a reported inventory even when the run failed: Town's view
// of the repository must not depend on the health duty that follows it.
func repoResult(worker workerResult, err error) (RunResult, error) {
	result := RunResult{Usage: worker.Usage, CostUSD: worker.CostUSD}
	if worker.Inventory != nil {
		inventory := worker.Inventory.snapshot()
		result.Inventory = &inventory
	}
	result.Health = worker.Health
	if err != nil {
		return result, err
	}
	if result.Inventory == nil {
		return result, errors.New("repo worker omitted its inventory")
	}
	if !SHA(result.Inventory.Head) || !ValidBranch(result.Inventory.Branch) {
		return result, errors.New("repo worker returned an inventory without a usable branch head")
	}
	if !ValidBranchHealth(result.Health) {
		return result, errors.New("repo worker returned an invalid branch health report")
	}
	if worker.Issue != nil || worker.Review != nil || worker.Simplification != nil {
		return result, errors.New("repo worker returned an unexpected typed result")
	}
	return result, nil
}

const manualReleaseTask = "Paused by manual merge policy; Release Bot may merge release-preparation pull requests"

func manualReleaseError() error {
	return errors.New("release-bot is paused by manual merge policy because it may create and merge release-preparation pull requests; choose a Town merge policy that permits automatic merges before starting it")
}

// enforceManualReleasePolicy normalizes every persisted state mutation, including
// config-file loads that bypass the interactive Settings API. Its return value
// tells callers whether a running worker needs cancellation.
func enforceManualReleasePolicy(t *Town) bool {
	if t == nil || t.Config.MergePolicy != "manual" || t.Workers == nil || t.Workers[Release] == nil {
		return false
	}
	w := t.Workers[Release]
	wasActive := w.Enabled || w.Status == "working" || w.Status == "pausing"
	w.Enabled = false
	if w.Status == "working" || w.Status == "pausing" {
		w.Status = "pausing"
	} else {
		w.Status = "paused"
	}
	w.Task = manualReleaseTask
	return wasActive
}

// complete turns a raw worker outcome into Town's result for one dispatch. It
// applies results only to the dispatched task.
func (b *BotWorkers) complete(ctx context.Context, t *Town, r Role, d dispatch, workerResult workerResult, err error, observe func(Progress), log *slog.Logger) (RunResult, error) {
	result := RunResult{Usage: workerResult.Usage, CostUSD: workerResult.CostUSD}
	if r == Release {
		result.Retried = workerResult.retried
	}
	switch r {
	case Bug, Feature, Release:
		if err != nil {
			return result, err
		}
		return result, validateWorkerResult(workerResult, r)
	case Simplifier:
		if d.issue > 0 {
			result.Issue = d.issue
		}
		if d.pr > 0 {
			result.PR = d.pr
		}
		if err != nil {
			return result, err
		}
		if d.issue == 0 && d.pr == 0 {
			if workerResult.Simplification != nil {
				return result, errors.New("repository scan returned an item assessment")
			}
			if workerResult.Issue != nil || workerResult.Review != nil {
				return result, errors.New("repository scan returned an unexpected typed result")
			}
			return result, nil
		}
		if err = validateWorkerResult(workerResult, r); err != nil {
			return result, err
		}
		assessment := workerResult.Simplification
		if assessment == nil || assessment.Mode != d.mode || (d.issue > 0 && assessment.Decision == "") {
			return result, errors.New("simplifier worker returned an assessment for the wrong mode or target")
		}
		result.Simplification = &Simplification{Mode: assessment.Mode, Decision: assessment.Decision, Summary: assessment.Summary, Detail: assessment.Detail}
		return result, nil
	case Hall:
		result.JudgedTask = d.judged
		if err != nil {
			return result, err
		}
		if err = validateWorkerResult(workerResult, r); err != nil {
			return result, err
		}
		switch d.mode {
		case "judge":
			if workerResult.Judgment == nil || workerResult.Bulletin != nil {
				return result, errors.New("mayor judgment returned no decision")
			}
			result.Judgment = &Judgment{Decision: workerResult.Judgment.Decision, Reason: strings.TrimSpace(workerResult.Judgment.Reason)}
		case "bulletin":
			if workerResult.Bulletin == nil || workerResult.Judgment != nil {
				return result, errors.New("mayor bulletin returned no bulletin")
			}
			b := *workerResult.Bulletin
			if !b.Since.Equal(d.since) || !b.Until.Equal(d.until) {
				return result, errors.New("mayor bulletin covered a different window than requested")
			}
			result.Bulletin = &b
		default:
			return result, fmt.Errorf("unsupported mayor duty %q", d.mode)
		}
		return result, nil
	case Issue:
		result.Issue = d.issue
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
			return result, &ReviewAttemptError{Status: review.Status, Detail: review.Detail, Complete: review.Complete, ExpectedBase: task.Base, ExpectedHead: task.Head, ReturnedBase: review.ExactBase, ReturnedHead: review.ExactHead}
		}
		result.Severities = map[string]string{}
		for id, severity := range review.Severities {
			if ValidSeverity(severity) {
				result.Severities["finding:"+id] = severity
			}
		}
		observe(Progress{Phase: "certifying", Task: "Checking all outstanding findings on this revision"})
		result.Audit, err = b.certify(ctx, t, task, review.Findings, result.Severities, log)
		return result, err
	}
	return result, fmt.Errorf("unsupported worker %s", r)
}

func (b *BotWorkers) runBot(ctx context.Context, t *Town, role Role, agent runner.AgentConfig, dir, state, remote string, d dispatch, deadline time.Time, observe func(Progress)) (workerResult, error) {
	bot, err := b.workerBot(ctx, t.ID, role)
	if err != nil {
		return workerResult{}, err
	}
	observe(Progress{Phase: "starting", Task: "Using " + string(role) + "-bot " + bot.version})
	branch := t.Branch()
	request := workerRequest{
		Protocol: workerProtocolVersion, Remote: remote, Branch: branch,
		Directory: dir, StateDirectory: state, Repo: t.Config.Repo, Host: "github.com",
		Agent: agent, Verify: t.Config.Verify, Issue: d.issue, PR: d.pr, BaseSHA: d.base, HeadSHA: d.head,
	}
	if role == Review && t.Config.ExecutionForRole(role).Managed() {
		managed, _ := ctx.Value(managedContextKey{}).(*managedDispatch)
		if managed == nil {
			return workerResult{}, errors.New("managed review has no durable dispatch context")
		}
		path, close, e := startRemoteAgent(ctx, managed, d.head)
		if e != nil {
			return workerResult{}, e
		}
		defer close()
		request.RemoteAgent = path
		request.Agent = runner.AgentConfig{}
	}
	// The house's policy carries its own verification command when it has
	// one, so Verify stays the town default for a house without a policy.
	if policy, configured := t.Config.PolicyForRole(role); configured {
		request.Policy = &policy
		if len(policy.Verify) > 0 {
			request.Verify = policy.Verify
		}
	}
	if role == Simplifier {
		request.Mode = t.Config.SimplifierModeOrDefault()
	}
	if role == Issue {
		request.SupersededPR = d.supersededPR
	}
	if role == Repo {
		// Repo Bot discovers the current default on every inventory. Other
		// houses work on the branch that inventory has already established.
		request.Branch = t.Config.Branch
		request.Mode = d.mode
		request.SinceHead = d.sinceHead
		request.Commits = d.commits
		request.InventorySince, request.InventoryBranch = d.inventorySince, d.inventoryBranch
	}
	if role == Hall {
		request.Mode = d.mode
		request.Arrival = d.arrival
		if d.mode == "bulletin" {
			since, until := d.since, d.until
			request.Since, request.Until = &since, &until
		}
	}
	retry := role == Release && t.Workers[Release] != nil && t.Workers[Release].RetryRequested
	// Record dispatch provenance before sending work so an interrupted write
	// remains uncertain until it is reconciled.
	started := func(run WorkerRun) error {
		if role != Repo || d.mode != "inventory" {
			run.Execution = clone(t.Config.Execution)
			run.Runtime = t.Config.ExecutionRuntimeForRole(role)
		}
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
	return b.runPersistent(ctx, t.ID, bot, request, retry, deadline, observe, started)
}

func validateWorkerResult(result workerResult, role Role) error {
	if result.Usage != nil && (result.Usage.InputTokens < 0 || result.Usage.OutputTokens < 0) {
		return errors.New("worker returned invalid usage")
	}
	if result.CostUSD != nil && *result.CostUSD < 0 {
		return errors.New("worker returned invalid cost")
	}
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
	case Simplifier:
		if result.Simplification == nil {
			return errors.New("simplifier worker omitted its assessment")
		}
		if result.Issue != nil || result.Review != nil {
			return errors.New("simplifier worker returned unexpected typed result")
		}
		s := result.Simplification
		if (s.Mode != "suggest" && s.Mode != "auto") || (s.Decision != "admit" && s.Decision != "decline") || strings.TrimSpace(s.Detail) == "" {
			return errors.New("simplifier worker returned an invalid assessment")
		}
		if len(s.Detail) > 16<<10 || len(s.Summary) > 1024 {
			return errors.New("simplifier worker returned an oversized assessment")
		}
	case Hall:
		if result.Issue != nil || result.Review != nil || result.Simplification != nil {
			return errors.New("mayor worker returned unexpected typed result")
		}
		if j := result.Judgment; j != nil {
			if (j.Decision != "admit" && j.Decision != "decline" && j.Decision != "delay") || strings.TrimSpace(j.Reason) == "" || len(j.Reason) > 2000 {
				return errors.New("mayor worker returned an invalid judgment")
			}
		}
		if b := result.Bulletin; b != nil {
			probe := *b
			probe.At = time.Now()
			if !validBulletin(probe) {
				return errors.New("mayor worker returned an invalid bulletin")
			}
		}
	default:
		if result.Issue != nil || result.Review != nil || result.Simplification != nil || result.Judgment != nil || result.Bulletin != nil {
			return errors.New("worker returned an unexpected typed result")
		}
	}
	return nil
}

// nextTask picks the house's next pull request (or Simplifier intake). A task
// the operator snoozed is skipped until its resume time, like one waiting out
// a failed attempt, so the rest of the queue keeps moving.
func nextTask(t *Town, role Role, stage string, now time.Time) *Task {
	tasks := []*Task{}
	for _, task := range t.Tasks {
		if !t.Config.eligibleUnderPolicy(role, task) {
			continue
		}
		if (task.Kind == "pr" || (role == Simplifier && task.Kind == "issue")) && task.House == role && task.Stage == stage && !task.Blocked && !task.RetryAt.After(now) && !task.Deferred(now) {
			tasks = append(tasks, task)
		}
	}
	// Work that has never been tried goes first. A task whose last attempt
	// failed waits behind every fresh one, so one bad pull request cannot hold
	// the house while the rest of the queue sits untouched.
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Attempts != tasks[j].Attempts {
			return tasks[i].Attempts < tasks[j].Attempts
		}
		return tasks[i].Number < tasks[j].Number
	})
	if len(tasks) > 0 {
		return tasks[0]
	}
	return nil
}

func nextIssue(t *Town, now time.Time) *Task {
	tasks := []*Task{}
	for _, task := range t.Tasks {
		if !t.Config.eligibleUnderPolicy(Issue, task) {
			continue
		}
		if task.Kind == "issue" && task.House == Issue && task.Stage == "queued" && !task.Blocked && !task.RetryAt.After(now) && !task.Deferred(now) {
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

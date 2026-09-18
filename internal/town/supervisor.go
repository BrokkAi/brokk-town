package town

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/brokk-town/internal/harness"
)

// Progress is one worker phase/task snapshot. Seq is the worker protocol event
// number when the observation came from an external bot stream, so Town can
// resume that stream after a restart.
type Progress struct {
	Phase, Task string
	Seq         uint64
}
type RunResult struct {
	// Inventory is the repo worker's observation of the repository.
	Inventory *RepoSnapshot
	// Health is its report on the branch this town covers.
	Health         *BranchHealth
	Owned          map[int]Ownership
	Audit          *Audit
	PR             int
	Issue          int
	Simplification *Simplification
	Usage          *OutcomeUsage
	CostUSD        *float64
	// Retried reports that the release worker accepted the requested attempt
	// budget reset before this run, so the request is consumed.
	Retried bool
}
type Workers interface {
	Run(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error)
	// Observe asks the repo worker for one repository inventory, and for its
	// branch-health duty when the request carries it.
	Observe(context.Context, *Town, InventoryRequest, func(Progress), *slog.Logger) (RunResult, error)
}

// InventoryRequest is what Town needs from one repository observation. Town's
// task graph never crosses the worker protocol: the worker is told only which
// revisions to compare, never what they mean.
type InventoryRequest struct {
	// SinceHead is the branch head Town last observed. The worker names the
	// commits the branch gained beyond it.
	SinceHead string
	// Commits are the revisions Town still needs release ancestry for.
	Commits []string
	// Health asks for the branch-health duty as well. A confirmation read
	// during a merge asks for the observation alone: it must never start a
	// repair agent inside another house's work.
	Health bool
}
type issueStateWorkers interface {
	SyncIssues(*Town) error
}

// Adopter reconnects to a bot process recorded by an earlier service. Workers
// without it leave such runs with an uncertain outcome.
type Adopter interface {
	Adopt(context.Context, *Town, Role, WorkerRun, func(Progress), *slog.Logger) (RunResult, error)
}
type IssueRetrier interface {
	CanRetryIssue(*Town, int) (bool, error)
	RetryIssue(*Town, int) error
}
type Supervisor struct {
	Store     *Store
	GitHub    GitHub
	Workers   Workers
	Publisher IssuePublisher
	Funnels   FunnelRegistry
	Harnesses *harness.Catalog
	// BotVersions reads npm's stable tag for every bot package. When set, Run
	// checks it at start and every BotVersionInterval (default six hours) and
	// offers newer versions to each town as Mayoral decisions or auto-updates.
	BotVersions        func(context.Context) (map[Role]string, error)
	BotVersionInterval time.Duration
	mu                 sync.Mutex
	running            map[string]context.CancelFunc
	retrying           map[string]bool
	reconciling        map[string]chan struct{}
	// repairing names the towns whose repo house holds an agent slot for a
	// branch repair.
	repairing map[string]bool
	wg        sync.WaitGroup
	wake      chan struct{}
	fatal     chan error
	now       func() time.Time
}

func NewSupervisor(store *Store, gh GitHub, workers Workers) *Supervisor {
	publisher, _ := gh.(IssuePublisher)
	registry := FunnelRegistry{}
	if provider, ok := gh.(GitHubFunnelProvider); ok {
		registry[ProviderID("github")] = &GitHubFunnel{Client: provider}
	}
	return &Supervisor{Store: store, GitHub: gh, Workers: workers, Publisher: publisher, Funnels: registry, Harnesses: harness.New(filepath.Join(filepath.Dir(store.path), "harnesses"), store.Snapshot().Demo), running: map[string]context.CancelFunc{}, retrying: map[string]bool{}, reconciling: map[string]chan struct{}{}, wake: make(chan struct{}, 1), fatal: make(chan error, 1), now: time.Now}
}
func (s *Supervisor) fail(err error) {
	if err != nil {
		select {
		case s.fatal <- err:
		default:
		}
	}
}
func (s *Supervisor) update(fn func(*State) error) { s.fail(s.Store.Update(fn)) }
func (s *Supervisor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() { cancel(); s.wg.Wait() }()
	if s.BotVersions != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.watchBotVersions(ctx)
		}()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// Reconnect to bot processes left by the previous service before scheduling
	// anything, so no house is dispatched twice.
	s.adopt(ctx)
	for {
		s.schedule(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-s.fatal:
			return fmt.Errorf("town state persistence failed: %w", err)
		case <-ticker.C:
		case <-s.wake:
		}
	}
}
func (s *Supervisor) schedule(ctx context.Context) {
	state := s.Store.Snapshot()
	if state.Demo {
		return
	}
	s.reviveDelayedBotUpgrades()
	for _, t := range state.Towns {
		if t.Deleted {
			continue
		}
		s.scheduleRequests(ctx, t)
		for _, r := range Roles {
			w := t.Workers[r]
			if !w.Enabled || w.Next.After(s.now()) {
				continue
			}
			if r != Repo && !t.Initialized {
				continue
			}
			key := t.ID + ":" + string(r)
			s.mu.Lock()
			// Recheck eligibility and the current limit under the same lock as
			// capacity edits; a stale scheduling pass cannot undo a reduction.
			eligible, limit := s.Store.dispatchEligibility(t.ID, r, s.now())
			if !eligible || ctx.Err() != nil {
				s.mu.Unlock()
				continue
			}
			_, busy := s.running[key]
			busy = busy || s.retrying[key]
			if r != Repo && s.activeWorkers() >= limit {
				busy = true
			}
			if !busy {
				s.dispatch(ctx, t, r, key, nil)
			}
			s.mu.Unlock()
		}
	}
}

// dispatch reserves key and runs one worker attempt in the background. Explicit
// stops cancel with errStopWorker so the bot process is killed; the plain
// cancellation of service shutdown leaves external processes running for the
// next service to adopt. Caller holds s.mu.
func (s *Supervisor) dispatch(ctx context.Context, t *Town, r Role, key string, adopt *WorkerRun) {
	child, cancel := context.WithCancelCause(ctx)
	s.running[key] = func() { cancel(errStopWorker) }
	s.Store.setActive(s.activeWorkers())
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { cancel(nil); s.releaseWorker(key) }()
		s.execute(child, t, r, adopt)
	}()
}

// adopt dispatches every persisted run handle. It runs once at startup, before
// the first scheduling pass.
func (s *Supervisor) adopt(ctx context.Context) {
	state := s.Store.Snapshot()
	if state.Demo {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range state.Towns {
		for _, r := range AgentRoles {
			w := t.Workers[r]
			if w == nil || w.Run == nil || ctx.Err() != nil {
				continue
			}
			key := t.ID + ":" + string(r)
			if _, busy := s.running[key]; busy {
				continue
			}
			s.dispatch(ctx, t, r, key, w.Run)
		}
	}
}

// activeWorkers counts only scheduled bot roles. Reporters, issue publishing,
// and prompt-free model discovery do not belong to the agent worker pool.
// Caller holds s.mu.
// activeWorkers counts the runs holding an agent slot. The repo house observes
// the repository without one; it holds a slot only while it is repairing the
// branch, which is the only part of its work that starts an agent.
func (s *Supervisor) activeWorkers() int {
	active := 0
	for key := range s.running {
		id, role, ok := strings.Cut(key, ":")
		if !ok || !ValidAgentRole(Role(role)) {
			continue
		}
		if Role(role) == Repo && !s.repairing[id] {
			continue
		}
		active++
	}
	return active
}

// claimRepair reserves an agent slot for one branch repair. The inventory runs
// whatever the answer: a town that cannot spare an agent still has to see its
// repository.
func (s *Supervisor) claimRepair(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, limit := s.Store.dispatchEligibility(id, Repo, s.now()); s.activeWorkers() >= limit {
		return false
	}
	if s.repairing == nil {
		s.repairing = map[string]bool{}
	}
	s.repairing[id] = true
	s.Store.setActive(s.activeWorkers())
	return true
}

func (s *Supervisor) releaseRepair(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.repairing, id)
	s.Store.setActive(s.activeWorkers())
}
func (s *Supervisor) notifyScheduler() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Supervisor) releaseWorker(key string) {
	s.mu.Lock()
	delete(s.running, key)
	s.Store.setActive(s.activeWorkers())
	s.mu.Unlock()
	s.notifyScheduler()
}

// SetCapacity commits before waking scheduling. Existing runs keep their slots
// until cleanup finishes even if the new limit is lower than current usage.
func (s *Supervisor) SetCapacity(limit int) error {
	cfg := ServiceConfig{MaxWorkers: limit}
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	err := s.Store.Update(func(st *State) error { st.ServiceConfig = cfg; return nil })
	s.mu.Unlock()
	if err == nil {
		s.notifyScheduler()
	}
	return err
}

func (s *Supervisor) execute(ctx context.Context, t *Town, r Role, adopt *WorkerRun) {
	now := s.now()
	started := now
	if adopt != nil && !adopt.Started.IsZero() {
		started = adopt.Started
	}
	abandon := false
	if err := s.Store.Update(func(st *State) error {
		current := st.Towns[t.ID]
		w := current.Workers[r]
		if ctx.Err() != nil {
			return context.Canceled
		}
		if adopt != nil {
			// A deleted town's orphan is ended; every other handle is resumed,
			// even for a paused house, because pause lets active work finish.
			// Manual policy is the exception: an older persisted Release Bot
			// must be identified and stopped before it can merge preparation PRs.
			if current.Deleted || (r == Release && current.Config.MergePolicy == "manual") {
				abandon = true
				return nil
			}
			t = clone(current)
			w.Status = "working"
			w.Error = ""
			w.Updated = now
			return nil
		}
		if current.Deleted || !w.Enabled {
			return context.Canceled
		}
		if r != Repo {
			// Migrate older towns to a pinned registry definition at first dispatch.
			// Invalid selections are reported by the worker, not a store failure.
			if cfg, err := s.Prepare(current.Config, AgentSettings{}); err == nil {
				current.Config = cfg
			}
		}
		// Resolve settings at actual dispatch, not from an older scheduler snapshot.
		t = clone(current)
		if r != Repo {
			profile := current.Config.Public().BotAgents[r]
			w.Agent = &profile
			t.Config = t.Config.ForRole(r)
		}
		w.Status = "working"
		w.Error = ""
		w.Updated = now
		return nil
	}); err != nil {
		if !errors.Is(err, context.Canceled) {
			s.fail(err)
		}
		return
	}
	if abandon {
		s.abandon(ctx, t, r, *adopt)
		return
	}
	// Progress and logging never block agent callbacks. A single consumer persists
	// bounded observations; final results are committed separately and never dropped.
	updates := make(chan Log, 64)
	progress := make(chan Progress, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.consumeWorkerChatter(t.ID, r, updates, progress)
	}()
	observe := func(p Progress) { latestProgress(progress, p) }
	log := slog.New(&workerLog{role: r, out: updates, now: s.now})
	var result RunResult
	var err error
	func() {
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("worker panic: %v", v)
			}
		}()
		if adopt != nil {
			result, err = s.adoptRun(ctx, t, r, *adopt, observe, log)
		} else if r == Repo {
			repair := s.claimRepair(t.ID)
			err = s.reconcile(ctx, t, repair, observe, log)
			if repair {
				s.releaseRepair(t.ID)
			}
		} else if r == Review {
			handled, e := s.mergeReady(ctx, t, log)
			err = e
			if !handled && err == nil {
				result, err = s.Workers.Run(ctx, t, r, observe, log)
			}
		} else {
			result, err = s.Workers.Run(ctx, t, r, observe, log)
		}
	}()
	detached := errors.Is(err, errWorkerDetached)
	var unknown *WorkerOutcomeUnknownError
	stopUnconfirmed := adopt != nil && stopRequested(ctx) && errors.As(err, &unknown) && unknown.processRunning
	if err != nil && !detached && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		log.Error("worker attempt failed", "error", err)
	}
	close(updates)
	close(progress)
	<-done
	finished := s.now()
	s.update(func(st *State) error {
		current := st.Towns[t.ID]
		w := current.Workers[r]
		if detached {
			// The bot process is still running. Keep its handle and profile so
			// the next service reconnects instead of scheduling a second run.
			w.Status = "detached"
			w.Phase = "detached"
			w.Task = "Still running; the next town service will reconnect to it"
			w.Updated = s.now()
			return nil
		}
		if stopUnconfirmed {
			// Authentication failed while a stop was pending, so there is no
			// proof that the recorded process ended. Keep the handle for another
			// safe adoption attempt and expose the uncertainty to the operator.
			w.Status = "failed"
			if !w.Enabled {
				w.Status = "paused"
			}
			w.Error = err.Error()
			w.Task = "Could not confirm that the persisted worker stopped"
			w.Updated = s.now()
			return nil
		}
		w.Run = nil
		w.Agent = nil
		w.Status = "waiting"
		w.Updated = s.now()
		w.Next = s.now().Add(time.Duration(current.Config.PollSeconds) * time.Second)
		// Intake is queued work, so Simplifier uses the normal poll cadence.
		// Only discovery scans wait thirty minutes between runs.
		if r == Bug || r == Feature {
			w.Next = s.now().Add(30 * time.Minute)
		}
		if r == Release {
			w.Next = s.now().Add(5 * time.Minute)
			if result.Retried {
				w.RetryRequested = false
			}
		}
		if !w.Enabled {
			w.Status = "paused"
		}
		reviewRetrying := err != nil && r == Review && result.PR > 0
		if reviewRetrying {
			if task := current.Tasks[fmt.Sprintf("pr:%d", result.PR)]; task != nil {
				reviewRetrying = task.Attempts+1 < 5
			}
		}
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil && !reviewRetrying {
			w.Status = "failed"
			w.Error = err.Error()
			w.Task = "Work paused: " + err.Error()
			w.Next = s.now().Add(15 * time.Minute)
			st.Event(t.ID, "error", string(r), "hall", "", string(r)+" needs attention", s.now())
			if r == Repo {
				current.Error = err.Error()
			}
		}
		if r == Review && err != nil && result.PR > 0 {
			// The task owns its backoff. One exhausted PR must not prevent
			// unrelated queued reviews from using the house.
			w.Next = s.now().Add(time.Duration(current.Config.PollSeconds) * time.Second)
			var attempt *ReviewAttemptError
			if errors.As(err, &attempt) {
				// Older workers omit Status; refresh those incomplete results too.
				current.Workers[Repo].Next = time.Time{}
			}
		}
		for n, o := range result.Owned {
			current.Owned[n] = o
		}
		if r == Simplifier {
			var simplifierTask *Task
			if result.Issue > 0 {
				simplifierTask = current.Tasks[fmt.Sprintf("issue:%d", result.Issue)]
			} else if result.PR > 0 {
				simplifierTask = current.Tasks[fmt.Sprintf("pr:%d", result.PR)]
			}
			if simplifierTask != nil {
				applySimplification(st, current, simplifierTask, result.Simplification, err, s.now())
			}
		}
		if result.PR > 0 {
			task := current.Tasks[fmt.Sprintf("pr:%d", result.PR)]
			if task != nil {
				if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
					task.Attempts++
					task.RetryAt = s.now().Add(15 * time.Minute)
					limit := 3
					if r == Review {
						limit = 5
					}
					task.Blocked = task.Attempts >= limit
					if task.Blocked {
						task.Detail = err.Error()
						current.RecordOutcome(OutcomeRecord{ID: fmt.Sprintf("blocked:%s:%d", task.ID, task.Attempts), At: s.now(), Class: "outcome", Kind: "blocked", Status: "blocked", Role: r, TaskID: task.ID, Revision: task.Head, URL: task.URL, Detail: task.Detail})
					} else if r == Review {
						task.Detail = fmt.Sprintf("Reviewer attempt %d of %d did not complete; retry scheduled.", task.Attempts, limit)
						w.Task = task.Detail
						w.Error = ""
					} else {
						task.Detail = err.Error()
					}
				} else if result.Audit != nil && task.Head == result.Audit.Head && task.Base == result.Audit.Base && task.Description == result.Audit.Description {
					task.Audit = result.Audit
					if task.Concerns == nil {
						task.Concerns = map[string]string{}
					}
					for _, f := range result.Audit.Findings {
						task.Concerns[f.ID] = f.Detail
					}
					task.Attempts = 0
					task.RetryAt = time.Time{}
					task.Detail = result.Audit.Summary
					switch result.Audit.Verdict {
					case "clean":
						st.Move(current, task, "ready", Review, "Review complete: ready for merge checks", s.now())
					case "changes_needed":
						if task.External {
							task.Stage = "awaiting_mayor"
							task.House = Hall
							task.MayoralDecision = "pending"
							task.Detail = "Review found changes are needed in this external PR. Decide whether Town should review it again or decline it."
							st.Event(t.ID, "decision", "review", "hall", task.ID, "External PR needs a Mayoral decision: "+task.Title, s.now())
						} else {
							st.Move(current, task, "fixes", Issue, "Review feedback delivered to issue-bot", s.now())
						}
					default:
						task.Stage = "inconclusive"
						task.Blocked = true
					}
				}
			}
		}
		if ValidAgentRole(r) {
			taskID, revision := outcomeAttemptTask(t, r, result)
			status, detail := "attempted", "Worker attempt completed"
			if err != nil && !errors.Is(err, context.Canceled) {
				status, detail = "blocked", err.Error()
			} else if errors.Is(err, context.Canceled) {
				status, detail = "abandoned", "Worker attempt was canceled"
			}
			attemptID := fmt.Sprintf("attempt:%s:%d:%s", r, started.UnixNano(), taskID)
			current.RecordOutcome(OutcomeRecord{ID: attemptID, At: finished, Class: "attempt", Kind: "worker_attempt", Status: status, Role: r, TaskID: taskID, Revision: revision, Detail: detail, ElapsedMS: elapsedMillis(started, finished), Usage: result.Usage, CostUSD: result.CostUSD})
			if status == "abandoned" {
				current.RecordOutcome(OutcomeRecord{ID: "abandoned:" + attemptID, At: finished, Class: "outcome", Kind: "abandoned", Status: "abandoned", TaskID: taskID, Revision: revision, Detail: detail, ElapsedMS: elapsedMillis(started, finished)})
			}
		}
		if r != Repo {
			current.Workers[Repo].Next = time.Time{}
		}
		return nil
	})
}

// WorkerChatterInterval bounds how often a worker's own log lines and phase
// changes reach durable state. Each commit clones the whole state, revalidates
// every task, writes and fsyncs the file and its directory under the write lock,
// and pushes a fresh snapshot to every connected client: a chatty agent produces
// lines far faster than that, and several towns of them make the daemon
// I/O-bound. Coalescing costs at most this much staleness on a busy worker, and
// nothing at all on a quiet one.
var WorkerChatterInterval = 2 * time.Second

// consumeWorkerChatter commits one worker's observations, coalescing bursts. The
// first line after a quiet moment is committed immediately so the operator sees
// work start; further lines within the interval ride along with it. Everything
// buffered is committed before the consumer returns, so no observation is lost
// and the final result still commits after all of them.
func (s *Supervisor) consumeWorkerChatter(id string, r Role, updates <-chan Log, progress <-chan Progress) {
	var pending []Log
	var latest *Progress
	flush := func() {
		if len(pending) == 0 && latest == nil {
			return
		}
		logs, phase := pending, latest
		pending, latest = nil, nil
		s.update(func(st *State) error {
			w := st.Towns[id].Workers[r]
			w.Logs = append(w.Logs, logs...)
			if len(w.Logs) > 100 {
				w.Logs = w.Logs[len(w.Logs)-100:]
			}
			if phase != nil {
				w.Phase = phase.Phase
				w.Task = phase.Task
				w.Updated = s.now()
				if phase.Seq > 0 && w.Run != nil {
					w.Run.Seq = phase.Seq
				}
			}
			return nil
		})
	}
	ticker := time.NewTicker(WorkerChatterInterval)
	defer ticker.Stop()
	// quiet means nothing has been committed during the current interval, so the
	// next observation is worth committing at once.
	quiet := true
	u, pch := updates, progress
	for u != nil || pch != nil {
		select {
		case l, ok := <-u:
			if !ok {
				u = nil
				continue
			}
			pending = append(pending, l)
			if len(pending) > 100 {
				pending = pending[len(pending)-100:]
			}
		case p, ok := <-pch:
			if !ok {
				pch = nil
				continue
			}
			observation := p
			latest = &observation
		case <-ticker.C:
			quiet = len(pending) == 0 && latest == nil
			flush()
			continue
		}
		if quiet {
			quiet = false
			flush()
		}
	}
	flush()
}

// latestProgress publishes one observation on a single-slot channel, replacing
// an observation the consumer has not taken yet. The pending item is already
// stale, so keeping it instead of the new one would leave a phase the worker
// has already left behind on display until the run ends.
func latestProgress(ch chan Progress, p Progress) {
	select {
	case ch <- p:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- p:
	default:
	}
}

func outcomeAttemptTask(t *Town, role Role, result RunResult) (string, string) {
	if role == Simplifier && result.Issue > 0 {
		id := fmt.Sprintf("issue:%d", result.Issue)
		if task := t.Tasks[id]; task != nil {
			return id, task.Head
		}
		return id, ""
	}
	if result.PR > 0 {
		id := fmt.Sprintf("pr:%d", result.PR)
		if task := t.Tasks[id]; task != nil {
			return id, task.Head
		}
		return id, ""
	}
	if role == Issue {
		var selected *Task
		for _, task := range t.Tasks {
			if task.Kind == "issue" && task.House == Issue && (task.Stage == "queued" || task.Stage == "blocked") && (selected == nil || task.Number < selected.Number) {
				selected = task
			}
		}
		if selected != nil {
			return selected.ID, selected.Head
		}
	}
	return "", ""
}

func applySimplification(st *State, t *Town, task *Task, assessment *Simplification, runErr error, now time.Time) {
	if task.Kind == "issue" {
		task.Attempts++
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			task.Attempts--
			return
		}
		task.RetryAt = now.Add(15 * time.Minute)
		task.Blocked = task.Attempts >= 3
		task.Detail = runErr.Error()
		if task.Blocked {
			st.Event(t.ID, "error", string(Simplifier), "hall", task.ID, "Simplifier blocked: "+task.Title, now)
		}
		return
	}
	if assessment == nil {
		// A repository scan has no task-bound assessment; simplify-intake tasks
		// always return one and fail validation before reaching here.
		return
	}
	task.Simplification = assessment
	task.Attempts = 0
	task.RetryAt = time.Time{}
	task.Blocked = false
	task.Detail = assessment.Detail
	if assessment.Summary != "" {
		task.Detail = assessment.Summary + "\n\n" + assessment.Detail
	}
	if assessment.Mode == "suggest" {
		task.Stage = "awaiting_mayor"
		task.House = Hall
		task.MayoralDecision = "pending"
		st.Event(t.ID, "decision", string(Simplifier), "hall", task.ID, "Simplifier advises the Mayor on: "+task.Title, now)
		return
	}
	task.MayoralDecision = ""
	if assessment.Decision == "decline" {
		task.Stage = "declined"
		task.House = Hall
		if task.Kind == "issue" {
			task.Detail = "Simplifier declined this work. Town is closing the issue."
			t.Workers[Repo].Next = time.Time{}
			st.Event(t.ID, "decision", string(Simplifier), string(Repo), task.ID, "Simplifier declined: "+task.Title, now)
			return
		}
		task.Detail = "Simplifier declined this pull request. Town will not review it."
		st.Event(t.ID, "decision", string(Simplifier), "outside", task.ID, "Simplifier declined: "+task.Title, now)
		return
	}
	target := Review
	if task.Kind == "issue" {
		target = Issue
	}
	st.Move(t, task, "queued", target, "Simplifier admitted: "+task.Title, now)
	t.Workers[target].Next = time.Time{}
}

// adoptRun resumes a persisted external run when the workers support it.
// Otherwise the outcome is recorded as uncertain and the handle is dropped.
func (s *Supervisor) adoptRun(ctx context.Context, t *Town, r Role, run WorkerRun, observe func(Progress), log *slog.Logger) (RunResult, error) {
	adopter, ok := s.Workers.(Adopter)
	if !ok {
		return RunResult{PR: run.PR}, &WorkerOutcomeUnknownError{Bot: run.Bot, Version: run.Version, Reason: "was running when the service restarted and this service cannot reconnect to it"}
	}
	return adopter.Adopt(ctx, t, r, run, observe, log)
}

// abandon ends a persisted bot process that no longer has authority to run and
// drops its handle. Stop semantics apply after the worker identity is proven.
func (s *Supervisor) abandon(ctx context.Context, t *Town, r Role, run WorkerRun) {
	if adopter, ok := s.Workers.(Adopter); ok {
		stopped, cancel := context.WithCancelCause(ctx)
		cancel(errStopWorker)
		_, err := adopter.Adopt(stopped, t, r, run, func(Progress) {}, slog.New(&workerLog{role: r, out: nil, now: s.now}))
		if err != nil && !errors.Is(err, context.Canceled) {
			s.update(func(st *State) error {
				worker := st.Towns[t.ID].Workers[r]
				worker.Error = fmt.Sprintf("Could not stop persisted %s safely: %v", run.Bot, err)
				worker.Updated = s.now()
				return nil
			})
			return
		}
	}
	s.update(func(st *State) error {
		current := st.Towns[t.ID]
		worker := current.Workers[r]
		worker.Run = nil
		worker.Agent = nil
		worker.Error = ""
		worker.Status = "waiting"
		if !worker.Enabled {
			worker.Status = "paused"
		}
		finished := s.now()
		worker.Updated = finished
		current.RecordOutcome(OutcomeRecord{ID: fmt.Sprintf("abandoned:orphan:%s:%d", r, run.Started.UnixNano()), At: finished, Class: "outcome", Kind: "abandoned", Status: "abandoned", TaskID: workerRunTask(run), Revision: run.HeadSHA, Detail: "Orphaned worker was stopped after its town was deleted", ElapsedMS: elapsedMillis(run.Started, finished)})
		return nil
	})
}

func workerRunTask(run WorkerRun) string {
	if run.PR > 0 {
		return fmt.Sprintf("pr:%d", run.PR)
	}
	if run.Issue > 0 {
		return fmt.Sprintf("issue:%d", run.Issue)
	}
	return ""
}

// reconcileGate serializes repository reconciliation per town. The repo worker
// and the merge path both reconcile, and each one commits a complete inventory:
// two overlapping passes could commit in the order they finished rather than the
// order they observed, regressing the recorded head and last release and
// replaying deliveries that were already announced.
//
// It is a one-slot channel rather than a mutex so that a pass waiting its turn
// still answers a stop or a service shutdown: a full inventory of a large
// repository can legitimately take minutes.
// inventorySince is the branch head Town last observed, and only once the town
// has an inventory to compare against.
func inventorySince(t *Town) string {
	if t.Initialized && SHA(t.Head) {
		return t.Head
	}
	return ""
}

// unprovenCommits names the commits Town still needs release ancestry for. The
// worker proves each one; what it means for them to be released is Town's.
func unprovenCommits(t *Town) []string {
	commits := []string{}
	for _, task := range t.Tasks {
		if task.Kind == "commit" && task.Stage != "shipped" && SHA(task.Head) {
			commits = append(commits, task.Head)
		}
	}
	sort.Strings(commits)
	return commits
}

// applyBranchHealth commits the worker's report and announces what changed. A
// published repair is an outcome; a branch that stays red is not announced
// again on every poll.
func applyBranchHealth(st *State, t *Town, health *BranchHealth, now time.Time) {
	if t == nil || health == nil {
		return
	}
	health.At = now
	previous := t.Health
	t.Health = health
	if health.Pushed != "" {
		t.RecordOutcome(OutcomeRecord{ID: "branch-repair:" + health.Pushed, At: now, Class: "outcome", Kind: "branch_repair", Status: "confirmed", Role: Repo, Revision: health.Pushed, Detail: branchHealthTask(health)})
	}
	if previous != nil && previous.State == health.State && previous.Head == health.Head {
		return
	}
	switch health.State {
	case "red", "unrepairable":
		st.Event(t.ID, "error", string(Repo), "hall", "", branchHealthTask(health), now)
	case "repaired":
		st.Event(t.ID, "change", string(Repo), "hall", "", branchHealthTask(health), now)
	case "green":
		if previous != nil && !previous.Healthy() {
			st.Event(t.ID, "report", string(Repo), "hall", "", "Branch checks are passing again", now)
		}
	}
}

// branchHealthTask is the one line the operator reads on the repo house.
func branchHealthTask(health *BranchHealth) string {
	if health == nil {
		return "Repository inventory is current"
	}
	failing := strings.Join(health.Failing, ", ")
	switch health.State {
	case "red":
		return "Branch checks are failing: " + failing
	case "repaired":
		return "Published a repair for " + failing
	case "unrepairable":
		detail := "Branch checks are failing and Repo Bot cannot repair them"
		if health.Detail != "" {
			detail += ": " + health.Detail
		}
		return detail
	case "pending":
		return "Branch checks are still running"
	case "unreported":
		return "Repository inventory is current; the branch reports no checks"
	default:
		return "Repository inventory is current"
	}
}

func (s *Supervisor) reconcileGate(id string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reconciling == nil {
		s.reconciling = map[string]chan struct{}{}
	}
	gate := s.reconciling[id]
	if gate == nil {
		gate = make(chan struct{}, 1)
		s.reconciling[id] = gate
	}
	return gate
}

// reconcile confirms Town's view of the repository from one worker inventory.
// health asks for the branch-health duty as well; a confirmation read during a
// merge does not, because a repair agent must not start inside another house.
func (s *Supervisor) reconcile(ctx context.Context, t *Town, health bool, observe func(Progress), log *slog.Logger) error {
	gate := s.reconcileGate(t.ID)
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-gate }()
	// Read the town again: waiting for the gate can outlast the caller's view,
	// and every step below works from the configuration it commits against.
	t = s.Store.Snapshot().Towns[t.ID]
	if t == nil || t.Deleted {
		return nil
	}
	if workers, ok := s.Workers.(issueStateWorkers); ok {
		if err := workers.SyncIssues(t); err != nil {
			return fmt.Errorf("import issue-bot jobs: %w", err)
		}
	}
	t = s.Store.Snapshot().Towns[t.ID]
	// Funnel synchronization is source-neutral and commits typed health before
	// legacy repository reconciliation. Existing PR/release authority remains on
	// the mature GitHub path while issue intake migrates incrementally.
	if len(t.Config.Funnels) > 0 {
		if err := s.reconcileFunnels(ctx, t.ID); err != nil {
			return err
		}
		t = s.Store.Snapshot().Towns[t.ID]
	}
	result, err := s.Workers.Observe(ctx, t, InventoryRequest{SinceHead: inventorySince(t), Commits: unprovenCommits(t), Health: health}, observe, log)
	if err != nil {
		return err
	}
	if result.Inventory == nil {
		return errors.New("the repo worker returned no inventory")
	}
	remote := *result.Inventory
	// When this repository has configured GitHub funnels, the normalized
	// selector result is the issue inventory. PRs/releases continue through the
	// existing exact-revision path.
	selected := map[int]bool{}
	hasGitHubFunnel := false
	for _, config := range t.Config.Funnels {
		if config.Enabled && config.Provider == ProviderID("github") && strings.EqualFold(config.Location["repository"], t.Config.Repo) {
			hasGitHubFunnel = true
		}
	}
	if hasGitHubFunnel {
		for _, task := range t.Tasks {
			if task.Source != nil && task.Source.Identity.Provider == ProviderID("github") && task.Source.Eligible {
				if n, parseErr := strconv.Atoi(string(task.Source.Identity.Item)); parseErr == nil {
					selected[n] = true
				}
			}
		}
		issues := remote.Issues[:0]
		for _, issue := range remote.Issues {
			if selected[issue.Number] {
				issues = append(issues, issue)
			}
		}
		remote.Issues = issues
	}
	remote.ObservedHeads = map[int]string{}
	for _, task := range t.Tasks {
		if task.Kind == "pr" {
			remote.ObservedHeads[task.Number] = task.Head
		}
	}
	if err := s.Store.Update(func(st *State) error {
		current := st.Towns[t.ID]
		Reconcile(st, current, remote, s.now())
		applyBranchHealth(st, current, result.Health, s.now())
		w := current.Workers[Repo]
		w.Task = branchHealthTask(current.Health)
		w.Phase = "reporting"
		return nil
	}); err != nil {
		return err
	}
	if workers, ok := s.Workers.(issueStateWorkers); ok {
		if err := workers.SyncIssues(s.Store.Snapshot().Towns[t.ID]); err != nil {
			return fmt.Errorf("preserve issue-bot jobs after inventory: %w", err)
		}
	}
	return s.closeDeclinedProposals(ctx, s.Store.Snapshot().Towns[t.ID], remote)
}

// closeDeclinedProposals retires issues after an authorized final decision: the
// Mayor's decline for Town's own proposals, or Simplifier's auto decline for an
// intake issue. Closing is idempotent, so an issue that is already closed is
// skipped and an uncertain attempt is retried by the next inventory.
func (s *Supervisor) closeDeclinedProposals(ctx context.Context, t *Town, remote RepoSnapshot) error {
	if t == nil || t.Deleted {
		return nil
	}
	open := map[int]bool{}
	for _, i := range remote.Issues {
		if i.State != "closed" {
			open[i.Number] = true
		}
	}
	numbers := []int{}
	for _, task := range t.Tasks {
		mayorDeclined := !task.External && task.MayoralDecision == "declined"
		simplifierDeclined := task.Simplification != nil && task.Simplification.Mode == "auto" && task.Simplification.Decision == "decline"
		if task.Kind == "issue" && (mayorDeclined || simplifierDeclined) && open[task.Number] {
			numbers = append(numbers, task.Number)
		}
	}
	sort.Ints(numbers)
	var failures error
	for _, n := range numbers {
		id := fmt.Sprintf("issue:%d", n)
		if err := s.GitHub.CloseIssue(ctx, t.Config.Repo, n); err != nil {
			failures = errors.Join(failures, fmt.Errorf("close declined issue #%d: %w", n, err))
			continue
		}
		if err := s.Store.Update(func(st *State) error {
			current := st.Towns[t.ID]
			task := current.Tasks[id]
			if current == nil || task == nil {
				return nil
			}
			mayorDeclined := task.MayoralDecision == "declined"
			simplifierDeclined := task.Simplification != nil && task.Simplification.Mode == "auto" && task.Simplification.Decision == "decline"
			if !mayorDeclined && !simplifierDeclined {
				return nil
			}
			if mayorDeclined {
				task.Detail = "The Mayor declined this proposal. Town closed the issue."
				st.Event(t.ID, "decision", "hall", string(Repo), id, "Declined proposal closed: "+task.Title, s.now())
			} else {
				task.Detail = "Simplifier declined this low-value complex issue. Town closed it."
				st.Event(t.ID, "decision", string(Simplifier), string(Repo), id, "Simplifier closed: "+task.Title, s.now())
			}
			return nil
		}); err != nil {
			return errors.Join(failures, err)
		}
	}
	return failures
}
func (s *Supervisor) Control(id string, role Role, action, taskID string) error {
	if action == "delete" {
		if role != "all" {
			return errors.New("delete applies to the entire town")
		}
		return s.Delete(id)
	}
	decision := action == "admit" || action == "decline" || action == "delay"
	if action != "start" && action != "pause" && action != "stop" && action != "retry" && !decision {
		return errors.New("unknown action")
	}
	if role != "all" && !ValidRole(role) && !(decision && role == Hall) {
		return errors.New("unknown role")
	}
	if action == "retry" && taskID == "" && role == Release {
		return s.retryRelease(id)
	}
	if action == "retry" {
		state := s.Store.Snapshot()
		t := state.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		task := t.Tasks[taskID]
		if task == nil {
			return errors.New("unknown task")
		}
		if task.Kind == "issue" && !state.Demo {
			if _, ok := s.Workers.(IssueRetrier); ok {
				return s.retryIssueTask(id, taskID)
			}
		}
	}
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		if decision {
			task := t.Tasks[taskID]
			if task == nil || task.MayoralDecision != "pending" || task.Stage != "awaiting_mayor" || task.House != Hall {
				return errors.New("task is not awaiting a Mayoral decision")
			}
			if task.Kind == "upgrade" || action == "delay" {
				return st.decideBotUpgrade(t, task, action, s.now())
			}
			if action == "decline" {
				task.MayoralDecision = "declined"
				task.Stage = "declined"
				// Work the town proposed to itself is retired at its source: a
				// declined proposal is closed rather than left open for a bot to
				// pick up again. Outside work is only ignored; Town does not
				// close other people's issues and pull requests.
				if task.Kind == "issue" && !task.External {
					task.Detail = "The Mayor declined this proposal. Town is closing the issue."
					t.Workers[Repo].Next = time.Time{}
					st.Event(id, "decision", "hall", string(Repo), task.ID, "Mayor declined: "+task.Title, s.now())
					return nil
				}
				task.Detail = "The Mayor declined this outside work. Town will not act on it."
				st.Event(id, "decision", "hall", "outside", task.ID, "Mayor declined: "+task.Title, s.now())
				return nil
			}
			task.MayoralDecision = "admitted"
			task.Stage = "queued"
			task.Detail = "The Mayor admitted this work to town."
			target := Review
			if task.Kind == "issue" {
				target = Issue
			}
			task.House = target
			t.Workers[target].Next = time.Time{}
			st.Event(id, "decision", "hall", string(target), task.ID, "Mayor admitted: "+task.Title, s.now())
			return nil
		}
		if action == "retry" {
			task := t.Tasks[taskID]
			if task == nil {
				return errors.New("unknown task")
			}
			resetTaskForRetry(t, task)
			st.Event(id, "control", "operator", string(task.House), task.ID, "retry "+task.ID, s.now())
			return nil
		}
		for _, r := range Roles {
			if role != "all" && role != r {
				continue
			}
			w := t.Workers[r]
			if action == "start" && r == Release && t.Config.MergePolicy == "manual" {
				if role == Release {
					return manualReleaseError()
				}
				w.Enabled = false
				w.Status = "paused"
				w.Task = manualReleaseTask
				continue
			}
			w.Enabled = action == "start"
			if w.Enabled {
				if w.Task == manualReleaseTask {
					w.Task = "Ready when you are"
				}
				w.Next = time.Time{}
				w.Error = ""
				w.Status = "waiting"
			} else if w.Status == "working" && action == "pause" {
				w.Status = "pausing"
			} else {
				w.Status = "paused"
			}
		}
		st.Event(id, "control", "operator", string(role), "", action+" "+string(role), s.now())
		return nil
	})
	if err != nil {
		return err
	}
	if action == "stop" {
		s.mu.Lock()
		for key, cancel := range s.running {
			if strings.HasPrefix(key, id+":") && (role == "all" || key == id+":"+string(role)) {
				cancel()
			}
		}
		s.mu.Unlock()
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// retryRelease asks the next release dispatch to lift the release bot's
// exhausted attempt budget through its worker API. Town never edits the bot's
// private state itself; the worker resets it and the same run resumes the job.
func (s *Supervisor) retryRelease(id string) error {
	return s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		if t.Config.MergePolicy == "manual" {
			return manualReleaseError()
		}
		w := t.Workers[Release]
		if w == nil {
			return errors.New("town has no release house")
		}
		w.RetryRequested = true
		w.Enabled = true
		w.Next = time.Time{}
		w.Error = ""
		w.Status = "waiting"
		w.Task = "Retry requested: the next release run resets the release bot's attempt budget"
		st.Event(id, "control", "operator", string(Release), "", "retry release", s.now())
		return nil
	})
}

func resetTaskForRetry(t *Town, task *Task) {
	if i := t.Intents[task.Number]; i != nil && i.Status == "uncertain" {
		i.Status = "retry" // Explicit operator request; worker rechecks GitHub before any write.
	}
	task.Blocked = false
	task.Attempts = 0
	task.RetryAt = time.Time{}
	if task.Kind == "issue" && task.Stage == "blocked" {
		task.Stage = "queued"
		task.Detail = "Waiting for issue-bot."
		if task.IssueJob != nil {
			task.IssueJob.Status = "pending"
			task.IssueJob.RetryEligible = false
			task.IssueJob.RetryDetail = "Retry requested; issue-bot will recheck GitHub before continuing."
		}
	}
	if task.Stage == "inconclusive" {
		task.Audit = nil
		task.Stage = "queued"
		task.House = Review
	}
	if task.Cycles >= t.Config.MaxCycles {
		task.Cycles = 0
	}
	// Clearing the house's retry delay lets an enabled house pick the task up
	// immediately. Enabled is deliberately untouched: one blocked task is not a
	// reason to start a house the operator paused, which would release every
	// other ready merge and queued review at once.
	w := t.Workers[task.House]
	if w == nil {
		return
	}
	w.Next = time.Time{}
	if !w.Enabled {
		task.Detail = fmt.Sprintf("Ready to retry. %s Bot is paused; start it to run this task.", houseName(task.House))
	}
}

// houseName is the operator-facing name of a house, matching the browser and
// TUI labels.
func houseName(role Role) string {
	if role == Hall {
		return "Town Hall"
	}
	return strings.ToUpper(string(role)[:1]) + string(role)[1:]
}

func (s *Supervisor) retryIssueTask(id, taskID string) error {
	retrier, ok := s.Workers.(IssueRetrier)
	if !ok {
		return errors.New("issue worker durable retry support changed")
	}
	key := id + ":" + string(Issue)
	s.mu.Lock()
	if s.retrying == nil {
		s.retrying = map[string]bool{}
	}
	if s.retrying[key] {
		s.mu.Unlock()
		return errors.New("issue retry is already in progress")
	}
	s.retrying[key] = true
	s.mu.Unlock()

	finish := func() {
		s.mu.Lock()
		delete(s.retrying, key)
		s.mu.Unlock()
		s.notifyScheduler()
	}
	defer finish()

	// Validate the Town identity and issue-bot state before interrupting an
	// active worker. Funnel failures commonly have no issue-bot job at all; in
	// that case Town's own blocked task is the only state that needs resetting.
	s.mu.Lock()
	state := s.Store.Snapshot()
	t := state.Towns[id]
	if t == nil || t.Deleted {
		s.mu.Unlock()
		return errors.New("unknown town")
	}
	task := t.Tasks[taskID]
	if task == nil || task.Kind != "issue" {
		s.mu.Unlock()
		return errors.New("issue task changed while preparing retry")
	}
	durable, err := retrier.CanRetryIssue(t, task.Number)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("inspect issue-bot retry state for issue #%d: %w", task.Number, err)
	}
	if !durable {
		err = s.Store.Update(func(st *State) error {
			current := st.Towns[id]
			if current == nil || current.Deleted {
				return errors.New("unknown town")
			}
			currentTask := current.Tasks[taskID]
			if currentTask == nil || currentTask.Kind != "issue" || currentTask.Number != task.Number {
				return errors.New("issue task changed while committing retry")
			}
			resetTaskForRetry(current, currentTask)
			st.Event(id, "control", "operator", string(currentTask.House), currentTask.ID, "retry "+currentTask.ID, s.now())
			return nil
		})
		s.mu.Unlock()
		return err
	}
	if cancel := s.running[key]; cancel != nil {
		cancel()
	}
	s.mu.Unlock()

	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		_, running := s.running[key]
		s.mu.Unlock()
		if !running {
			break
		}
		select {
		case <-deadline.C:
			return errors.New("timed out waiting for the active issue worker to stop before retry")
		case <-ticker.C:
		}
	}

	// Keep scheduling and town deletion out while issue-bot takes its own state
	// and checkout locks and Town commits the corresponding retry state.
	s.mu.Lock()
	defer s.mu.Unlock()
	state = s.Store.Snapshot()
	t = state.Towns[id]
	if t == nil || t.Deleted {
		return errors.New("unknown town")
	}
	task = t.Tasks[taskID]
	if task == nil || task.Kind != "issue" {
		return errors.New("issue task changed while preparing retry")
	}
	if err := retrier.RetryIssue(t, task.Number); err != nil {
		return fmt.Errorf("reset issue-bot retry state for issue #%d: %w", task.Number, err)
	}
	return s.Store.Update(func(st *State) error {
		current := st.Towns[id]
		if current == nil || current.Deleted {
			return errors.New("unknown town")
		}
		currentTask := current.Tasks[taskID]
		if currentTask == nil || currentTask.Kind != "issue" || currentTask.Number != task.Number {
			return errors.New("issue task changed while committing retry")
		}
		resetTaskForRetry(current, currentTask)
		st.Event(id, "control", "operator", string(currentTask.House), currentTask.ID, "retry "+currentTask.ID, s.now())
		return nil
	})
}
func (s *Supervisor) mergeReady(ctx context.Context, t *Town, log *slog.Logger) (bool, error) {
	numbers := []int{}
	for _, task := range t.Tasks {
		if task.Kind == "pr" && task.Stage == "ready" && !task.Blocked && !task.RetryAt.After(s.now()) {
			numbers = append(numbers, task.Number)
		}
	}
	sort.Ints(numbers)
	for _, n := range numbers {
		task := t.Tasks[fmt.Sprintf("pr:%d", n)]
		if t.Config.MergePolicy == "manual" || (task.External && t.Config.MergePolicy != "all") {
			continue
		}
		if intent := t.Intents[n]; intent != nil && intent.Kind == "merge" && intent.Status != "confirmed" && intent.Status != "retry" {
			p, err := s.GitHub.Pull(ctx, t.Config.Repo, n)
			if err != nil {
				return true, err
			}
			if p.MergedAt != nil {
				return true, s.reconcile(ctx, t, false, func(Progress) {}, log)
			}
			if err := s.block(t.ID, task.ID, "Merge outcome is uncertain. Repo-bot will reconcile it; inspect GitHub before retrying."); err != nil {
				return true, err
			}
			continue
		}
		p, err := s.GitHub.Pull(ctx, t.Config.Repo, n)
		if err != nil {
			return true, err
		}
		if p.MergedAt != nil {
			return true, s.reconcile(ctx, t, false, func(Progress) {}, log)
		}
		gate, err := s.GitHub.Gate(ctx, t.Config.Repo, n)
		if err != nil {
			return true, err
		}
		if !gate.Allows(p, task.Audit) {
			if err := s.Store.Update(func(st *State) error {
				st.Towns[t.ID].Tasks[task.ID].Detail = "Waiting for current review, required checks, approvals, and mergeability."
				return nil
			}); err != nil {
				return true, err
			}
			continue
		}
		discussion, err := s.GitHub.Discussion(ctx, t.Config.Repo, n)
		if err != nil {
			return true, err
		}
		if task.Audit.Discussion != Digest(discussion) || task.Audit.Description != description(p) {
			return true, s.Store.Update(func(st *State) error {
				current := st.Towns[t.ID].Tasks[task.ID]
				current.Audit = nil
				current.Stage = "queued"
				current.Detail = "Discussion or PR description changed; another review is required."
				return nil
			})
		}
		// Recheck metadata adjacent to intent, then submit an expected-head merge.
		fresh, err := s.GitHub.Pull(ctx, t.Config.Repo, n)
		if err != nil {
			return true, err
		}
		if fresh.Head.SHA != p.Head.SHA || fresh.Base.SHA != p.Base.SHA || description(fresh) != description(p) || fresh.State != "open" || fresh.Draft || fresh.Locked {
			continue
		}
		if err = s.Store.Update(func(st *State) error {
			st.Towns[t.ID].Intents[n] = &Intent{Kind: "merge", PR: n, Base: p.Base.SHA, Head: p.Head.SHA, Status: "uncertain", At: s.now()}
			return nil
		}); err != nil {
			return true, err
		}
		sha, err := s.GitHub.Merge(ctx, t.Config.Repo, n, p.Head.SHA)
		if err != nil {
			return true, errors.Join(err, s.block(t.ID, task.ID, "Merge outcome is uncertain: "+err.Error()))
		}
		err = s.Store.Update(func(st *State) error {
			i := st.Towns[t.ID].Intents[n]
			i.Status = "confirmed"
			i.NewHead = sha
			return nil
		})
		if err != nil {
			return true, err
		}
		return true, s.reconcile(ctx, t, false, func(Progress) {}, log)
	}
	return false, nil
}

type workerLog struct {
	role  Role
	out   chan Log
	now   func() time.Time
	attrs []slog.Attr
	group string
}

func (l *workerLog) Enabled(context.Context, slog.Level) bool { return true }
func (l *workerLog) Handle(_ context.Context, r slog.Record) error {
	text := r.Message
	appendAttr := func(a slog.Attr) {
		key := strings.ToLower(a.Key)
		if strings.Contains(key, "token") || strings.Contains(key, "secret") || key == "environment" || key == "command" {
			return
		}
		text += " · " + a.Key + "=" + a.Value.String()
	}
	for _, a := range l.attrs {
		appendAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool { appendAttr(a); return true })
	if len(text) > 4000 {
		text = text[:4000] + "…"
	}
	entry := Log{l.now(), r.Level.String(), text}
	select {
	case l.out <- entry:
		return nil
	default:
	}
	// Logging never blocks an agent callback. The worker's log ring keeps only
	// its newest lines, so a full buffer gives up the oldest pending line rather
	// than the one just produced, which is the one an operator is watching for.
	select {
	case <-l.out:
	default:
	}
	select {
	case l.out <- entry:
	default:
	}
	return nil
}
func (l *workerLog) WithAttrs(a []slog.Attr) slog.Handler {
	copy := *l
	copy.attrs = append(append([]slog.Attr{}, l.attrs...), a...)
	return &copy
}
func (l *workerLog) WithGroup(g string) slog.Handler { copy := *l; copy.group = g; return &copy }
func Workspace(root, id string, r Role) (string, string) {
	base := filepath.Join(root, "towns", Key(id), string(r))
	return filepath.Join(base, "checkout"), filepath.Join(base, "state")
}

func (s *Supervisor) block(id, taskID, detail string) error {
	return s.Store.Update(func(st *State) error {
		task := st.Towns[id].Tasks[taskID]
		task.Blocked = true
		task.Detail = detail
		return nil
	})
}

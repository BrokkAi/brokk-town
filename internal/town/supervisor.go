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
	"unicode/utf8"

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
	Health *BranchHealth
	Owned  map[int]Ownership
	Audit  *Audit
	// Severities are the reviewer's ratings for the audit's evidence IDs.
	Severities     map[string]string
	PR             int
	Issue          int
	Simplification *Simplification
	// Judgment and JudgedTask are Mayor Bot's decision and the arrival it
	// concerns; Bulletin is the bulletin it wrote.
	Judgment   *Judgment
	JudgedTask string
	Bulletin   *Bulletin
	Usage      *OutcomeUsage
	CostUSD    *float64
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

type IssueRetrier interface {
	CanRetryIssue(*Town, int) (bool, error)
	RetryIssue(*Town, int) error
}
type Supervisor struct {
	Store       *Store
	GitHub      GitHub
	Workers     Workers
	Publisher   IssuePublisher
	Funnels     FunnelRegistry
	Harnesses   *harness.Catalog
	mu          sync.Mutex
	running     map[string]context.CancelFunc
	retrying    map[string]bool
	reconciling map[string]chan struct{}
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
	if workers, ok := s.Workers.(*BotWorkers); ok {
		defer workers.Close()
		if err := workers.SyncProcesses(ctx, s.Store.Snapshot()); err != nil {
			return err
		}
	}
	defer func() { cancel(); s.wg.Wait() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// Reconnect to bot processes left by the previous service before scheduling
	// anything, so no house is dispatched twice.
	for {
		if workers, ok := s.Workers.(*BotWorkers); ok {
			if err := workers.SyncProcesses(ctx, s.Store.Snapshot()); err != nil && ctx.Err() == nil {
				return err
			}
		}
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
				s.dispatch(ctx, t, r, key)
			}
			s.mu.Unlock()
		}
	}
}

// dispatch reserves key and runs one worker attempt in the background. Explicit
// stops and service shutdown cancel the job and stop its process. Caller holds s.mu.
func (s *Supervisor) dispatch(ctx context.Context, t *Town, r Role, key string) {
	child, cancel := context.WithCancelCause(ctx)
	s.running[key] = func() { cancel(errStopWorker) }
	s.Store.setActive(s.activeWorkers())
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { cancel(nil); s.releaseWorker(key) }()
		s.execute(child, t, r)
	}()
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
	// A repair is the repo house's only agent work, so it answers to the
	// budget even though the inventory around it does not.
	if s.Store.budgetExhausted(id, s.now()) {
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

func (s *Supervisor) execute(ctx context.Context, t *Town, r Role) {
	now := s.now()
	started := now
	if err := s.Store.Update(func(st *State) error {
		current := st.Towns[t.ID]
		w := current.Workers[r]
		if ctx.Err() != nil {
			return context.Canceled
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
	repaired := false
	func() {
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("worker panic: %v", v)
			}
		}()
		if r == Repo {
			repair := s.claimRepair(t.ID)
			repaired = repair
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
	if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		log.Error("worker attempt failed", "error", err)
	}
	close(updates)
	close(progress)
	<-done
	finished := s.now()
	s.update(func(st *State) error {
		current := st.Towns[t.ID]
		w := current.Workers[r]
		var interrupted *WorkerInterruptedError
		if w.Run != nil && errors.As(err, &interrupted) {
			run := w.Run
			target := workerRunTask(*run)
			w.Recovery = &WorkerRecovery{TaskID: target, Base: run.BaseSHA, Head: run.HeadSHA, Started: run.Started, Detail: recoveryDetail(r, target)}
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
		// A failure on one pull request belongs to that pull request. The
		// house stays available for the rest of its queue.
		// A Mayor judgment's failure belongs to the arrival it concerned.
		taskOwned := err != nil && ((result.PR > 0 && (r == Review || r == Issue)) || (r == Hall && result.JudgedTask != ""))
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil && !taskOwned {
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
		if r == Hall && (result.JudgedTask != "" || result.Bulletin != nil) {
			runErr := err
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				runErr = nil
			}
			if result.JudgedTask != "" && (runErr != nil || result.Judgment != nil) {
				applyJudgment(st, current, result.JudgedTask, result.Judgment, runErr, s.now())
				if runErr != nil {
					// The failure belongs to that arrival; the house keeps
					// judging the rest of the queue.
					err = nil
				}
			}
			if result.Bulletin != nil && runErr == nil {
				if e := applyBulletin(st, current, *result.Bulletin, s.now()); e != nil {
					w.Error = e.Error()
				}
			}
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
					s.failPullAttempt(st, current, task, r, err.Error())
					if r == Review && task.Stage == "queued" {
						w.Task = task.Detail
						w.Error = ""
					}
				} else if result.Audit != nil && task.Head == result.Audit.Head && task.Base == result.Audit.Base && task.Description == result.Audit.Description {
					if task.Severities == nil {
						task.Severities = map[string]string{}
					}
					for id, severity := range result.Severities {
						task.Severities[id] = severity
					}
					for _, f := range result.Audit.Findings {
						if ValidSeverity(f.Severity) {
							task.Severities[f.ID] = f.Severity
						}
					}
					if result.Audit.Verdict == "inconclusive" {
						// No decision was reached about this revision, so the
						// attempt counts like any other that produced none.
						s.failPullAttempt(st, current, task, r, "inconclusive review: "+result.Audit.Summary)
					} else {
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
						s.settleReview(st, current, task)
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
			attempt := OutcomeRecord{ID: attemptID, At: finished, Class: "attempt", Kind: "worker_attempt", Status: status, Role: r, TaskID: taskID, Revision: revision, Detail: detail, ElapsedMS: elapsedMillis(started, finished), Agent: OccupiesAgentSlot(r) || repaired, Usage: result.Usage, CostUSD: result.CostUSD}
			current.RecordOutcome(attempt)
			current.chargeBudget(attempt, finished)
			if status == "abandoned" {
				current.RecordOutcome(OutcomeRecord{ID: "abandoned:" + attemptID, At: finished, Class: "outcome", Kind: "abandoned", Status: "abandoned", TaskID: taskID, Revision: revision, Detail: detail, ElapsedMS: elapsedMillis(started, finished)})
			}
		}
		if w.Recovery != nil {
			w.Task = w.Recovery.Detail
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
	if role == Hall {
		if task := t.Tasks[result.JudgedTask]; task != nil {
			return task.ID, task.Head
		}
		return result.JudgedTask, ""
	}
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

// applySimplification binds one assessment to the intake it answers. Repo Bot
// can observe a closure while Simplifier is still running, and the Mayor can
// decide the same arrival in that window; either way the task has left intake
// and the returning assessment describes work Town no longer holds. Applying it
// regardless overwrote a confirmed closure and sent the pull request to Review.
func applySimplification(st *State, t *Town, task *Task, assessment *Simplification, runErr error, now time.Time) {
	if task.House != Simplifier || task.Stage != "simplifying" {
		if runErr == nil && assessment != nil {
			st.Event(t.ID, "decision", string(Simplifier), "hall", task.ID, "Simplifier result discarded; "+task.Title+" left intake while it ran", now)
		}
		return
	}
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
	current := s.Store.Snapshot().Towns[t.ID]
	err = errors.Join(s.closeDeclinedProposals(ctx, current, remote), s.closeRetiredPulls(ctx, current, remote))
	return errors.Join(err, s.fileFollowUps(ctx, s.Store.Snapshot().Towns[t.ID]))
}

// closeDeclinedProposals retires issues after an authorized final decision: the
// Mayor's decline for Town's own proposals, or Simplifier's auto decline for an
// intake issue. Closing is idempotent, so an issue that is already closed is
// skipped and an uncertain attempt is retried by the next inventory.
//
// The candidates come from a snapshot, and the Mayor can admit an auto-declined
// issue while this runs. Each issue is therefore claimed under the store just
// before the GitHub write: the claim rechecks the decline and moves an
// auto-declined issue to "closing", which the Mayor's admission refuses. An
// accepted close settles it as closed, a definite rejection releases it, and an
// uncertain outcome keeps the claim so the write is retried rather than
// admitted.
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
		if closableDecline(task) && open[task.Number] {
			numbers = append(numbers, task.Number)
		}
	}
	sort.Ints(numbers)
	var failures error
	for _, n := range numbers {
		id := fmt.Sprintf("issue:%d", n)
		claimed := false
		if err := s.Store.Update(func(st *State) error {
			current := st.Towns[t.ID]
			if current == nil || current.Deleted {
				return nil
			}
			task := current.Tasks[id]
			if !closableDecline(task) {
				return nil
			}
			if task.MayoralDecision == "" && task.Stage == "declined" {
				task.Stage = "closing"
				task.Updated = s.now()
			}
			claimed = true
			return nil
		}); err != nil {
			return errors.Join(failures, err)
		}
		if !claimed {
			continue
		}
		if err := s.GitHub.CloseIssue(ctx, t.Config.Repo, n); err != nil {
			failures = errors.Join(failures, fmt.Errorf("close declined issue #%d: %w", n, err))
			var rejected *RejectedError
			if errors.As(err, &rejected) {
				// GitHub refused the close, so nothing happened there; the
				// claim is released and the Mayor can admit the issue again.
				if err := s.Store.Update(func(st *State) error {
					current := st.Towns[t.ID]
					if current == nil {
						return nil
					}
					if task := current.Tasks[id]; task != nil && task.Stage == "closing" && task.MayoralDecision == "" {
						task.Stage = "declined"
						task.Detail = fmt.Sprintf("GitHub refused to close this issue (HTTP %d). Simplifier's decline stands; the Mayor can admit it anyway.", rejected.Status)
						task.Updated = s.now()
						st.Event(t.ID, "error", string(Repo), "hall", id, "GitHub refused to close: "+task.Title, s.now())
					}
					return nil
				}); err != nil {
					return errors.Join(failures, err)
				}
			}
			continue
		}
		if err := s.Store.Update(func(st *State) error {
			current := st.Towns[t.ID]
			task := current.Tasks[id]
			if current == nil || task == nil {
				return nil
			}
			if !closableDecline(task) {
				return nil
			}
			if task.Stage == "closing" {
				// GitHub accepted the close. A reopen seen by a later
				// inventory is then an appeal rather than a close to retry.
				task.Stage = "closed"
				task.Updated = s.now()
			}
			if task.MayoralDecision == "declined" {
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

// closableDecline reports whether Town still holds a final decline it carries
// out by closing the issue: the Mayor's decline of Town's own proposal, or
// Simplifier's auto decline that nobody has escalated to the Mayor or admitted.
func closableDecline(task *Task) bool {
	if task == nil || task.Kind != "issue" {
		return false
	}
	if task.MayoralDecision == "declined" {
		return !task.External
	}
	return task.MayoralDecision == "" && task.House == Hall && autoDeclined(task) && (task.Stage == "declined" || task.Stage == "closing" || task.Stage == "locked")
}
func (s *Supervisor) Control(id string, role Role, action, taskID string) error {
	if action == "delete" {
		if role != "all" {
			return errors.New("delete applies to the entire town")
		}
		return s.Delete(id)
	}
	decision := action == "admit" || action == "decline"
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
			return st.decideTask(t, t.Tasks[taskID], action, "Mayor", s.now())
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
			if action == "start" && w.Recovery != nil {
				if w.Run != nil {
					return errors.New("the previous worker may still be running; reconnect or stop it before recovery")
				}
				if w.Recovery.TaskID != "" {
					return fmt.Errorf("recover interrupted %s with retry --task %s after checking GitHub", r, w.Recovery.TaskID)
				}
				w.Recovery = nil // Explicit restart authorizes another discovery scan.
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
	if w.Recovery != nil && w.Recovery.TaskID == task.ID && w.Run == nil {
		w.Recovery = nil
		w.Error = ""
		w.Status = "waiting"
		if !w.Enabled {
			w.Status = "paused"
		}
	}
	w.Next = time.Time{}
	if !w.Enabled {
		task.Detail = fmt.Sprintf("Ready to retry. %s Bot is paused; start it to run this task.", houseName(task.House))
	}
}

// houseName is the operator-facing name of a house.
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
		if !gate.Allows(p, task.Audit, t.Branch()) {
			if err := s.Store.Update(func(st *State) error {
				detail := "Waiting for current review, required checks, approvals, and mergeability."
				if p.Base.Ref != t.Branch() || gate.BaseRef != t.Branch() {
					detail = fmt.Sprintf("Targets %s, but this town covers %s. Town does not merge outside the branch it was configured for.", p.Base.Ref, t.Branch())
				}
				st.Towns[t.ID].Tasks[task.ID].Detail = detail
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
	// Groups and LogValuers are resolved and walked, so a sensitive key nested
	// inside one is dropped like a top-level one instead of being printed as
	// part of the group's rendering.
	var appendAttr func(prefix string, a slog.Attr)
	appendAttr = func(prefix string, a slog.Attr) {
		key := strings.ToLower(a.Key)
		if strings.Contains(key, "token") || strings.Contains(key, "secret") || key == "environment" || key == "command" {
			return
		}
		v := a.Value.Resolve()
		if v.Kind() == slog.KindGroup {
			if a.Key != "" {
				prefix += a.Key + "."
			}
			for _, member := range v.Group() {
				appendAttr(prefix, member)
			}
			return
		}
		text += " · " + prefix + a.Key + "=" + v.String()
	}
	for _, a := range l.attrs {
		appendAttr("", a)
	}
	r.Attrs(func(a slog.Attr) bool { appendAttr("", a); return true })
	if len(text) > 4000 {
		cut := 4000
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
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

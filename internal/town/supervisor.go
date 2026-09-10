package town

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/brokk-town/internal/harness"
)

type Progress struct{ Phase, Task string }
type RunResult struct {
	Owned map[int]Ownership
	Audit *Audit
	PR    int
}
type Workers interface {
	Run(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error)
}
type Supervisor struct {
	MaxWorkers int
	Store      *Store
	GitHub     GitHub
	Workers    Workers
	Publisher  IssuePublisher
	Harnesses  *harness.Catalog
	mu         sync.Mutex
	running    map[string]context.CancelFunc
	wg         sync.WaitGroup
	wake       chan struct{}
	fatal      chan error
	now        func() time.Time
}

func NewSupervisor(store *Store, gh GitHub, workers Workers) *Supervisor {
	publisher, _ := gh.(IssuePublisher)
	return &Supervisor{MaxWorkers: 4, Store: store, GitHub: gh, Workers: workers, Publisher: publisher, Harnesses: harness.New(filepath.Join(filepath.Dir(store.path), "harnesses"), store.Snapshot().Demo), running: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1), fatal: make(chan error, 1), now: time.Now}
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
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
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
			_, busy := s.running[key]
			active := 0
			for key := range s.running {
				if !strings.HasSuffix(key, ":repo") && !strings.HasSuffix(key, ":requests") {
					active++
				}
			}
			if r != Repo && active >= max(1, s.MaxWorkers) {
				busy = true
			}
			if !busy {
				child, cancel := context.WithCancel(ctx)
				s.running[key] = cancel
				s.wg.Add(1)
				go func(t *Town, r Role, key string) {
					defer s.wg.Done()
					defer func() { s.mu.Lock(); delete(s.running, key); s.mu.Unlock() }()
					s.execute(child, t, r)
				}(t, r, key)
			}
			s.mu.Unlock()
		}
	}
}
func (s *Supervisor) execute(ctx context.Context, t *Town, r Role) {
	now := s.now()
	if err := s.Store.Update(func(st *State) error {
		current := st.Towns[t.ID]
		w := current.Workers[r]
		if current.Deleted || !w.Enabled || ctx.Err() != nil {
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
		u, pch := updates, progress
		for u != nil || pch != nil {
			select {
			case l, ok := <-u:
				if !ok {
					u = nil
					continue
				}
				s.update(func(st *State) error {
					w := st.Towns[t.ID].Workers[r]
					w.Logs = append(w.Logs, l)
					if len(w.Logs) > 100 {
						w.Logs = w.Logs[len(w.Logs)-100:]
					}
					return nil
				})
			case p, ok := <-pch:
				if !ok {
					pch = nil
					continue
				}
				s.update(func(st *State) error {
					w := st.Towns[t.ID].Workers[r]
					w.Phase = p.Phase
					w.Task = p.Task
					w.Updated = s.now()
					return nil
				})
			}
		}
	}()
	observe := func(p Progress) {
		select {
		case progress <- p:
		default:
		}
	}
	log := slog.New(&workerLog{role: r, out: updates, now: s.now})
	var result RunResult
	var err error
	func() {
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("worker panic: %v", v)
			}
		}()
		if r == Repo {
			err = s.reconcile(ctx, t)
		} else if r == Review {
			handled, e := s.mergeReady(ctx, t)
			err = e
			if !handled && err == nil {
				result, err = s.Workers.Run(ctx, t, r, observe, log)
			}
		} else {
			result, err = s.Workers.Run(ctx, t, r, observe, log)
		}
	}()
	close(updates)
	close(progress)
	<-done
	s.update(func(st *State) error {
		current := st.Towns[t.ID]
		w := current.Workers[r]
		w.Status = "waiting"
		w.Updated = s.now()
		w.Next = s.now().Add(time.Duration(current.Config.PollSeconds) * time.Second)
		if r == Bug || r == Feature {
			w.Next = s.now().Add(30 * time.Minute)
		}
		if r == Release {
			w.Next = s.now().Add(5 * time.Minute)
		}
		if !w.Enabled {
			w.Status = "paused"
		}
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			w.Status = "failed"
			w.Error = err.Error()
			w.Task = "Work paused: " + err.Error()
			w.Next = s.now().Add(15 * time.Minute)
			st.Event(t.ID, "error", string(r), "hall", "", string(r)+" needs attention", s.now())
			if r == Repo {
				current.Error = err.Error()
			}
		}
		for n, o := range result.Owned {
			current.Owned[n] = o
		}
		if result.PR > 0 {
			task := current.Tasks[fmt.Sprintf("pr:%d", result.PR)]
			if task != nil {
				if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
					task.Attempts++
					task.RetryAt = s.now().Add(15 * time.Minute)
					task.Detail = err.Error()
					task.Blocked = task.Attempts >= 3
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
							task.Stage = "awaiting_author"
							task.Detail = "Review feedback is ready for the external PR author."
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
		if r != Repo {
			current.Workers[Repo].Next = time.Time{}
		}
		return nil
	})
}
func (s *Supervisor) reconcile(ctx context.Context, t *Town) error {
	t = s.Store.Snapshot().Towns[t.ID]
	remote, err := s.GitHub.Snapshot(ctx, t.Config)
	if err != nil {
		return err
	}
	remote.ObservedHeads = map[int]string{}
	for _, task := range t.Tasks {
		if task.Kind == "pr" {
			remote.ObservedHeads[task.Number] = task.Head
		}
	}
	if t.Initialized && SHA(t.Head) && t.Head != remote.Head {
		remote.Commits, err = s.GitHub.Changes(ctx, t.Config.Repo, t.Head, remote.Head)
		if err != nil {
			return err
		}
	}
	remote.Released = map[string]bool{}
	if latest := latestRelease(remote.Releases); latest != nil {
		commits := map[string]bool{}
		for _, c := range remote.Commits {
			if SHA(c.SHA) {
				commits[c.SHA] = true
			}
		}
		for _, task := range t.Tasks {
			if task.Kind == "commit" && task.Stage != "shipped" {
				commits[task.Head] = true
			}
		}
		for _, p := range remote.Pulls {
			if p.MergedAt != nil && SHA(p.MergeCommit) && p.Base.Ref == remote.Branch {
				known := t.Tasks["commit:"+p.MergeCommit]
				if known == nil || known.Stage != "shipped" {
					commits[p.MergeCommit] = true
				}
			}
		}
		for sha := range commits {
			included, err := s.GitHub.Contains(ctx, t.Config.Repo, sha, latest.Tag)
			if err != nil {
				return err
			}
			remote.Released[sha] = included
		}
	}
	return s.Store.Update(func(st *State) error {
		Reconcile(st, st.Towns[t.ID], remote, s.now())
		w := st.Towns[t.ID].Workers[Repo]
		w.Task = "Repository inventory is current"
		w.Phase = "reporting"
		return nil
	})
}
func (s *Supervisor) Control(id string, role Role, action, taskID string) error {
	if action == "delete" {
		if role != "all" {
			return errors.New("delete applies to the entire town")
		}
		return s.Delete(id)
	}
	if action != "start" && action != "pause" && action != "stop" && action != "retry" {
		return errors.New("unknown action")
	}
	if role != "all" && !ValidRole(role) {
		return errors.New("unknown role")
	}
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		if action == "retry" {
			task := t.Tasks[taskID]
			if task == nil {
				return errors.New("unknown task")
			}
			if i := t.Intents[task.Number]; i != nil && i.Status == "uncertain" {
				i.Status = "retry" // Explicit operator request; worker rechecks GitHub before any write.
			}
			task.Blocked = false
			task.Attempts = 0
			task.RetryAt = time.Time{}
			if task.Stage == "inconclusive" {
				task.Audit = nil
				task.Stage = "queued"
				task.House = Review
			}
			if task.Cycles >= t.Config.MaxCycles {
				task.Cycles = 0
			}
			t.Workers[task.House].Next = time.Time{}
			t.Workers[task.House].Enabled = true
			return nil
		}
		for _, r := range Roles {
			if role != "all" && role != r {
				continue
			}
			w := t.Workers[r]
			w.Enabled = action == "start"
			if w.Enabled {
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
func (s *Supervisor) mergeReady(ctx context.Context, t *Town) (bool, error) {
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
				return true, s.reconcile(ctx, t)
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
			return true, s.reconcile(ctx, t)
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
		return true, s.reconcile(ctx, t)
	}
	return false, nil
}

type workerLog struct {
	role  Role
	out   chan<- Log
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
	select {
	case l.out <- Log{l.now(), r.Level.String(), text}:
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

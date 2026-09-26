package town

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Store struct {
	active  int // Runtime reservations, never persisted or inferred from worker status.
	mu      sync.RWMutex
	state   State
	path    string
	lock    *os.File
	changed chan struct{}
	notice  string
}

// ErrServiceRunning means another process holds this state directory's lock.
var ErrServiceRunning = errors.New("another town service is running")

func Open(dir string, demo bool) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: %w", ErrServiceRunning, err)
	}
	s := &Store{state: NewState(demo), path: filepath.Join(dir, "state.json"), lock: f, changed: make(chan struct{})}
	b, err := os.ReadFile(s.path)
	if err == nil {
		err = json.Unmarshal(b, &s.state)
		parsed := err == nil
		// Unmarshal leaves absent fields unchanged. Since the initial state is
		// marked as demo, read the marker from the file itself before accepting
		// or replacing it as demo state.
		explicitDemo := parsed && demoStateIsDisposable(demo, b)
		if err == nil {
			if demo && !explicitDemo {
				err = errors.New("state format or demo mode mismatch; use a separate state directory")
			} else {
				err = validateState(s.state, demo)
			}
		}
		// Demo state is a simulation, so a file an older Town wrote must not
		// stop the demo from starting. It is set aside with every byte kept and
		// the demo seeds itself again. Only a file that parsed and says it is
		// demo state is treated this way: anything else may be real work that
		// someone pointed at this directory, and that is never discarded.
		if err != nil && explicitDemo {
			if rejected, e := setAside(s.path); e == nil {
				s.notice = fmt.Sprintf("demo state this Town cannot read was set aside as %s; starting a fresh demo", filepath.Base(rejected))
				s.state = NewState(true)
				err = nil
			}
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.Close()
		if demo {
			return nil, fmt.Errorf("read town state: %w (the demo keeps its state in %s; move that file aside to start over)", err, dir)
		}
		return nil, fmt.Errorf("read town state: %w", err)
	}
	if s.notice != "" {
		// Leave the directory holding a state this Town can reopen, rather than
		// waiting for the first write.
		if err := s.Update(func(*State) error { return nil }); err != nil {
			s.Close()
			return nil, fmt.Errorf("replace unreadable demo state: %w", err)
		}
	}
	// A saved dispatch is interrupted, not a process to adopt. Preserve possible
	// writes, while allowing repository reads to resume and reconcile them.
	for _, t := range s.state.Towns {
		for _, w := range t.Workers {
			recoverWorkerRun(w)
			w.Agent = nil
			w.Execution = nil
			if w.Recovery != nil {
				if w.Role == Repo {
					w.Next = time.Time{}
				}
				w.Status = "failed"
				if !w.Enabled {
					w.Status = "paused"
				}
				w.Task = w.Recovery.Detail
				continue
			}
			if w.Enabled {
				w.Status = "waiting"
			} else {
				w.Status = "paused"
			}
		}
	}
	return s, nil
}

// Notice reports a one-time fact about how this store was opened, such as a
// demo state that had to be set aside. It is empty when nothing happened. Call
// it before the store is shared: it is not synchronized with writes.
func (s *Store) Notice() string { return s.notice }

// demoStateIsDisposable requires a demo marker in the saved JSON. The default
// value in Store.state cannot prove that a file belongs to the demo.
func demoStateIsDisposable(demo bool, data []byte) bool {
	if !demo {
		return false
	}
	var marker struct {
		Demo *bool `json:"demo"`
	}
	return json.Unmarshal(data, &marker) == nil && marker.Demo != nil && *marker.Demo
}

// setAside renames a state file to a sibling name, keeping the original bytes
// and permissions so a reset never destroys the evidence of what was there.
func setAside(path string) (string, error) {
	dir, base := filepath.Split(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	stamp := time.Now().UTC().Format("20060102-150405")
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("%s.rejected-%s.json", stem, stamp)
		if i > 0 {
			name = fmt.Sprintf("%s.rejected-%s-%d.json", stem, stamp, i)
		}
		target := filepath.Join(dir, name)
		if _, err := os.Lstat(target); err == nil {
			continue // Another rejected state already holds that name.
		}
		if err := os.Rename(path, target); err != nil {
			return "", err
		}
		return target, nil
	}
	return "", fmt.Errorf("no free name beside %s", path)
}

func validateState(s State, demo bool) error {
	if err := s.ServiceConfig.Validate(); err != nil {
		return err
	}
	if s.Format != 1 || s.Demo != demo || s.Towns == nil {
		return errors.New("state format or demo mode mismatch; use a separate state directory")
	}
	var last uint64
	for _, e := range s.Events {
		if e.Seq <= last || e.Seq > s.Seq || s.Towns[e.Town] == nil {
			return errors.New("invalid event sequence")
		}
		last = e.Seq
	}
	for id, t := range s.Towns {
		if t == nil || t.ID != id || id != strings.ToLower(t.Config.Repo) || t.Config.Validate() != nil || t.Tasks == nil || t.Workers == nil || t.Owned == nil || t.Intents == nil {
			return errors.New("invalid town state")
		}
		if len(t.Bulletins) > maxBulletins {
			return errors.New("too many bulletins")
		}
		for i, b := range t.Bulletins {
			if !validBulletin(b) || (i > 0 && !t.Bulletins[i-1].Until.Equal(b.Since)) {
				return errors.New("invalid bulletin feed")
			}
		}
		if t.DefaultBranch != "" && !ValidBranch(t.DefaultBranch) {
			return errors.New("invalid observed default branch")
		}
		if l := t.Budget; l != nil {
			if !ValidBudgetPeriod(l.Period) || l.From.IsZero() || l.Attempts < 0 || l.AgentMS < 0 || l.Untimed < 0 || l.Untimed > l.Attempts {
				return errors.New("invalid budget ledger")
			}
			if l.Usage != nil && (l.Usage.InputTokens < 0 || l.Usage.OutputTokens < 0) {
				return errors.New("invalid budget ledger usage")
			}
			if l.CostUSD != nil && *l.CostUSD < 0 {
				return errors.New("invalid budget ledger cost")
			}
		}
		for _, r := range Roles {
			if t.Workers[r] == nil || t.Workers[r].Role != r {
				return fmt.Errorf("town %s is missing its %s house", id, r)
			}
			if err := t.Workers[r].Run.Validate(r); err != nil {
				return err
			}
			if recovery := t.Workers[r].Recovery; recovery != nil {
				if recovery.Detail == "" || (recovery.Base != "" && !SHA(recovery.Base)) || (recovery.Head != "" && !SHA(recovery.Head)) {
					return errors.New("invalid worker recovery record")
				}
			}
		}
		for key, task := range t.Tasks {
			if task == nil || task.ID != key || !ValidRole(task.House) || task.Cycles < 0 || task.Attempts < 0 || (task.Head != "" && !SHA(task.Head)) || (task.Base != "" && !SHA(task.Base)) {
				return errors.New("invalid task identity or revision")
			}
			if task.MayoralDecision != "" && task.MayoralDecision != "pending" && task.MayoralDecision != "admitted" && task.MayoralDecision != "declined" {
				return errors.New("invalid Mayoral decision")
			}
			if task.MayoralDecision == "pending" && (task.House != Hall || task.Stage != "awaiting_mayor") {
				return errors.New("pending Mayoral decision left Town Hall")
			}
			// A declined pull request its author closed keeps the decline, as
			// does Town's own declined pull request while Town closes it.
			if task.MayoralDecision == "declined" && (task.House != Hall || (task.Stage != "declined" && (task.Kind != "pr" || (task.Stage != "closed" && (task.Stage != "closing" || task.External))))) {
				return errors.New("declined Mayoral decision is not final")
			}
			if task.Stage == "simplifying" && (task.House != Simplifier || (task.Kind != "issue" && task.Kind != "pr")) {
				return errors.New("pending simplifier intake left the clarifier")
			}
			if validDeferReason(task.DeferReason) != nil || (task.DeferReason != "" && task.DeferredUntil.IsZero()) {
				return errors.New("invalid task snooze")
			}
			if s := task.Simplification; s != nil {
				if (s.Mode != "suggest" && s.Mode != "auto") || (s.Decision != "admit" && s.Decision != "decline") || strings.TrimSpace(s.Detail) == "" || len(s.Detail) > 16<<10 || len(s.Summary) > 1024 {
					return errors.New("invalid simplifier assessment")
				}
			}
			switch task.Kind {
			case "issue", "pr":
				if task.Number < 1 || key != fmt.Sprintf("%s:%d", task.Kind, task.Number) {
					return errors.New("invalid task number")
				}
			case "commit":
				if !SHA(task.Head) || key != "commit:"+task.Head {
					return errors.New("invalid commit identity")
				}
			case "source":
				if task.Source == nil || key != "source:"+task.Source.Identity.Key() {
					return errors.New("invalid source task identity")
				}
			default:
				return errors.New("invalid task kind")
			}
			if a := task.Audit; a != nil {
				evidence := map[string]string{}
				for _, f := range a.Findings {
					evidence[f.ID] = f.Detail
				}
				if !SHA(a.Base) || !SHA(a.Head) || validateAudit(a, evidence) != nil {
					return errors.New("invalid saved audit")
				}
			}
			if task.Source != nil {
				if err := task.Source.Validate(); err != nil {
					return errors.New("invalid funnel task")
				}
			}
		}
		seenOutcomes := map[string]bool{}
		for _, outcome := range t.Outcomes {
			if outcome.validate() != nil || seenOutcomes[outcome.ID] {
				return errors.New("invalid or duplicate outcome record")
			}
			seenOutcomes[outcome.ID] = true
		}
		for n, o := range t.Owned {
			if n < 1 || o.Issue < 1 || o.Branch == "" {
				return errors.New("invalid ownership")
			}
		}
		for id, r := range t.Requests {
			if r == nil || r.ID != id || r.validate() != nil {
				return errors.New("invalid issue submission")
			}
			switch r.Status {
			case "queued", "uncertain", "canceled":
			case "confirmed":
				if r.Number < 1 {
					return errors.New("invalid issue confirmation")
				}
			default:
				return errors.New("invalid issue submission status")
			}
		}
		for n, i := range t.Intents {
			if i == nil || i.PR != n || n < 1 || !SHA(i.Base) || !SHA(i.Head) || (i.Kind != "merge" && i.Kind != "repair") || (i.Status != "uncertain" && i.Status != "confirmed" && i.Status != "retry") {
				return errors.New("invalid write intent")
			}
			if i.Kind == "repair" && (!SHA(i.NewHead) || i.Branch == "" || !filepath.IsAbs(i.Directory)) {
				return errors.New("invalid saved repair")
			}
		}
		for key, i := range t.FunnelIntents {
			if i == nil || i.ID != key || i.Validate() != nil {
				return errors.New("invalid funnel write intent")
			}
		}
		for key, sync := range t.FunnelSyncs {
			if sync == nil || sync.Funnel != key || sync.Provider == "" || sync.Outcome.Validate() != nil {
				return errors.New("invalid funnel sync")
			}
		}
	}
	return nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	err := s.lock.Close()
	s.lock = nil
	return err
}
func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := clone(s.state)
	active := s.active
	if out.Demo {
		active = 0
		for _, t := range out.Towns {
			if t.Deleted {
				continue
			}
			for role, w := range t.Workers {
				if OccupiesAgentSlot(role) && (w.Status == "working" || w.Status == "pausing") {
					active++
				}
			}
		}
	}
	out.Capacity = &Capacity{Active: active, Limit: out.ServiceConfig.MaxWorkers}
	return out
}

// dispatchEligibility reads only scheduling fields rather than cloning all task
// history for each candidate. Caller serializes reservations and capacity edits.
func (s *Store) dispatchEligibility(id string, role Role, now time.Time) (bool, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := s.state.Towns[id]
	if t == nil || t.Deleted || (role != Repo && !t.Initialized) {
		return false, s.state.ServiceConfig.MaxWorkers
	}
	if role == Release && t.Config.MergePolicy == "manual" {
		return false, s.state.ServiceConfig.MaxWorkers
	}
	// An exhausted budget holds new agent work only. The repository
	// inventory keeps running: a town that cannot see GitHub cannot
	// reconcile the writes its earlier attempts may already have made.
	if OccupiesAgentSlot(role) && t.BudgetState(now).Exhausted {
		return false, s.state.ServiceConfig.MaxWorkers
	}
	// Quiet hours hold new agent work the same way, and for the same reason
	// leave the inventory running.
	if OccupiesAgentSlot(role) && t.QuietState(now, s.state.ServiceConfig.QuietHours).Active {
		return false, s.state.ServiceConfig.MaxWorkers
	}
	w := t.Workers[role]
	return w != nil && w.Enabled && w.Run == nil && (w.Recovery == nil || role == Repo) && !w.Next.After(now), s.state.ServiceConfig.MaxWorkers
}

// budgetExhausted reports whether this town has spent its accounting
// period. Running work is never interrupted by it.
func (s *Store) budgetExhausted(id string, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := s.state.Towns[id]
	return t != nil && !t.Deleted && t.BudgetState(now).Exhausted
}

// setActive publishes reservation changes through the same snapshot stream.
// The supervisor calls this under its scheduler mutex.
func (s *Store) setActive(active int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == active {
		return
	}
	s.active = active
	close(s.changed)
	s.changed = make(chan struct{})
}
func (s *Store) Watch() <-chan struct{} { s.mu.RLock(); defer s.mu.RUnlock(); return s.changed }
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return errors.New("state store is closed")
	}
	next := clone(s.state)
	if err := fn(&next); err != nil {
		return err
	}
	for _, town := range next.Towns {
		enforceManualReleasePolicy(town)
		boundStateText(town)
	}
	if err := validateState(next, s.state.Demo); err != nil {
		return err
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.state = next
	close(s.changed)
	s.changed = make(chan struct{})
	d, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

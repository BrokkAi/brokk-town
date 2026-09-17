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
		if err == nil {
			// Only an absent setting migrates to the default. A present empty,
			// null, or invalid configuration must fail validation.
			var fields map[string]json.RawMessage
			err = json.Unmarshal(b, &fields)
			if raw, present := fields["service_config"]; present && err == nil {
				s.state.ServiceConfig = ServiceConfig{}
				err = json.Unmarshal(raw, &s.state.ServiceConfig)
			}
			s.state.Capacity = nil
		}
		if err == nil {
			// Older towns predate feature discovery. Add only the absent role,
			// paused, without enabling new automation or repairing corrupt workers.
			for _, t := range s.state.Towns {
				if t != nil && t.Workers != nil {
					if _, present := t.Workers[Feature]; !present {
						t.Workers[Feature] = &Worker{Role: Feature, Status: "paused", Task: "Ready when you are", Logs: []Log{}}
					}
					if _, present := t.Workers[Simplifier]; !present {
						t.Workers[Simplifier] = &Worker{Role: Simplifier, Status: "paused", Task: "Ready when you are", Logs: []Log{}}
					}
				}
				if t != nil {
					if t.FunnelIntents == nil {
						t.FunnelIntents = map[string]*WriteIntent{}
					}
					if t.FunnelSyncs == nil {
						t.FunnelSyncs = map[FunnelID]*FunnelSync{}
					}
					if t.Outcomes == nil {
						t.Outcomes = []OutcomeRecord{}
					}
					enforceManualReleasePolicy(t)
				}
			}
			err = validateState(s.state, demo)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.Close()
		return nil, fmt.Errorf("read town state: %w", err)
	}
	// External bot processes outlive a service restart; their handles stay so
	// the supervisor can reconnect before it schedules anything new. Every other
	// worker returns to its scheduled state, and durable intents are kept.
	for _, t := range s.state.Towns {
		for _, w := range t.Workers {
			if w.Run != nil && !t.Deleted {
				w.Status = "working"
				w.Phase = "reconnecting"
				w.Task = "Reconnecting to " + w.Run.Bot + " " + w.Run.Version + " started before the service restarted"
				continue
			}
			w.Agent = nil
			if w.Enabled {
				w.Status = "waiting"
			} else {
				w.Status = "paused"
			}
		}
	}
	return s, nil
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
		if t.DefaultBranch != "" && !ValidBranch(t.DefaultBranch) {
			return errors.New("invalid observed default branch")
		}
		for _, r := range Roles {
			if t.Workers[r] == nil || t.Workers[r].Role != r {
				return errors.New("missing worker")
			}
			if err := t.Workers[r].Run.Validate(r); err != nil {
				return err
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
			if task.MayoralDecision == "declined" && (task.House != Hall || task.Stage != "declined") {
				return errors.New("declined Mayoral decision is not final")
			}
			if task.Stage == "simplifying" && (task.House != Simplifier || (task.Kind != "issue" && task.Kind != "pr")) {
				return errors.New("pending simplifier intake left the clarifier")
			}
			if s := task.Simplification; s != nil {
				if (s.Mode != "suggest" && s.Mode != "auto") || (s.Decision != "admit" && s.Decision != "decline") || strings.TrimSpace(s.Detail) == "" || len(s.Detail) > 16<<10 || len(s.Summary) > 1024 {
					return errors.New("invalid simplifier assessment")
				}
			}
			switch task.Kind {
			case "upgrade":
				u := task.Upgrade
				if u == nil || !ValidAgentRole(u.Role) || key != "upgrade:"+string(u.Role) || task.House != Hall || !workerVersionPattern.MatchString(u.From) || !workerVersionPattern.MatchString(u.To) {
					return errors.New("invalid bot upgrade identity")
				}
				if task.Stage == "delayed" && (task.MayoralDecision != "" || task.RetryAt.IsZero()) {
					return errors.New("delayed bot upgrade needs a due time")
				}
				if task.Stage != "awaiting_mayor" && task.Stage != "declined" && task.Stage != "delayed" {
					return errors.New("invalid bot upgrade stage")
				}
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
				if ValidAgentRole(role) && (w.Status == "working" || w.Status == "pausing") {
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
	w := t.Workers[role]
	return w != nil && w.Enabled && !w.Next.After(now), s.state.ServiceConfig.MaxWorkers
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

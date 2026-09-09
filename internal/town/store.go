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
)

type Store struct {
	mu      sync.RWMutex
	state   State
	path    string
	lock    *os.File
	changed chan struct{}
}

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
		return nil, fmt.Errorf("another town service is running: %w", err)
	}
	s := &Store{state: NewState(demo), path: filepath.Join(dir, "state.json"), lock: f, changed: make(chan struct{})}
	b, err := os.ReadFile(s.path)
	if err == nil {
		err = json.Unmarshal(b, &s.state)
		if err == nil {
			err = validateState(s.state, demo)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.Close()
		return nil, fmt.Errorf("read town state: %w", err)
	}
	// Worker processes cannot survive a service restart; durable intents can.
	for _, t := range s.state.Towns {
		for _, w := range t.Workers {
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
		for _, r := range Roles {
			if t.Workers[r] == nil || t.Workers[r].Role != r {
				return errors.New("missing worker")
			}
		}
		for key, task := range t.Tasks {
			if task == nil || task.ID != key || !ValidRole(task.House) || task.Cycles < 0 || task.Attempts < 0 || (task.Head != "" && !SHA(task.Head)) || (task.Base != "" && !SHA(task.Base)) {
				return errors.New("invalid task identity or revision")
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
		}
		for n, o := range t.Owned {
			if n < 1 || o.Issue < 1 || o.Branch == "" {
				return errors.New("invalid ownership")
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
func (s *Store) Snapshot() State        { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.state) }
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

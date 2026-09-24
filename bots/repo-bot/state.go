package repobot

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const stateFormat = 1

// State is what this bot remembers between runs: which failing revision it has
// already spent attempts on. It exists so a poll every minute cannot start an
// agent every minute on the same red branch.
type State struct {
	Format   int    `json:"format"`
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Head     string `json:"head,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
	Pushed   string `json:"pushed,omitempty"`
	Failure  string `json:"failure,omitempty"`
	// VainCompare is a release tag whose comparison with the branch head proved
	// nothing. A repository that cuts releases from another branch answers that
	// way forever, so it is not asked again for the same tag.
	VainCompare string    `json:"vain_compare,omitempty"`
	Updated     time.Time `json:"updated,omitempty"`
	// History is the latest standalone observations, oldest first, for the
	// dashboard and status output. Town keeps its own record of worker runs.
	History []Observation `json:"history,omitempty"`
}

// Observation summarizes one standalone run.
type Observation struct {
	At         time.Time `json:"at"`
	Head       string    `json:"head,omitempty"`
	Health     string    `json:"health,omitempty"`
	Failing    []string  `json:"failing,omitempty"`
	Pushed     string    `json:"pushed,omitempty"`
	Attempts   int       `json:"attempts,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	OpenIssues int       `json:"open_issues"`
	OpenPulls  int       `json:"open_pulls"`
	Releases   int       `json:"releases"`
	NewCommits int       `json:"new_commits,omitempty"`
	Error      string    `json:"error,omitempty"`
}

const maxHistory = 100

// maxStateBytes bounds the saved state, which ReadState reads no further than.
const maxStateBytes = 1 << 20

func statePath(cfg Config) string { return filepath.Join(cfg.StateDirectory, "repo-bot.json") }

// lockWatch keeps a second standalone process off the same state and the same
// repository branch, where both would spend repair attempts and push repairs.
// Town serializes its own worker runs.
func lockWatch(cfg Config) (func(), error) {
	base, err := stateHome()
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(strings.ToLower(cfg.GitHub.Host+"/"+cfg.GitHubRepo()) + "\x00" + cfg.Branch))
	branchUnlock, err := lockFile(filepath.Join(base, "repo-bot", "locks", fmt.Sprintf("%x.lock", key)))
	if err != nil {
		return nil, err
	}
	stateUnlock, err := lockFile(filepath.Join(cfg.StateDirectory, "daemon.lock"))
	if err != nil {
		branchUnlock()
		return nil, err
	}
	return func() { stateUnlock(); branchUnlock() }, nil
}

func lockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another process holds %s: %w", path, err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// ReadState returns the durable repair state, or nil when this workspace has
// none yet. A state written for another repository or branch is not this
// workspace's memory and is reported as an error rather than silently reused.
func ReadState(cfg Config) (*State, error) {
	f, err := os.Open(statePath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s State
	decoder := json.NewDecoder(io.LimitReader(f, maxStateBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("read repair state: %w", err)
	}
	if s.Format != stateFormat {
		return nil, fmt.Errorf("unsupported repair state format %d", s.Format)
	}
	if s.Repo != cfg.GitHubRepo() || s.Branch != cfg.Branch {
		return nil, fmt.Errorf("repair state belongs to %s@%s", s.Repo, s.Branch)
	}
	return &s, nil
}

// updateState reads, changes and saves this workspace's memory in one step, so
// one duty's record never erases another's.
func updateState(cfg Config, change func(*State)) error {
	saved, err := ReadState(cfg)
	if err != nil {
		return err
	}
	if saved == nil {
		saved = &State{}
	}
	change(saved)
	return writeState(cfg, saved)
}

func writeState(cfg Config, s *State) error {
	s.Format = stateFormat
	s.Repo = cfg.GitHubRepo()
	s.Branch = cfg.Branch
	s.Updated = time.Now().UTC()
	if err := os.MkdirAll(cfg.StateDirectory, 0700); err != nil {
		return err
	}
	var data bytes.Buffer
	for {
		data.Reset()
		encoder := json.NewEncoder(&data)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(s); err != nil {
			return err
		}
		// ReadState reads at most maxStateBytes, so the oldest observations
		// give way rather than leave a state the bot can no longer read.
		if data.Len() <= maxStateBytes || len(s.History) == 0 {
			break
		}
		s.History = s.History[max(1, len(s.History)/10):]
	}
	if data.Len() > maxStateBytes {
		return errors.New("repair state is too large to save")
	}
	f, err := os.CreateTemp(cfg.StateDirectory, "repo-bot-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.Write(data.Bytes()); err != nil {
		f.Close()
		os.Remove(name)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, statePath(cfg)); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

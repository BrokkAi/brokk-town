package mayorbot

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// State is the Mayor's durable record of the bulletins it has written. Town
// keeps the bulletins themselves; this history lets the bot refuse to rewrite
// a window it already covered and shows an operator what was produced.
type State struct {
	Format    int              `json:"format"`
	Remote    string           `json:"remote"`
	Branch    string           `json:"branch"`
	Repo      string           `json:"repo"`
	Host      string           `json:"host"`
	Bulletins []BulletinRecord `json:"bulletins,omitempty"`
}

// BulletinRecord names one bulletin window and what it reported.
type BulletinRecord struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	At    time.Time `json:"at"`
	Title string    `json:"title"`
	Items int       `json:"items"`
	Pulls []int     `json:"pulls,omitempty"`
	// Summary and Entries keep what the bulletin said, for the dashboard and
	// status output.
	Summary string         `json:"summary,omitempty"`
	Entries []BulletinItem `json:"entries,omitempty"`
}

const maxBulletinRecords = 200

func newState(cfg Config) *State {
	return &State{Format: 1, Remote: cfg.Remote, Branch: cfg.Branch, Repo: cfg.GitHubRepo(), Host: cfg.GitHub.Host}
}
func ReadState(cfg Config) (*State, error) {
	b, err := os.ReadFile(filepath.Join(cfg.StateDirectory, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("invalid saved state: %w", err)
	}
	if s.Format != 1 || s.Remote != cfg.Remote || s.Branch != cfg.Branch || s.Repo != cfg.GitHubRepo() || s.Host != cfg.GitHub.Host {
		return nil, errors.New("state version or repository identity differs from configuration")
	}
	for _, r := range s.Bulletins {
		if !r.Until.After(r.Since) || r.At.IsZero() || r.Items < 0 || len(r.Title) > 256 {
			return nil, errors.New("invalid saved bulletin record")
		}
	}
	return &s, nil
}
func (s *State) record(r BulletinRecord) {
	s.Bulletins = append(s.Bulletins, r)
	if len(s.Bulletins) > maxBulletinRecords {
		s.Bulletins = s.Bulletins[len(s.Bulletins)-maxBulletinRecords:]
	}
}
func validCommit(s string) bool {
	return (len(s) == 40 || len(s) == 64) && strings.Trim(s, "0123456789abcdef") == ""
}
func writeState(cfg Config, state *State) error {
	if err := os.MkdirAll(cfg.StateDirectory, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(cfg.StateDirectory, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(cfg.StateDirectory, "state.json")); err != nil {
		return err
	}
	dir, err := os.Open(cfg.StateDirectory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
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
func lockConfig(cfg Config) (func(), error) {
	base, err := stateHome()
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(strings.ToLower(cfg.GitHub.Host + "/" + cfg.GitHubRepo())))
	repoUnlock, err := lockFile(filepath.Join(base, "mayor-bot", "locks", fmt.Sprintf("%x.lock", key)))
	if err != nil {
		return nil, err
	}
	stateUnlock, err := lockFile(filepath.Join(cfg.StateDirectory, "daemon.lock"))
	if err != nil {
		repoUnlock()
		return nil, err
	}
	checkoutUnlock, err := lockFile(cfg.Directory + ".mayor-bot.lock")
	if err != nil {
		stateUnlock()
		repoUnlock()
		return nil, err
	}
	return func() { checkoutUnlock(); stateUnlock(); repoUnlock() }, nil
}

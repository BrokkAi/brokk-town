package repobot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func workspace(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.Remote = "https://github.com/acme/orchard.git"
	cfg.Branch = "main"
	cfg.Directory = filepath.Join(root, "checkout")
	cfg.StateDirectory = filepath.Join(root, "state")
	cfg.GitHub.Repo = "acme/orchard"
	return cfg
}

func TestStateRoundTrip(t *testing.T) {
	cfg := workspace(t)
	saved, err := ReadState(cfg)
	if err != nil || saved != nil {
		t.Fatalf("a workspace with no state must report none: %v %v", saved, err)
	}
	if err := writeState(cfg, &State{Head: strings.Repeat("a", 40), Attempts: 2, Failure: "verification failed"}); err != nil {
		t.Fatal(err)
	}
	saved, err = ReadState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Attempts != 2 || saved.Head != strings.Repeat("a", 40) || saved.Failure != "verification failed" {
		t.Fatalf("state did not survive the round trip: %+v", saved)
	}
	if saved.Repo != "acme/orchard" || saved.Branch != "main" {
		t.Fatalf("state must record the workspace it belongs to: %+v", saved)
	}
	info, err := os.Stat(statePath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("repair state must stay private, got %v", info.Mode().Perm())
	}
}

func TestStateFromAnotherWorkspaceIsRefused(t *testing.T) {
	cfg := workspace(t)
	if err := writeState(cfg, &State{Head: strings.Repeat("b", 40)}); err != nil {
		t.Fatal(err)
	}
	other := cfg
	other.Branch = "release"
	if _, err := ReadState(other); err == nil {
		t.Fatal("state written for another branch must not be reused")
	}
}

func TestSecondWatchOnTheSameBranchIsRefused(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := workspace(t)
	unlock, err := lockWatch(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Another workspace for the same repository branch would spend the same
	// repair budget and push competing repairs.
	other := workspace(t)
	if err := Watch(t.Context(), other, nil, true); err == nil || !strings.Contains(err.Error(), "another process holds") {
		t.Fatalf("a second watch must be refused, got %v", err)
	}
	unlock()
	release := workspace(t)
	release.Branch = "release"
	unlockRelease, err := lockWatch(release)
	if err != nil {
		t.Fatalf("another branch is independent: %v", err)
	}
	unlockRelease()
	again, err := lockWatch(cfg)
	if err != nil {
		t.Fatalf("the lock must be released: %v", err)
	}
	again()
}

func TestLargeHistoryStaysReadable(t *testing.T) {
	cfg := workspace(t)
	// Compiler output is full of characters JSON escapes six bytes wide, so a
	// full history of bounded details can still outgrow what ReadState reads.
	detail := truncate(strings.Repeat("<T>&", 1000), 2000)
	var history []Observation
	for i := range maxHistory {
		history = append(history, Observation{At: time.Unix(int64(i), 0).UTC(), Health: healthRed, Detail: detail, Error: detail})
	}
	if err := writeState(cfg, &State{Head: strings.Repeat("a", 40), Attempts: 1, History: history}); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadState(cfg)
	if err != nil {
		t.Fatalf("a saved state must stay readable: %v", err)
	}
	if n := len(saved.History); n == 0 || n >= maxHistory || !saved.History[n-1].At.Equal(history[maxHistory-1].At) || saved.Attempts != 1 {
		t.Fatalf("the oldest observations give way, the newest and the repair budget stay: %d observations, %+v", n, saved.History[n-1].At)
	}
}

func TestStateRejectsUnknownFields(t *testing.T) {
	cfg := workspace(t)
	if err := os.MkdirAll(cfg.StateDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(cfg), []byte(`{"format":1,"repo":"acme/orchard","branch":"main","surprise":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(cfg); err == nil {
		t.Fatal("unknown state fields must be refused rather than ignored")
	}
}

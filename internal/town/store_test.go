package town

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

func rejectedStates(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, "state.rejected-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// staleDemoState writes what an older Town left behind: valid JSON in today's
// format, in demo mode, but one town is missing a house that did not exist
// when the file was written.
func staleDemoState(t *testing.T, dir string) []byte {
	t.Helper()
	state := NewState(true)
	orchard, err := state.Add(DefaultConfig("BrokkAi/orchard"))
	if err != nil {
		t.Fatal(err)
	}
	orchard.Initialized = true
	delete(orchard.Workers, Hall)
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(dir), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDemoOpenSetsAsideADemoStateItCannotRead(t *testing.T) {
	dir := t.TempDir()
	original := staleDemoState(t, dir)
	store, err := Open(dir, true)
	if err != nil {
		t.Fatalf("a stale demo state must not stop the demo: %v", err)
	}
	defer store.Close()
	if notice := store.Notice(); !strings.Contains(notice, "set aside") {
		t.Fatalf("notice = %q, want it to explain the reset", notice)
	}
	if snapshot := store.Snapshot(); !snapshot.Demo || len(snapshot.Towns) != 0 {
		t.Fatalf("want a fresh empty demo state, got demo=%v towns=%d", snapshot.Demo, len(snapshot.Towns))
	}
	rejected := rejectedStates(t, dir)
	if len(rejected) != 1 {
		t.Fatalf("set-aside files = %v, want exactly one", rejected)
	}
	kept, err := os.ReadFile(rejected[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, original) {
		t.Fatal("the set-aside state must keep every original byte")
	}
	info, err := os.Stat(rejected[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("set-aside permissions = %v, want 0600", info.Mode().Perm())
	}
	current, err := os.ReadFile(statePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(current, []byte(`"demo": true`)) {
		t.Fatal("the state on disk must be the fresh demo state")
	}
}

func TestOpenNeverSetsAsideStateThatIsNotDemoState(t *testing.T) {
	dir := t.TempDir()
	real, err := json.MarshalIndent(NewState(false), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(dir), real, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, true); err == nil {
		t.Fatal("real state in the demo directory must still be refused")
	} else if !strings.Contains(err.Error(), "demo mode mismatch") {
		t.Fatalf("err = %v, want the demo mismatch to be named", err)
	}
	if rejected := rejectedStates(t, dir); len(rejected) != 0 {
		t.Fatalf("nothing may be set aside: %v", rejected)
	}
	kept, err := os.ReadFile(statePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, real) {
		t.Fatal("a refused state must be left exactly as it was")
	}
}

func TestDemoOpenDoesNotAssumeAnUnparseableStateIsDemoState(t *testing.T) {
	dir := t.TempDir()
	truncated := []byte(`{"format": 1, "demo": true, "towns": {`)
	if err := os.WriteFile(statePath(dir), truncated, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir, true)
	if err == nil {
		t.Fatal("a file this Town cannot parse must still be reported")
	}
	if !strings.Contains(err.Error(), "move that file aside to start over") {
		t.Fatalf("err = %v, want the demo to say how to recover", err)
	}
	if rejected := rejectedStates(t, dir); len(rejected) != 0 {
		t.Fatalf("a file that cannot be parsed must not be set aside: %v", rejected)
	}
	kept, err := os.ReadFile(statePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, truncated) {
		t.Fatal("the unreadable state must be left exactly as it was")
	}
}

func TestDemoOpenKeepsAUsableDemoState(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(s *State) error {
		_, err := s.Add(DefaultConfig("BrokkAi/orchard"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if notice := reopened.Notice(); notice != "" {
		t.Fatalf("notice = %q, want none for a state that loads", notice)
	}
	if towns := reopened.Snapshot().Towns; len(towns) != 1 {
		t.Fatalf("the demo town must survive a reopen, got %d towns", len(towns))
	}
	if rejected := rejectedStates(t, dir); len(rejected) != 0 {
		t.Fatalf("a usable state must not be set aside: %v", rejected)
	}
}

// The demo seeds itself only when the store has no towns, so a reset state has
// to come back empty for the village to be rebuilt.
func TestResetDemoStateSeedsTheVillageAgain(t *testing.T) {
	dir := t.TempDir()
	staleDemoState(t, dir)
	store, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, store, time.Hour) }()
	deadline := time.Now().Add(time.Second)
	for len(store.Snapshot().Towns) != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	towns := store.Snapshot().Towns
	if len(towns) != 2 {
		t.Fatalf("towns = %d, want the two demo towns", len(towns))
	}
	for _, id := range []string{"brokkai/orchard", "brokkai/paper-trail"} {
		if towns[id] == nil {
			t.Fatalf("town %s is missing after the reset", id)
		}
		for _, role := range Roles {
			if towns[id].Workers[role] == nil {
				t.Fatalf("town %s is missing its %s house", id, role)
			}
		}
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("demo ended with %v", err)
	}
}

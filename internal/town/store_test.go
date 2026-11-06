package town

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

// An older Town kept its demo in a state file marked "demo": true. Demo mode is
// gone, and its simulated repositories must never be served as real ones.
func TestOpenRefusesDemoState(t *testing.T) {
	dir := t.TempDir()
	raw, err := json.Marshal(NewState())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["demo"] = json.RawMessage("true")
	if raw, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(dir), raw, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err == nil {
		store.Close()
		t.Fatal("demo state must be refused")
	}
	if !strings.Contains(err.Error(), "demo mode has been removed") {
		t.Fatalf("err = %v, want it to say demo mode was removed", err)
	}
	kept, err := os.ReadFile(statePath(dir))
	if err != nil || !bytes.Equal(kept, raw) {
		t.Fatalf("a refused state must be left exactly as it was: %v", err)
	}
}

// State saved before demo mode was removed carries "demo": false, and still
// opens.
func TestOpenKeepsStateMarkedNotDemo(t *testing.T) {
	dir := t.TempDir()
	state := NewState()
	if _, err := state.Add(DefaultConfig("BrokkAi/orchard")); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["demo"] = json.RawMessage("false")
	if raw, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(dir), raw, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if towns := store.Snapshot().Towns; len(towns) != 1 {
		t.Fatalf("towns = %d, want the saved repository", len(towns))
	}
}

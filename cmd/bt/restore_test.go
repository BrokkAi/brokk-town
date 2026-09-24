package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func deletedTown(t *testing.T, dir, repo string) {
	t.Helper()
	store, err := town.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(s *town.State) error {
		cfg := town.DefaultConfig(repo)
		cfg.MergePolicy = "all"
		x, e := s.Add(cfg)
		if e != nil {
			return e
		}
		x.Deleted = true
		for _, w := range x.Workers {
			w.Enabled = false
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func readTown(t *testing.T, dir, id string) *town.Town {
	t.Helper()
	store, err := town.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	return store.Snapshot().Towns[id]
}

func serveOnce(t *testing.T, dir, configFile string) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serve(ctx, dir, "127.0.0.1:0", false, configFile); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// A town listed in the config file is live: a deleted one is restored under
// the listed settings rather than updated as an invisible tombstone.
func TestServeConfigRestoresDeletedTown(t *testing.T) {
	dir := t.TempDir()
	deletedTown(t, dir, "acme/listed")
	path := filepath.Join(t.TempDir(), "towns.json")
	entry := `[{"repo":"acme/listed","harness":"custom","agent":{"command":["fake"]},"merge_policy":"manual","poll_seconds":60,"report_seconds":1800,"max_cycles":5}]`
	if err := os.WriteFile(path, []byte(entry), 0600); err != nil {
		t.Fatal(err)
	}
	if err := serveOnce(t, dir, path); err != nil {
		t.Fatal(err)
	}
	x := readTown(t, dir, "acme/listed")
	if x.Deleted || x.Config.MergePolicy != "manual" || !x.Workers[town.Repo].Enabled || x.Workers[town.Issue].Enabled {
		t.Fatal("config file did not restore the town", x.Deleted, x.Config.MergePolicy)
	}
}

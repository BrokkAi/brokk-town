package town

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func storageFixture(t *testing.T) (*Supervisor, string, string) {
	t.Helper()
	st := testStore(t, false)
	x := addTown(t, st)
	update(t, st, func(s *State) {
		x := s.Towns[x.ID]
		for _, w := range x.Workers {
			w.Enabled = false
		}
		x.Tasks["issue:1"] = &Task{ID: "issue:1", Kind: "issue", Number: 1, House: Issue, Stage: "closed"}
	})
	return NewSupervisor(st, nil, nil), x.ID, filepath.Dir(st.path)
}
func savedTranscript(t *testing.T, s *Supervisor, id, root, name string, complete bool) string {
	t.Helper()
	_, state := Workspace(root, id, Issue)
	path := filepath.Join(state, "sessions", name+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("private transcript data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := transcriptPaths(root, id, Issue)
	delete(before, path)
	b := BotWorkers{Root: root, Store: s.Store}
	b.recordTranscripts(id, Issue, before, RunResult{Issue: 1}, complete, slog.New(slog.NewTextHandler(io.Discard, nil)))
	update(t, s.Store, func(st *State) {
		for k, r := range st.Towns[id].Artifacts {
			r.Finished = time.Now().Add(-8 * 24 * time.Hour)
			st.Towns[id].Artifacts[k] = r
		}
	})
	return path
}
func findArtifact(t *testing.T, s *Supervisor, id, path string, age int) StorageArtifact {
	t.Helper()
	v, e := s.Storage(context.Background(), id, age)
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range v.Artifacts {
		if strings.HasSuffix(a.Path, path) {
			return a
		}
	}
	t.Fatalf("artifact %s missing: %+v", path, v)
	return StorageArtifact{}
}
func TestStorageCleanupPreservesIdentityAndRestart(t *testing.T) {
	s, id, root := storageFixture(t)
	path := savedTranscript(t, s, id, root, "done", true)
	a := findArtifact(t, s, strings.ToUpper(id), "done.jsonl", 168)
	if !a.Eligible || a.Task != "issue:1" || a.Bytes == 0 || a.Files != 1 {
		t.Fatalf("%+v", a)
	}
	if fresh := findArtifact(t, s, id, "done.jsonl", 24*9); fresh.Eligible {
		t.Fatal("ignored retention age")
	}
	raw, _ := json.Marshal(s.Store.Snapshot().Public())
	if strings.Contains(string(raw), "artifacts") || strings.Contains(string(raw), "done.jsonl") {
		t.Fatal("private receipt leaked")
	}
	result, err := s.CleanupStorage(context.Background(), id, 168, []string{a.ID})
	if err != nil || len(result) != 1 || result[0].Status != "removed" {
		t.Fatal(result, err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("transcript survived", err)
	}
	if s.Store.Snapshot().Towns[id].Tasks["issue:1"] == nil {
		t.Fatal("task identity lost")
	}
	if _, err = s.CleanupStorage(context.Background(), id, 168, []string{a.ID}); err == nil {
		t.Fatal("stale receipt accepted")
	}
	s.Store.Close()
	reopened, err := Open(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Snapshot().Towns[id].Tasks["issue:1"] == nil {
		t.Fatal("restart lost task")
	}
}
func TestStorageRetainsUnfinishedUnknownChangedAndSymlinkedEvidence(t *testing.T) {
	for _, scenario := range []string{"failed", "unknown", "changed", "unfinished", "symlink", "active", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			s, id, root := storageFixture(t)
			path := savedTranscript(t, s, id, root, "keep", scenario != "failed")
			a := findArtifact(t, s, id, "keep.jsonl", 0)
			switch scenario {
			case "unknown":
				update(t, s.Store, func(st *State) { st.Towns[id].Artifacts = nil })
			case "changed":
				if err := os.WriteFile(path, []byte("unfinished edits"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unfinished":
				update(t, s.Store, func(st *State) { st.Towns[id].Tasks["issue:1"].Stage = "queued" })
			case "symlink":
				outside := filepath.Join(t.TempDir(), "precious")
				os.WriteFile(outside, []byte("private"), 0600)
				os.Remove(path)
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "active":
				update(t, s.Store, func(st *State) { st.Towns[id].Workers[Issue].Enabled = true })
			case "uncertain":
				update(t, s.Store, func(st *State) {
					st.Towns[id].Intents[1] = &Intent{PR: 1, Kind: "merge", Base: baseSHA, Head: headSHA, Status: "uncertain"}
				})
			}
			if kept := findArtifact(t, s, id, "keep.jsonl", 0); kept.Eligible {
				t.Fatal("unsafe artifact eligible", kept)
			}
			if _, err := s.CleanupStorage(context.Background(), id, 0, []string{a.ID}); err == nil {
				t.Fatal("unsafe selection cleaned")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("retained evidence removed", err)
			}
		})
	}
}
func TestStorageReservationBlocksWorkersAndStandaloneLocks(t *testing.T) {
	s, id, root := storageFixture(t)
	savedTranscript(t, s, id, root, "keep", true)
	release, err := s.beginStorage(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.beginStorage(id); err == nil {
		t.Fatal("duplicate cleanup")
	}
	if err = s.Control(id, Issue, "start", ""); err == nil {
		t.Fatal("worker started during cleanup")
	}
	if ok, _ := s.Store.dispatchEligibility(id, Issue, time.Now()); ok {
		t.Fatal("dispatch allowed")
	}
	// Unrelated bookkeeping can persist without killing the service.
	update(t, s.Store, func(st *State) { st.Towns[id].LastSync = time.Now() })
	release()
	lock, err := lockStorage(root, id)
	if err != nil {
		t.Fatal(err)
	}
	a := findArtifact(t, s, id, "keep.jsonl", 0)
	if _, err = s.CleanupStorage(context.Background(), id, 0, []string{a.ID}); err == nil {
		t.Fatal("standalone lock ignored")
	}
	lock()
	if s.Store.storageHeld(id) {
		t.Fatal("failed cleanup leaked reservation")
	}
	if err = s.Control(id, Issue, "start", ""); err != nil {
		t.Fatal(err)
	}
}
func TestStorageCancellationAndDemoDoNotRemove(t *testing.T) {
	s, id, root := storageFixture(t)
	path := savedTranscript(t, s, id, root, "keep", true)
	a := findArtifact(t, s, id, "keep.jsonl", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inventory, err := s.Storage(ctx, id, 0)
	if err != nil || !inventory.Incomplete {
		t.Fatal(inventory, err)
	}
	if _, err = s.CleanupStorage(ctx, id, 0, []string{a.ID}); err == nil {
		t.Fatal("canceled cleanup accepted")
	}
	demo := NewSupervisor(testStore(t, true), nil, nil)
	if _, err = demo.CleanupStorage(context.Background(), id, 0, []string{a.ID}); err == nil {
		t.Fatal("demo cleanup allowed")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
func TestStorageRepairCollectionRequiresCleanConfirmedExactReceipt(t *testing.T) {
	for _, scenario := range []string{"confirmed", "unknown", "uncertain", "dirty", "ignored", "moved"} {
		t.Run(scenario, func(t *testing.T) {
			b, x, _, _ := fixtureWorkers(t)
			ctx := context.Background()
			p, err := b.GitHub.Pull(ctx, x.Config.Repo, 1)
			if err != nil {
				t.Fatal(err)
			}
			tree, err := b.tree(ctx, x, p, "repair", true)
			if err != nil {
				t.Fatal(err)
			}
			defer tree.close()
			if scenario != "unknown" {
				x.Intents[1] = &Intent{PR: 1, Kind: "repair", Status: "confirmed", Directory: tree.dir, Base: p.Base.SHA, Head: p.Head.SHA, NewHead: p.Head.SHA, Branch: p.Head.Ref}
			}
			switch scenario {
			case "uncertain":
				x.Intents[1].Status = "uncertain"
			case "dirty":
				os.WriteFile(filepath.Join(tree.dir, "precious"), []byte("unfinished"), 0600)
			case "ignored":
				common, _ := git(ctx, tree.dir, "rev-parse", "--git-common-dir")
				if !filepath.IsAbs(common) {
					common = filepath.Join(tree.dir, common)
				}
				os.MkdirAll(filepath.Join(common, "info"), 0700)
				os.WriteFile(filepath.Join(common, "info", "exclude"), []byte("precious\n"), 0600)
				os.WriteFile(filepath.Join(tree.dir, "precious"), []byte("unfinished"), 0600)
			case "moved":
				x.Intents[1].NewHead = baseSHA
			}
			if err = b.CollectWorktrees(ctx, x); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(tree.dir)
			if scenario == "confirmed" {
				if !os.IsNotExist(err) {
					t.Fatal("confirmed tree retained", err)
				}
			} else if err != nil {
				t.Fatal("unsafe collection lost work", err)
			}
		})
	}
}

func TestStorageDoesNotInferReceiptsFromIncompleteDiscovery(t *testing.T) {
	s, id, root := storageFixture(t)
	path := savedTranscript(t, s, id, root, "old", true)
	update(t, s.Store, func(st *State) { st.Towns[id].Artifacts = nil })
	b := BotWorkers{Root: root, Store: s.Store}
	b.recordTranscripts(id, Issue, nil, RunResult{Issue: 1}, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(s.Store.Snapshot().Towns[id].Artifacts) != 0 {
		t.Fatal("incomplete discovery promoted old evidence")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	release, err := s.beginStorage(id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = s.Store.Update(func(st *State) error { st.Towns[id].Tasks["issue:1"].Stage = "queued"; return nil }); err == nil {
		t.Fatal("terminal task reopened during cleanup")
	}
}

func TestStorageExplicitRepairRemovalPreservesWriteReceipt(t *testing.T) {
	b, x, _, _ := fixtureWorkers(t)
	b.Root = filepath.Dir(b.Store.path)
	ctx := context.Background()
	p, err := b.GitHub.Pull(ctx, x.Config.Repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := b.tree(ctx, x, p, "repair", true)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.close()
	update(t, b.Store, func(st *State) {
		x := st.Towns[x.ID]
		for _, w := range x.Workers {
			w.Enabled = false
		}
		x.Intents[1] = &Intent{PR: 1, Kind: "repair", Status: "confirmed", Directory: tree.dir, Base: p.Base.SHA, Head: p.Head.SHA, NewHead: p.Head.SHA, Branch: p.Head.Ref}
	})
	s := NewSupervisor(b.Store, nil, b)
	a := findArtifact(t, s, x.ID, filepath.Base(tree.dir), 0)
	if !a.Eligible {
		t.Fatal(a)
	}
	result, err := s.CleanupStorage(ctx, x.ID, 0, []string{a.ID})
	if err != nil || result[0].Status != "removed" {
		t.Fatal(result, err)
	}
	if s.Store.Snapshot().Towns[x.ID].Intents[1].NewHead != p.Head.SHA {
		t.Fatal("saved write identity removed")
	}
	if dirs, branches := worktrees(t, b, x.ID); len(dirs) != 0 || len(branches) != 0 {
		t.Fatal(dirs, branches)
	}
}

package featurebot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func completedFixture(t *testing.T) (engine, *State, CompletedWorkspace) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", canonicalTestDir(t))
	e, s, _, _, _ := fixture(t)
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadState(e.config)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Scan != nil || len(saved.Workspaces) != 1 {
		t.Fatalf("missing completion: %+v", saved)
	}
	return e, saved, saved.Workspaces[0]
}

func TestPrunePreviewAgeAndApply(t *testing.T) {
	e, s, w := completedFixture(t)
	ctx := context.Background()
	writeTestFile(t, filepath.Join(w.Directory, "research.txt"), "research")
	writeTestFile(t, filepath.Join(w.Directory, ".gitignore"), "ignored\n")
	writeTestFile(t, filepath.Join(w.Directory, "ignored"), "artifact")
	transcript := filepath.Join(e.config.StateDirectory, "session.log")
	writeTestFile(t, transcript, "retained transcript")
	// Pruning is local: any fetch from the moved remote would fail.
	if err := os.Rename(e.config.Remote, e.config.Remote+"-offline"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(e.config.StateDirectory, "state.json"))
	var out bytes.Buffer
	boundary := w.CompletedAt.Add(time.Hour)
	if err := prune(ctx, e.config, time.Hour, false, &out, boundary); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not older") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := prune(ctx, e.config, time.Hour, false, &out, boundary.Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "eligible") {
		t.Fatal(out.String())
	}
	after, _ := os.ReadFile(filepath.Join(e.config.StateDirectory, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("preview changed state")
	}
	if _, err := os.Stat(w.Directory); err != nil {
		t.Fatal(err)
	}
	if err := prune(ctx, e.config, time.Hour, true, &out, boundary.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace survives: %v", err)
	}
	if strings.Contains(localGit(t, e.config.Directory, "worktree", "list", "--porcelain"), w.Directory) {
		t.Fatal("registration survives")
	}
	afterState, err := ReadState(e.config)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(transcript); err != nil || string(data) != "retained transcript" {
		t.Fatal("transcript changed", err)
	}
	if !reflect.DeepEqual(afterState.Completed, s.Completed) {
		t.Fatal("saved proposals changed")
	}
	if len(afterState.Workspaces) != 0 || strings.Join(afterState.History, "\n") != strings.Join(s.History, "\n") {
		t.Fatal("wrong saved state")
	}
	if err := prune(ctx, e.config, time.Hour, true, &out, boundary.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestPruneProtectedWorkspaces(t *testing.T) {
	for _, kind := range []string{"tracked", "staged", "locked", "attached", "head", "foreign", "symlink", "parent-symlink", "active", "ambiguous", "active-alias", "assume-unchanged", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			e, s, w := completedFixture(t)
			switch kind {
			case "tracked":
				writeTestFile(t, filepath.Join(w.Directory, "README.md"), "changed")
			case "staged":
				writeTestFile(t, filepath.Join(w.Directory, "README.md"), "changed")
				localGit(t, w.Directory, "add", "README.md")
			case "locked":
				localGit(t, e.config.Directory, "worktree", "lock", w.Directory)
			case "attached":
				localGit(t, w.Directory, "switch", "-c", "keep")
			case "head":
				localGit(t, w.Directory, "commit", "--allow-empty", "-m", "changed HEAD")
			case "foreign":
				localGit(t, e.config.Directory, "worktree", "remove", w.Directory)
				localGit(t, e.config.Directory, "clone", e.config.Remote, w.Directory)
			case "symlink":
				target := w.Directory + "-preserved"
				if err := os.Rename(w.Directory, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, w.Directory); err != nil {
					t.Fatal(err)
				}
			case "parent-symlink":
				parent := filepath.Dir(w.Directory)
				if err := os.Rename(parent, parent+"-preserved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(parent+"-preserved", parent); err != nil {
					t.Fatal(err)
				}
			case "active":
				// Exhausted retries keep the scan active.
				s.Scan = &Scan{Directory: w.Directory, Commit: w.Commit, Tries: 999, Failure: "research failed"}
			case "ambiguous":
				c := &Candidate{RequestID: strings.Repeat("b", 32), Finding: finding(), Status: "posting"}
				s.Scan = &Scan{Directory: w.Directory, Commit: w.Commit, Tries: 1, Discovered: true, Candidates: []*Candidate{c}}
			case "active-alias":
				alias := filepath.Join(filepath.Dir(w.Directory), "scan-active")
				if err := os.Symlink(w.Directory, alias); err != nil {
					t.Fatal(err)
				}
				s.Scan = &Scan{Directory: alias, Commit: w.Commit, Tries: 999}
			case "assume-unchanged":
				localGit(t, w.Directory, "update-index", "--assume-unchanged", "README.md")
				writeTestFile(t, filepath.Join(w.Directory, "README.md"), "changed")
			case "legacy":
				s.Workspaces = nil
			}
			if err := writeState(e.config, s); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(e.config.StateDirectory, "state.json"))
			var out bytes.Buffer
			if err := prune(context.Background(), e.config, time.Hour, true, &out, w.CompletedAt.Add(2*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(w.Directory); err != nil {
				t.Fatal("protected path removed", err)
			}
			after, _ := os.ReadFile(filepath.Join(e.config.StateDirectory, "state.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("protected state changed")
			}
			if kind != "legacy" && !strings.Contains(out.String(), "skip ") || kind == "legacy" && !strings.Contains(out.String(), "externally managed") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestPruneInterruptedRemoval(t *testing.T) {
	for _, registered := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing-directory", false: "removed-before-save"}[registered], func(t *testing.T) {
			e, _, w := completedFixture(t)
			if registered {
				if err := os.RemoveAll(w.Directory); err != nil {
					t.Fatal(err)
				}
			} else {
				localGit(t, e.config.Directory, "worktree", "remove", w.Directory)
			}
			var out bytes.Buffer
			if err := prune(context.Background(), e.config, time.Hour, true, &out, w.CompletedAt.Add(2*time.Hour)); err != nil {
				t.Fatal(err)
			}
			s, err := ReadState(e.config)
			if err != nil || len(s.Workspaces) != 0 {
				t.Fatalf("%+v %v", s, err)
			}
			if strings.Contains(localGit(t, e.config.Directory, "worktree", "list", "--porcelain"), w.Directory) {
				t.Fatal("registration survives")
			}
		})
	}
}

func TestPruneRefusesResearchLocks(t *testing.T) {
	e, _, w := completedFixture(t)
	unlock, err := lockConfig(e.config)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	var out bytes.Buffer
	if err := prune(context.Background(), e.config, time.Hour, true, &out, w.CompletedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("ignored research lock")
	}
}

func TestCompletedWorkspaceValidation(t *testing.T) {
	for _, kind := range []string{"commit", "time", "path", "escape", "relative", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			e, s, w := completedFixture(t)
			switch kind {
			case "commit":
				s.Workspaces[0].Commit = "bad"
			case "time":
				s.Workspaces[0].CompletedAt = time.Time{}
			case "path":
				s.Workspaces[0].Directory = e.config.Directory
			case "escape":
				s.Workspaces[0].Directory = e.config.Directory + "-scans/scan-x/../../checkout"
			case "relative":
				s.Workspaces[0].Directory = "scan-x"
			case "duplicate":
				s.Workspaces = append(s.Workspaces, w)
			}
			if err := writeState(e.config, s); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadState(e.config); err == nil {
				t.Fatal("accepted invalid completion")
			}
		})
	}
}

func TestCompletionLifecycle(t *testing.T) {
	for _, kind := range []string{"zero", "dry-run", "submitted", "failed", "discarded", "stale"} {
		t.Run(kind, func(t *testing.T) {
			e, s, _, a, _ := fixture(t)
			if kind == "dry-run" || kind == "submitted" {
				a.findings = []Finding{finding()}
			}
			if kind == "zero" {
				a.findings = []Finding{}
			}
			e.config.DryRun = kind == "dry-run"
			if kind == "failed" {
				a.err = errors.New("failed research")
			}
			if kind == "discarded" || kind == "stale" {
				// Revision changes discard undiscovered scans and stale pending findings.
				s.Scan = &Scan{Directory: filepath.Join(e.config.Directory+"-scans", "scan-discard"), Commit: strings.Repeat("a", 40)}
				if kind == "stale" {
					s.Scan.Discovered = true
					s.Scan.Candidates = []*Candidate{{RequestID: strings.Repeat("c", 32), Finding: finding(), Status: "stale"}}
				}
				if err := e.finish(s); err != nil {
					t.Fatal(err)
				}
			} else {
				err := e.step(context.Background(), s, true)
				if (err != nil) != (kind == "failed") {
					t.Fatalf("unexpected result: %v", err)
				}
			}
			saved, err := ReadState(e.config)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if kind == "failed" || kind == "discarded" || kind == "stale" {
				want = 0
			}
			if len(saved.Workspaces) != want {
				t.Fatalf("records: %+v", saved.Workspaces)
			}
			if want == 1 {
				w, h := saved.Workspaces[0], saved.History[len(saved.History)-1]
				if !strings.HasPrefix(h, w.Commit+": ") || !strings.HasPrefix(filepath.Base(w.Directory), "scan-") || time.Since(w.CompletedAt) > time.Minute {
					t.Fatalf("wrong record %+v for %q", w, h)
				}
				if _, err := os.Stat(w.Directory); err != nil {
					t.Fatal("completed workspace not retained", err)
				}
			}
		})
	}
}

func TestPrunePartialRemovalFailure(t *testing.T) {
	e, s, first := completedFixture(t)
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	second := s.Workspaces[1]
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := canonicalTestDir(t)
	// Fail just one Git removal, leaving other local Git operations intact.
	script := "#!/bin/sh\nif [ \"$1\" = worktree ] && [ \"$2\" = remove ] && [ \"$5\" = '" + first.Directory + "' ]; then exit 1; fi\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	now := second.CompletedAt.Add(2 * time.Hour)
	if err := prune(context.Background(), e.config, time.Hour, true, &out, now); err == nil {
		t.Fatal("removal failure lost")
	}
	saved, err := ReadState(e.config)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Workspaces) != 1 || saved.Workspaces[0].Directory != first.Directory {
		t.Fatalf("wrong partial state: %+v", saved.Workspaces)
	}
	if _, err := os.Stat(second.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("second workspace not removed")
	}
	if err := os.Remove(filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	if err := prune(context.Background(), e.config, time.Hour, true, &out, now); err != nil {
		t.Fatal(err)
	}
}

func TestPruneMissingWorkspacePreservesStagedData(t *testing.T) {
	e, _, w := completedFixture(t)
	writeTestFile(t, filepath.Join(w.Directory, "README.md"), "staged research")
	localGit(t, w.Directory, "add", "README.md")
	if err := os.RemoveAll(w.Directory); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := prune(context.Background(), e.config, time.Hour, true, &out, w.CompletedAt.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadState(e.config)
	if err != nil || len(saved.Workspaces) != 1 {
		t.Fatal("lost staged workspace record", err)
	}
	if !strings.Contains(out.String(), "staged modifications") {
		t.Fatal(out.String())
	}
	if !strings.Contains(localGit(t, e.config.Directory, "worktree", "list", "--porcelain"), w.Directory) {
		t.Fatal("lost staged index registration")
	}
}

func TestPruneFinishesRemovalWithoutGitFile(t *testing.T) {
	for _, kind := range []string{"partial", "modified", "staged"} {
		t.Run(kind, func(t *testing.T) {
			e, _, w := completedFixture(t)
			if kind == "staged" {
				writeTestFile(t, filepath.Join(w.Directory, "README.md"), "staged research")
				localGit(t, w.Directory, "add", "README.md")
			}
			// An interrupted git worktree remove can delete .git and some files first.
			if err := os.Remove(filepath.Join(w.Directory, ".git")); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "partial":
				if err := os.Remove(filepath.Join(w.Directory, "README.md")); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(w.Directory, "research.txt"), "research")
			case "modified":
				writeTestFile(t, filepath.Join(w.Directory, "README.md"), "changed")
			}
			var out bytes.Buffer
			now := w.CompletedAt.Add(2 * time.Hour)
			if err := prune(context.Background(), e.config, time.Hour, false, &out, now); err != nil {
				t.Fatal(err)
			}
			if err := prune(context.Background(), e.config, time.Hour, true, &out, now); err != nil {
				t.Fatal(err)
			}
			s, err := ReadState(e.config)
			if err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(w.Directory)
			registered := strings.Contains(localGit(t, e.config.Directory, "worktree", "list", "--porcelain"), w.Directory)
			if kind == "partial" {
				if !strings.Contains(out.String(), "interrupted removal") || !errors.Is(statErr, os.ErrNotExist) || registered || len(s.Workspaces) != 0 {
					t.Fatalf("interrupted removal not finished: %s %v %v %+v", out.String(), statErr, registered, s.Workspaces)
				}
				return
			}
			if !strings.Contains(out.String(), "skip ") || statErr != nil || !registered || len(s.Workspaces) != 1 {
				t.Fatalf("unsafe interrupted removal finished: %s %v %v %+v", out.String(), statErr, registered, s.Workspaces)
			}
		})
	}
}

func TestPruneOperationalErrorsFail(t *testing.T) {
	e, _, w := completedFixture(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := canonicalTestDir(t)
	script := "#!/bin/sh\nif [ \"$1\" = worktree ] && [ \"$2\" = list ]; then exit 128; fi\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	if err := prune(context.Background(), e.config, time.Hour, false, &out, w.CompletedAt.Add(2*time.Hour)); err == nil || !strings.Contains(out.String(), "skip ") {
		t.Fatalf("Git failure reported as success: %v %s", err, out.String())
	}
	if _, err := os.Stat(w.Directory); err != nil {
		t.Fatal(err)
	}
}

func TestPruneIgnoresRedirectingGitEnvironment(t *testing.T) {
	e, _, w := completedFixture(t)
	bogus := canonicalTestDir(t)
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY"} {
		t.Setenv(key, bogus)
	}
	var out bytes.Buffer
	if err := prune(context.Background(), e.config, time.Hour, true, &out, w.CompletedAt.Add(2*time.Hour)); err != nil {
		t.Fatal(err, out.String())
	}
	if _, err := os.Stat(w.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace survives: %v %s", err, out.String())
	}
}

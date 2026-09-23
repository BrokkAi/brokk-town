package releasebot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// triggerFixture has a verified release at the fixture head, an ignore policy
// for docs/internal/ and NOTES.md, and an agent that counts triage and
// preparation sessions without publishing anything.
type triggerFixture struct {
	*fixture
	triages, sessions int
	last              Progress
}

func newTriggerFixture(t *testing.T, releasedAgo time.Duration) *triggerFixture {
	t.Helper()
	tf := &triggerFixture{fixture: newFixture(t)}
	cfg := &tf.engine.config
	cfg.ReleaseTriggerIgnore = []string{"docs/internal/", "NOTES.md"}
	cfg.MinimumGap, cfg.Quiet, cfg.Burst = 0, 0, 1
	tf.engine.agent = triageAgent{
		scriptedAgent: func(context.Context, string) (Result, error) {
			tf.sessions++
			return Result{}, errors.New("fixture agent does not prepare releases")
		},
		triage: func(context.Context, string) (TriageDecision, error) {
			tf.triages++
			return TriageDecision{Decision: "release", Reason: "fixture wants a release"}, nil
		},
	}
	tf.engine.observe = func(p Progress) {
		if p.Phase != "" {
			tf.last = p
		}
	}
	if err := writeState(*cfg, &State{Format: 1, Remote: cfg.Remote, Branch: cfg.Branch, Directory: cfg.Directory, Released: tf.head, ReleasedAt: tf.now.Add(-releasedAgo)}); err != nil {
		t.Fatal(err)
	}
	return tf
}

// commit writes files (an empty content deletes the file) in dir and commits.
func commitFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if content == "" {
			localGit(t, dir, "rm", "-q", name)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, path, content)
		localGit(t, dir, "add", name)
	}
	localGit(t, dir, "commit", "-q", "-m", "change")
}

func (tf *triggerFixture) push(t *testing.T, files map[string]string) {
	t.Helper()
	commitFiles(t, tf.source, files)
	localGit(t, tf.source, "push", "-q", "origin", "master")
}

// deferred runs one cycle and reports whether it only kept monitoring.
func (tf *triggerFixture) deferred(t *testing.T, force bool) bool {
	t.Helper()
	triages, sessions := tf.triages, tf.sessions
	err := tf.engine.cycle(context.Background(), force)
	state := fixtureState(t, tf.fixture)
	if state.Job == nil {
		if err != nil {
			t.Fatal(err)
		}
		if tf.triages != triages || tf.sessions != sessions {
			t.Fatal("agent ran although no release job started")
		}
		return true
	}
	if tf.sessions == sessions {
		t.Fatalf("release job started without preparation: %v", err)
	}
	return false
}

func TestIgnoredChangesDeferDeadlineBurstAndTriage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ago   time.Duration
		burst int
	}{{"daily deadline", 48 * time.Hour, 1}, {"commit burst", time.Hour, 1}, {"triage", time.Hour, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			ago := tc.ago
			tf := newTriggerFixture(t, ago)
			tf.engine.config.Burst = tc.burst
			tf.push(t, map[string]string{"docs/internal/plan.md": "notes", "NOTES.md": "notes"})
			if !tf.deferred(t, false) || tf.triages != 0 {
				t.Fatal("ignored changes started triage or preparation")
			}
			if tf.last.Phase != "waiting" || !strings.Contains(tf.last.Task, "release_trigger_ignore") || tf.engine.unreleased != 1 {
				t.Fatalf("deferral was not explained: %+v", tf.last)
			}
			state := fixtureState(t, tf.fixture)
			if state.Released != tf.head || !state.ReleasedAt.Equal(tf.now.Add(-ago)) {
				t.Fatal("deferral advanced the release baseline")
			}
			// A restarted daemon reaches the same decision from Git.
			tf.now = tf.now.Add(time.Hour)
			restarted := *tf.engine
			restarted.starting = true
			tf.engine = &restarted
			if !tf.deferred(t, false) {
				t.Fatal("restart lost the deferral")
			}
		})
	}
}

func TestReleaseRelevantChangeMakesWholeRangeEligible(t *testing.T) {
	tf := newTriggerFixture(t, 48*time.Hour)
	tf.push(t, map[string]string{"docs/internal/plan.md": "notes"})
	if !tf.deferred(t, false) {
		t.Fatal("ignored change started a release")
	}
	tf.push(t, map[string]string{"main.go": "package main"})
	if tf.deferred(t, false) {
		t.Fatal("release-relevant change stayed deferred")
	}
	if tf.engine.unreleased != 2 {
		t.Fatalf("release range omitted deferred commits: %d", tf.engine.unreleased)
	}
	if job := fixtureState(t, tf.fixture).Job; job.Target != localGit(t, tf.source, "rev-parse", "HEAD") {
		t.Fatal("release targets less than the unreleased range")
	}
}

func TestMixedDeletedAndRenamedPathsAreReleaseRelevant(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare map[string]string
		change  func(t *testing.T, dir string)
		defer_  bool
	}{
		{"mixed", nil, func(t *testing.T, dir string) {
			commitFiles(t, dir, map[string]string{"docs/internal/a.md": "a", "src/a.go": "a"})
		}, false},
		{"deletion", nil, func(t *testing.T, dir string) { commitFiles(t, dir, map[string]string{"manifest": ""}) }, false},
		{"ignored deletion", map[string]string{"docs/internal/old.md": "old"}, func(t *testing.T, dir string) {
			commitFiles(t, dir, map[string]string{"docs/internal/old.md": ""})
		}, true},
		{"rename out of ignored directory", map[string]string{"docs/internal/guide.md": "guide text"}, func(t *testing.T, dir string) {
			localGit(t, dir, "mv", "docs/internal/guide.md", "docs/guide.md")
			localGit(t, dir, "commit", "-q", "-m", "publish guide")
		}, false},
		{"rename into ignored directory", nil, func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, "docs/internal"), 0700); err != nil {
				t.Fatal(err)
			}
			localGit(t, dir, "mv", "manifest", "docs/internal/manifest")
			localGit(t, dir, "commit", "-q", "-m", "retire manifest")
		}, false},
		{"directory prefix is literal", nil, func(t *testing.T, dir string) {
			commitFiles(t, dir, map[string]string{"docs/internal-api/a.md": "a", "docs/NOTES.md": "a"})
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tf := newTriggerFixture(t, 48*time.Hour)
			if tc.prepare != nil {
				// Establish the prepared files as part of the verified release.
				tf.push(t, tc.prepare)
				state := fixtureState(t, tf.fixture)
				state.Released = localGit(t, tf.source, "rev-parse", "HEAD")
				if err := writeState(tf.engine.config, state); err != nil {
					t.Fatal(err)
				}
			}
			tc.change(t, tf.source)
			localGit(t, tf.source, "push", "-q", "origin", "master")
			if got := tf.deferred(t, false); got != tc.defer_ {
				t.Fatalf("deferred = %v", got)
			}
		})
	}
}

func TestDivergentLocalAndRemoteTipsAreBothInspected(t *testing.T) {
	for _, tc := range []struct {
		name          string
		local, remote string
		defer_        bool
	}{
		{"remote change", "docs/internal/local.md", "main.go", false},
		{"local change", "main.go", "docs/internal/remote.md", false},
		{"both ignored", "docs/internal/local.md", "NOTES.md", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tf := newTriggerFixture(t, 48*time.Hour)
			if err := tf.engine.git.open(context.Background()); err != nil {
				t.Fatal(err)
			}
			commitFiles(t, tf.engine.config.Directory, map[string]string{tc.local: "local"})
			tf.push(t, map[string]string{tc.remote: "remote"})
			if got := tf.deferred(t, false); got != tc.defer_ {
				t.Fatalf("deferred = %v", got)
			}
		})
	}
}

func TestPendingJobRecoveryIgnoresTriggerPolicy(t *testing.T) {
	tf := newTriggerFixture(t, 48*time.Hour)
	tf.push(t, map[string]string{"docs/internal/plan.md": "notes"})
	if err := tf.engine.git.open(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := fixtureState(t, tf.fixture)
	branch, err := tf.engine.git.startBranch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state.Job = &Job{Target: localGit(t, tf.source, "rev-parse", "HEAD"), WorkBranch: branch, Started: tf.now}
	if err := writeState(tf.engine.config, state); err != nil {
		t.Fatal(err)
	}
	if tf.deferred(t, false) {
		t.Fatal("pending job was not resumed")
	}
}

func TestTriggerPolicyPreservesDefaultsFirstReleaseAndForce(t *testing.T) {
	t.Run("empty policy", func(t *testing.T) {
		tf := newTriggerFixture(t, 48*time.Hour)
		tf.engine.config.ReleaseTriggerIgnore = nil
		tf.push(t, map[string]string{"docs/internal/plan.md": "notes"})
		if tf.deferred(t, false) {
			t.Fatal("empty policy deferred a release")
		}
	})
	t.Run("no previous release", func(t *testing.T) {
		tf := newTriggerFixture(t, 0)
		state := fixtureState(t, tf.fixture)
		state.ReleasedAt = time.Time{}
		if err := writeState(tf.engine.config, state); err != nil {
			t.Fatal(err)
		}
		tf.push(t, map[string]string{"docs/internal/plan.md": "notes"})
		if tf.deferred(t, false) {
			t.Fatal("first release was deferred")
		}
	})
	t.Run("empty net diff", func(t *testing.T) {
		tf := newTriggerFixture(t, 48*time.Hour)
		commitFiles(t, tf.source, map[string]string{"docs/internal/plan.md": "notes"})
		localGit(t, tf.source, "revert", "--no-edit", "HEAD")
		localGit(t, tf.source, "push", "-q", "origin", "master")
		if tf.deferred(t, false) {
			t.Fatal("empty net diff was deferred")
		}
	})
	t.Run("force", func(t *testing.T) {
		tf := newTriggerFixture(t, time.Hour)
		tf.engine.config.Triage = false
		tf.engine.config.Burst = 0
		tf.push(t, map[string]string{"docs/internal/plan.md": "notes"})
		if tf.deferred(t, true) {
			t.Fatal("force did not override the exclusions")
		}
	})
	t.Run("force without unreleased commits", func(t *testing.T) {
		tf := newTriggerFixture(t, time.Hour)
		if !tf.deferred(t, true) || tf.engine.unreleased != 0 {
			t.Fatal("force started a release without unreleased commits")
		}
	})
}

func TestTriggerInspectionFailureIsVisible(t *testing.T) {
	tf := newTriggerFixture(t, 48*time.Hour)
	tf.push(t, map[string]string{"docs/internal/plan.md": "notes"})
	if !tf.deferred(t, false) {
		t.Fatal("ignored change started a release")
	}
	// The released tree is unreadable, so the paths cannot be compared.
	repository := tf.engine.git.repositoryDirectory()
	tree := localGit(t, repository, "rev-parse", tf.head+"^{tree}")
	object := filepath.Join(repository, "objects", tree[:2], tree[2:])
	if err := os.Remove(object); err != nil {
		t.Skipf("released tree is not a loose object: %v", err)
	}
	err := tf.engine.cycle(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "release_trigger_ignore") {
		t.Fatalf("inspection failure was not reported: %v", err)
	}
	if tf.triages != 0 || tf.sessions != 0 || fixtureState(t, tf.fixture).Job != nil {
		t.Fatal("inspection failure started release work")
	}
}

func TestChangedPathsListBothSidesOfRenames(t *testing.T) {
	tf := newTriggerFixture(t, 0)
	tf.push(t, map[string]string{"docs/internal/guide.md": "guide text", " spaced name": "x", "line\nbreak": "x", "docs/ünïcødé.md": "x"})
	before := localGit(t, tf.source, "rev-parse", "HEAD")
	localGit(t, tf.source, "mv", "docs/internal/guide.md", "docs/guide.md")
	localGit(t, tf.source, "commit", "-q", "-m", "move")
	localGit(t, tf.source, "push", "-q", "origin", "master")
	if err := tf.engine.git.open(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := localGit(t, tf.source, "rev-parse", "HEAD")
	paths, err := tf.engine.git.changedPaths(context.Background(), before, after)
	if err != nil || strings.Join(paths, ",") != "docs/guide.md,docs/internal/guide.md" {
		t.Fatalf("rename paths: %q %v", paths, err)
	}
	paths, err = tf.engine.git.changedPaths(context.Background(), tf.head, before)
	if err != nil || strings.Join(paths, "|") != " spaced name|docs/internal/guide.md|docs/ünïcødé.md|line\nbreak" {
		t.Fatalf("unusual path names: %q %v", paths, err)
	}
	if _, err := tf.engine.git.changedPaths(context.Background(), tf.head, strings.Repeat("0", 40)); err == nil {
		t.Fatal("missing commit compared as unchanged")
	}
}

func TestSubmoduleBumpIsReleaseRelevantDespiteIgnoreSubmodulesConfig(t *testing.T) {
	tf := newTriggerFixture(t, 48*time.Hour)
	link := func(commit string) {
		localGit(t, tf.source, "update-index", "--add", "--cacheinfo", "160000,"+commit+",vendor/sub")
	}
	link(strings.Repeat("1", 40))
	localGit(t, tf.source, "commit", "-q", "-m", "add submodule")
	localGit(t, tf.source, "push", "-q", "origin", "master")
	state := fixtureState(t, tf.fixture)
	state.Released = localGit(t, tf.source, "rev-parse", "HEAD")
	if err := writeState(tf.engine.config, state); err != nil {
		t.Fatal(err)
	}
	if err := tf.engine.git.open(context.Background()); err != nil {
		t.Fatal(err)
	}
	// An operator setting must not hide the bump from the comparison.
	localGit(t, tf.engine.git.repositoryDirectory(), "config", "diff.ignoreSubmodules", "all")
	if err := os.MkdirAll(filepath.Join(tf.source, "docs/internal"), 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(tf.source, "docs/internal/plan.md"), "notes")
	localGit(t, tf.source, "add", "docs/internal/plan.md")
	link(strings.Repeat("2", 40))
	localGit(t, tf.source, "commit", "-q", "-m", "docs and submodule bump")
	localGit(t, tf.source, "push", "-q", "origin", "master")
	if tf.deferred(t, false) {
		t.Fatal("submodule bump was hidden by diff.ignoreSubmodules")
	}
}

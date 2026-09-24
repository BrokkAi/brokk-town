package mayorbot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrokkAi/mayor-bot/internal/osrun"
)

// originGit runs git inside a test's origin repository with a fixed identity.
func originGit(t *testing.T, dir string) func(...string) string {
	t.Helper()
	return func(args ...string) string {
		t.Helper()
		out, err := osrun.Run(context.Background(), dir, map[string]string{
			"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
			"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
		}, append([]string{"git"}, args...)...)
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return out
	}
}

// originRepo builds a local remote so checkout exercises real git plumbing
// without network access or a live agent.
func originRepo(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := t.TempDir()
	run := originGit(t, dir)
	run("init", "--initial-branch", "master")
	run("commit", "--allow-empty", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("origin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "readme")
	return dir, run("rev-parse", "HEAD")
}

// advance adds a commit to the origin's branch and returns its revision.
func advance(t *testing.T, dir string) string {
	t.Helper()
	run := originGit(t, dir)
	run("commit", "--allow-empty", "-m", "next")
	return run("rev-parse", "HEAD")
}

func testConfig(t *testing.T, remote string) Config {
	t.Helper()
	root, err := canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Remote = remote
	cfg.Directory = filepath.Join(root, "checkout")
	cfg.StateDirectory = filepath.Join(root, "state")
	return cfg
}

// The branch head is already a local ref after open; fetching it as though it
// were a remote name made every issue assessment fail with exit status 128.
func TestIssueWorktreeUsesTheFetchedBranchHead(t *testing.T) {
	remote, want := originRepo(t)
	ctx := context.Background()
	base := checkout{config: testConfig(t, remote)}
	if err := base.open(ctx); err != nil {
		t.Fatal(err)
	}
	head, err := base.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if head != want {
		t.Fatalf("head %s, want %s", head, want)
	}
	w, err := base.itemWorktree(ctx, "issue-1", head)
	if err != nil {
		t.Fatalf("issue worktree: %v", err)
	}
	if got, err := w.git(ctx, "rev-parse", "HEAD"); err != nil || got != want {
		t.Fatalf("worktree at %s (%v), want %s", got, err, want)
	}
	if again, err := base.itemWorktree(ctx, "issue-1", head); err != nil || again.config.Directory != w.config.Directory {
		t.Fatalf("reusing an existing worktree failed: %v", err)
	}
}

func TestPullRequestWorktreeFetchesTheGitHubRef(t *testing.T) {
	remote, base0 := originRepo(t)
	ctx := context.Background()
	run := originGit(t, remote)
	run("commit", "--allow-empty", "-m", "pull")
	want := run("rev-parse", "HEAD")
	run("update-ref", "refs/pull/7/head", want)
	run("update-ref", "refs/heads/master", base0)

	b := checkout{config: testConfig(t, remote)}
	if err := b.open(ctx); err != nil {
		t.Fatal(err)
	}
	rev, err := b.fetchItem(ctx, "refs/pull/7/head", want)
	if err != nil {
		t.Fatalf("fetching the pull request head: %v", err)
	}
	w, err := b.itemWorktree(ctx, "pr-7", rev)
	if err != nil {
		t.Fatalf("pull request worktree: %v", err)
	}
	if got, err := w.git(ctx, "rev-parse", "HEAD"); err != nil || got != want {
		t.Fatalf("worktree at %s (%v), want %s", got, err, want)
	}
	if _, err := b.fetchItem(ctx, "refs/pull/7/head", base0); err == nil {
		t.Fatal("a moved revision was accepted")
	}
}

// Each of these left a worktree that `worktree add` or `worktree remove` would
// refuse, wedging every later run on the same item until someone cleaned
// up by hand.
func TestStaleItemWorktreesAreRebuilt(t *testing.T) {
	remote, first := originRepo(t)
	ctx := context.Background()
	base := checkout{config: testConfig(t, remote)}
	if err := base.open(ctx); err != nil {
		t.Fatal(err)
	}
	second := advance(t, remote)
	if _, err := base.git(ctx, "fetch", "--prune", "origin", "+refs/heads/master:refs/remotes/origin/master"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		stale func(*testing.T, string)
	}{
		{"untracked leftovers", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("notes"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"modified tracked source", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("edited"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory removed out of band", func(t *testing.T, dir string) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, err := base.itemWorktree(ctx, "issue-1", first)
			if err != nil {
				t.Fatal(err)
			}
			tc.stale(t, w.config.Directory)
			again, err := base.itemWorktree(ctx, "issue-1", second)
			if err != nil {
				t.Fatalf("stale worktree not recovered: %v", err)
			}
			if got, err := again.git(ctx, "rev-parse", "HEAD"); err != nil || got != second {
				t.Fatalf("worktree at %s (%v), want %s", got, err, second)
			}
			if status, err := again.git(ctx, "status", "--porcelain"); err != nil || status != "" {
				t.Fatalf("rebuilt worktree is not clean: %q (%v)", status, err)
			}
			if err := base.discard(ctx, again.config.Directory); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A worktree already at the right revision is reused, so a run that
// reruns unchanged does not pay for a fresh checkout.
func TestPristineItemWorktreeIsReused(t *testing.T) {
	remote, head := originRepo(t)
	ctx := context.Background()
	base := checkout{config: testConfig(t, remote)}
	if err := base.open(ctx); err != nil {
		t.Fatal(err)
	}
	w, err := base.itemWorktree(ctx, "issue-2", head)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(w.config.Directory, ".git")
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	again, err := base.itemWorktree(ctx, "issue-2", head)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if again.config.Directory != w.config.Directory || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a pristine worktree was rebuilt instead of reused")
	}
}

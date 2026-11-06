package town

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixtureCommitter = []string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.test"}

// isolateGitConfig keeps a developer's global and system git configuration,
// such as core.hooksPath or commit.gpgsign, out of the git fixtures.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func fixtureGit(ctx context.Context, dir string, args ...string) (string, error) {
	return git(ctx, dir, append(append([]string{}, fixtureCommitter...), args...)...)
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fixRepair is the part every well-behaved repair agent does: change the code
// and commit it on the provided branch.
func fixRepair(ctx context.Context, dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "code.txt"), []byte("fixed\n"), 0o600); err != nil {
		return err
	}
	_, err := fixtureGit(ctx, dir, "commit", "-am", "repair")
	return err
}

const repairReceipt = `TOWN_REPAIR {"summary":"Fixed the defect","checks":["Regression passed"]}`

// Each way an agent can hand back something other than one new commit on top
// of the reviewed head is refused before anything is saved or pushed.
func TestRepairGuardsRefuseEveryUnsafeResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		agent  func(ctx context.Context, tree sessionTree) (string, error)
		verify []string
		want   string
	}{
		{"empty_summary", func(ctx context.Context, tree sessionTree) (string, error) {
			return `TOWN_REPAIR {"summary":" ","checks":["x"]}`, fixRepair(ctx, tree.dir)
		}, nil, "summary and validation evidence"},
		{"no_checks", func(ctx context.Context, tree sessionTree) (string, error) {
			return `TOWN_REPAIR {"summary":"Fixed","checks":[]}`, fixRepair(ctx, tree.dir)
		}, nil, "summary and validation evidence"},
		{"dirty_tree", func(ctx context.Context, tree sessionTree) (string, error) {
			if err := fixRepair(ctx, tree.dir); err != nil {
				return "", err
			}
			return repairReceipt, os.WriteFile(filepath.Join(tree.dir, "scratch.txt"), []byte("left behind\n"), 0o600)
		}, nil, "left its branch or uncommitted changes"},
		{"left_branch", func(ctx context.Context, tree sessionTree) (string, error) {
			if err := fixRepair(ctx, tree.dir); err != nil {
				return "", err
			}
			_, err := git(ctx, tree.dir, "checkout", "-b", "elsewhere")
			return repairReceipt, err
		}, nil, "left its branch or uncommitted changes"},
		{"amended_history", func(ctx context.Context, tree sessionTree) (string, error) {
			if err := os.WriteFile(filepath.Join(tree.dir, "code.txt"), []byte("fixed\n"), 0o600); err != nil {
				return "", err
			}
			_, err := fixtureGit(ctx, tree.dir, "commit", "-a", "--amend", "-m", "rewritten")
			return repairReceipt, err
		}, nil, "rewrote history"},
		{"reset_to_base", func(ctx context.Context, tree sessionTree) (string, error) {
			if _, err := git(ctx, tree.dir, "reset", "--hard", "HEAD~1"); err != nil {
				return "", err
			}
			return repairReceipt, fixRepair(ctx, tree.dir)
		}, nil, "rewrote history"},
		{"no_commit", func(ctx context.Context, tree sessionTree) (string, error) {
			return repairReceipt, nil
		}, nil, "made no commit"},
		{"empty_commit", func(ctx context.Context, tree sessionTree) (string, error) {
			_, err := fixtureGit(ctx, tree.dir, "commit", "--allow-empty", "-m", "nothing")
			return repairReceipt, err
		}, nil, "made no code change"},
		{"revert_to_same_tree", func(ctx context.Context, tree sessionTree) (string, error) {
			if err := fixRepair(ctx, tree.dir); err != nil {
				return "", err
			}
			_, err := fixtureGit(ctx, tree.dir, "revert", "--no-edit", "HEAD")
			return repairReceipt, err
		}, nil, "made no code change"},
		{"verify_fails", func(ctx context.Context, tree sessionTree) (string, error) {
			return repairReceipt, fixRepair(ctx, tree.dir)
		}, []string{"sh", "-c", "echo broken >&2; exit 3"}, "broken"},
		{"verify_commits", func(ctx context.Context, tree sessionTree) (string, error) {
			return repairReceipt, fixRepair(ctx, tree.dir)
		}, append(append([]string{"git"}, fixtureCommitter...), "commit", "--allow-empty", "-m", "verify"), "verification changed repair HEAD"},
		{"verify_dirties", func(ctx context.Context, tree sessionTree) (string, error) {
			return repairReceipt, fixRepair(ctx, tree.dir)
		}, []string{"sh", "-c", "echo out > build.log"}, "left its branch or uncommitted changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, x, task, remote := fixtureWorkers(t)
			b.executeAgent = func(ctx context.Context, _ *Town, tree sessionTree, _ string, _ *slog.Logger, _ string) (string, error) {
				return tc.agent(ctx, tree)
			}
			x.Config.Verify = tc.verify
			err := b.repair(context.Background(), x, task, func(Progress) {}, quietLog())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("repair error = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "worktree preserved") {
				t.Fatalf("an unsaved repair preserved its worktree: %v", err)
			}
			state := b.Store.Snapshot().Towns[x.ID]
			if state.Intents[1] != nil {
				t.Fatalf("a refused repair saved an intent: %+v", state.Intents[1])
			}
			if got := state.Tasks[task.ID]; got.Stage != "fixes" || got.House != Issue || got.Cycles != 0 {
				t.Fatalf("a refused repair moved the task: %+v", got)
			}
			head, e := git(context.Background(), "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1")
			if e != nil || head != task.Head {
				t.Fatalf("a refused repair reached the remote: %s %v", head, e)
			}
			if dirs, branches := worktrees(t, b, x.ID); len(dirs) != 0 || len(branches) != 0 {
				t.Fatalf("refused repair leaked %v and %v", dirs, branches)
			}
		})
	}
}

// Once the intent is saved the push remote is checked again: an agent that
// pointed origin elsewhere cannot redirect the verified commit.
func TestRepairRefusesARedirectedPushRemote(t *testing.T) {
	b, x, task, remote := fixtureWorkers(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.git")
	b.executeAgent = func(ctx context.Context, _ *Town, tree sessionTree, _ string, _ *slog.Logger, _ string) (string, error) {
		if err := fixRepair(ctx, tree.dir); err != nil {
			return "", err
		}
		_, err := git(ctx, tree.dir, "remote", "set-url", "--push", "origin", elsewhere)
		return repairReceipt, err
	}
	err := b.repair(context.Background(), x, task, func(Progress) {}, quietLog())
	if err == nil || !strings.Contains(err.Error(), "repair push remote changed") || !strings.Contains(err.Error(), "worktree preserved") {
		t.Fatalf("redirected push = %v", err)
	}
	if i := b.Store.Snapshot().Towns[x.ID].Intents[1]; i == nil || i.Status != "uncertain" {
		t.Fatalf("saved repair lost: %+v", i)
	}
	if head, _ := git(context.Background(), "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1"); head != task.Head {
		t.Fatal("redirected repair reached the remote")
	}
}

// checkRepairRef is the authority on whether a push landed: a missing branch
// and a branch at another commit both leave the outcome unconfirmed.
func TestCheckRepairRefRequiresTheExactRemoteHead(t *testing.T) {
	b, x, task, _ := fixtureWorkers(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := b.checkRepairRef(ctx, x, dir, "issue-1", task.Head); err != nil {
		t.Fatalf("exact head refused: %v", err)
	}
	if err := b.checkRepairRef(ctx, x, dir, "issue-1", task.Base); err == nil || !strings.Contains(err.Error(), "at the exact head") || !strings.Contains(err.Error(), task.Head) {
		t.Fatalf("moved branch = %v", err)
	}
	if err := b.checkRepairRef(ctx, x, dir, "missing", task.Head); err == nil || !strings.Contains(err.Error(), "repair push not confirmed") {
		t.Fatalf("missing branch = %v", err)
	}
	b.remoteURL = func(string) string { return filepath.Join(dir, "absent.git") }
	if err := b.checkRepairRef(ctx, x, dir, "issue-1", task.Head); err == nil || !strings.Contains(err.Error(), "repair push not confirmed") {
		t.Fatalf("unreachable remote = %v", err)
	}
}

// rejectFirstPush installs a pre-receive hook that refuses exactly one push,
// the way a transient GitHub failure leaves a repair's outcome unknown.
func rejectFirstPush(t *testing.T, remote string) {
	t.Helper()
	isolateGitConfig(t)
	marker := filepath.Join(t.TempDir(), "rejected")
	hook := "#!/bin/sh\nif [ ! -e '" + marker + "' ]; then touch '" + marker + "'; echo 'transient failure' >&2; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
}

// failFirstRepairPush runs a repair whose push is rejected once, leaving a
// saved uncertain intent, and marks it for an operator retry.
func failFirstRepairPush(t *testing.T) (*BotWorkers, *Town, *Intent, string, *int) {
	t.Helper()
	b, x, task, remote := fixtureWorkers(t)
	b.confirmWait = 50 * time.Millisecond
	b.confirmInterval = time.Millisecond
	rejectFirstPush(t, remote)
	calls := 0
	b.executeAgent = func(ctx context.Context, _ *Town, tree sessionTree, _ string, _ *slog.Logger, _ string) (string, error) {
		calls++
		return repairReceipt, fixRepair(ctx, tree.dir)
	}
	err := b.repair(context.Background(), x, task, func(Progress) {}, quietLog())
	if err == nil || !strings.Contains(err.Error(), "transient failure") || !strings.Contains(err.Error(), "worktree preserved") {
		t.Fatalf("rejected push = %v", err)
	}
	intent := b.Store.Snapshot().Towns[x.ID].Intents[1]
	if intent == nil || intent.Status != "uncertain" || !SHA(intent.NewHead) {
		t.Fatalf("rejected push left no saved repair: %+v", intent)
	}
	if head, _ := git(context.Background(), "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1"); head != task.Head {
		t.Fatal("the rejected push moved the branch")
	}
	update(t, b.Store, func(st *State) { st.Towns[x.ID].Intents[1].Status = "retry" })
	return b, b.Store.Snapshot().Towns[x.ID], intent, remote, &calls
}

// An operator retry publishes the saved commit without rerunning the agent.
func TestResumeRepairPublishesTheSavedCommit(t *testing.T) {
	b, x, intent, remote, calls := failFirstRepairPush(t)
	if err := b.repair(context.Background(), x, x.Tasks["pr:1"], func(Progress) {}, quietLog()); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("retry reran the agent: %d calls", *calls)
	}
	head, err := git(context.Background(), "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1")
	if err != nil || head != intent.NewHead {
		t.Fatalf("remote is at %s, want the saved %s (%v)", head, intent.NewHead, err)
	}
	state := b.Store.Snapshot().Towns[x.ID]
	if state.Intents[1].Status != "confirmed" {
		t.Fatalf("resumed repair not confirmed: %+v", state.Intents[1])
	}
	out := state.Tasks["pr:1"]
	if out.Stage != "queued" || out.House != Review || out.Head != intent.NewHead || out.Cycles != 1 || out.Audit != nil {
		t.Fatalf("resumed repair did not hand back for review: %+v", out)
	}
}

// resumeRepair republishes only the exact saved, verified commit.
func TestResumeRepairRefusesAChangedSavedRepair(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, b *BotWorkers, x *Town, i *Intent, remote string)
		want   string
	}{
		{"new_commit", func(t *testing.T, _ *BotWorkers, _ *Town, i *Intent, _ string) {
			if _, err := fixtureGit(context.Background(), i.Directory, "commit", "--allow-empty", "-m", "later"); err != nil {
				t.Fatal(err)
			}
		}, "saved repair commit changed"},
		{"dirty", func(t *testing.T, _ *BotWorkers, _ *Town, i *Intent, _ string) {
			if err := os.WriteFile(filepath.Join(i.Directory, "code.txt"), []byte("edited\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "saved repair has uncommitted changes"},
		{"verify_fails", func(t *testing.T, b *BotWorkers, x *Town, _ *Intent, _ string) {
			x.Config.Verify = []string{"sh", "-c", "echo verify broke >&2; exit 2"}
		}, "verify broke"},
		{"verify_commits", func(t *testing.T, _ *BotWorkers, x *Town, _ *Intent, _ string) {
			x.Config.Verify = append(append([]string{"git"}, fixtureCommitter...), "commit", "--allow-empty", "-m", "verify")
		}, "verification changed saved repair"},
		{"verify_dirties", func(t *testing.T, _ *BotWorkers, x *Town, _ *Intent, _ string) {
			x.Config.Verify = []string{"sh", "-c", "echo out > build.log"}
		}, "verification left changes"},
		{"pull_moved", func(t *testing.T, _ *BotWorkers, _ *Town, _ *Intent, remote string) {
			ctx := context.Background()
			head, _ := git(ctx, "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1")
			tree, _ := git(ctx, "", "--git-dir", remote, "rev-parse", head+"^{tree}")
			other, err := fixtureGit(ctx, "", "--git-dir", remote, "commit-tree", tree, "-p", head, "-m", "author")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = git(ctx, "", "--git-dir", remote, "update-ref", "refs/heads/issue-1", other); err != nil {
				t.Fatal(err)
			}
		}, "saved repair no longer matches the PR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, x, intent, remote, calls := failFirstRepairPush(t)
			tc.change(t, b, x, intent, remote)
			before, _ := git(context.Background(), "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1")
			err := b.repair(context.Background(), x, x.Tasks["pr:1"], func(Progress) {}, quietLog())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("resume error = %v, want %q", err, tc.want)
			}
			if *calls != 1 {
				t.Fatal("a refused resume reran the agent")
			}
			if after, _ := git(context.Background(), "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1"); after != before {
				t.Fatal("a refused resume pushed")
			}
			if i := b.Store.Snapshot().Towns[x.ID].Intents[1]; i.Status == "confirmed" {
				t.Fatalf("a refused resume confirmed the repair: %+v", i)
			}
		})
	}
}

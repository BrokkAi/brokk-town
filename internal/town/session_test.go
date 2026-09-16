package town

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type gitFixtureGH struct {
	*fakeGH
	directory string
}

func (g gitFixtureGH) Pull(ctx context.Context, repo string, n int) (Pull, error) {
	p, _ := g.fakeGH.Pull(ctx, repo, n)
	head, e := git(ctx, "", "--git-dir", g.directory, "rev-parse", "refs/heads/"+p.Head.Ref)
	p.Head.SHA = head
	return p, e
}
func fixtureWorkers(t *testing.T) (*BotWorkers, *Town, *Task, string) {
	t.Helper()
	ctx := context.Background()
	s := testStore(t, false)
	x := addTown(t, s)
	source := filepath.Join(t.TempDir(), "source")
	remote := filepath.Join(t.TempDir(), "remote.git")
	run := func(dir string, args ...string) string {
		t.Helper()
		out, e := git(ctx, dir, args...)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	run("", "init", "--initial-branch=main", source)
	run(source, "config", "user.name", "Fixture")
	run(source, "config", "user.email", "fixture@example.test")
	os.WriteFile(filepath.Join(source, "code.txt"), []byte("base\n"), 0600)
	run(source, "add", "code.txt")
	run(source, "commit", "-m", "base")
	base := run(source, "rev-parse", "HEAD")
	run(source, "checkout", "-b", "issue-1")
	os.WriteFile(filepath.Join(source, "code.txt"), []byte("defect\n"), 0600)
	run(source, "commit", "-am", "introduce change")
	head := run(source, "rev-parse", "HEAD")
	run("", "clone", "--bare", source, remote)
	run("", "--git-dir", remote, "update-ref", "refs/pull/1/head", head)
	p := pull(1)
	p.Base.SHA = base
	p.Head.SHA = head
	gh := newGH(1)
	gh.p = p
	gh.snapshot = inventory(p)
	gh.snapshot.Head = base
	update(t, s, func(st *State) {
		x := st.Towns[x.ID]
		x.Owned[1] = Ownership{p.Head.Ref, 1}
		Reconcile(st, x, gh.snapshot, time.Now())
		task := x.Tasks["pr:1"]
		task.Stage = "fixes"
		task.House = Issue
		task.Audit = &Audit{Base: base, Head: head, Discussion: Digest([]Discussion{}), Description: description(p), Verdict: "changes_needed", Complete: true, Summary: "Found defect", Checks: []string{"reproduced"}, Findings: []Finding{{ID: "new:defect", State: "open", Detail: "code.txt needs repair"}}}
	})
	x = s.Snapshot().Towns[x.ID]
	workers := &BotWorkers{Root: t.TempDir(), Store: s, GitHub: gitFixtureGH{gh, remote}, remoteURL: func(string) string { return remote }}
	return workers, x, x.Tasks["pr:1"], remote
}
func TestRepairCommitsAndPushesOneExistingBranchThenHandsBack(t *testing.T) {
	b, x, task, remote := fixtureWorkers(t)
	calls := 0
	b.executeAgent = func(ctx context.Context, _ *Town, tree sessionTree, role string, _ *slog.Logger, prompt string) (string, error) {
		calls++
		if role != "issue" || !strings.Contains(prompt, "EXISTING pull request") {
			t.Fatal("wrong repair contract")
		}
		if e := os.WriteFile(filepath.Join(tree.dir, "code.txt"), []byte("fixed\n"), 0600); e != nil {
			return "", e
		}
		_, e := git(ctx, tree.dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-am", "repair")
		return `TOWN_REPAIR {"summary":"Fixed the defect","checks":["Regression passed"]}`, e
	}
	if e := b.repair(context.Background(), x, task, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil))); e != nil {
		t.Fatal(e)
	}
	state := b.Store.Snapshot().Towns[x.ID]
	out := state.Tasks[task.ID]
	if out.Stage != "queued" || out.House != Review || out.Audit != nil || out.Cycles != 1 || out.Head == task.Head {
		t.Fatalf("bad handoff %+v", out)
	}
	if state.Intents[1].Status != "confirmed" {
		t.Fatal("push not confirmed")
	}
	head, e := git(context.Background(), "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1")
	if e != nil || head != out.Head {
		t.Fatal("existing branch not advanced")
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	// Restart recovery sees the exact pushed commit and does not rerun the agent.
	update(t, b.Store, func(st *State) {
		t := st.Towns[x.ID]
		t.Intents[1].Status = "uncertain"
		t.Tasks[task.ID].Head = task.Head
		t.Tasks[task.ID].Stage = "fixes"
		t.Tasks[task.ID].House = Issue
		t.Tasks[task.ID].Cycles = 0
	})
	x = b.Store.Snapshot().Towns[x.ID]
	if e := b.repair(context.Background(), x, x.Tasks[task.ID], func(Progress) {}, slog.Default()); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || b.Store.Snapshot().Towns[x.ID].Tasks[task.ID].Cycles != 1 {
		t.Fatal("uncertain push duplicated repair")
	}
}

// laggingGH reports the pull request head GitHub showed before the push for a
// number of reads, the way the REST pull object trails a ref update.
type laggingGH struct {
	gitFixtureGH
	stale string
	lag   int
	reads int
}

func (g *laggingGH) Pull(ctx context.Context, repo string, n int) (Pull, error) {
	p, e := g.gitFixtureGH.Pull(ctx, repo, n)
	g.reads++
	if g.reads <= g.lag {
		p.Head.SHA = g.stale
	}
	return p, e
}
func repairAgent() func(context.Context, *Town, sessionTree, string, *slog.Logger, string) (string, error) {
	return func(ctx context.Context, _ *Town, tree sessionTree, _ string, _ *slog.Logger, _ string) (string, error) {
		if e := os.WriteFile(filepath.Join(tree.dir, "code.txt"), []byte("fixed\n"), 0600); e != nil {
			return "", e
		}
		_, e := git(ctx, tree.dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-am", "repair")
		return `TOWN_REPAIR {"summary":"Fixed the defect","checks":["Regression passed"]}`, e
	}
}
func TestRepairConfirmsFromRemoteRefWhenPullRequestHeadLags(t *testing.T) {
	for _, scenario := range []string{"catches_up", "never_catches_up"} {
		t.Run(scenario, func(t *testing.T) {
			b, x, task, remote := fixtureWorkers(t)
			lag := 3
			if scenario == "never_catches_up" {
				lag = 1 << 20
			}
			gh := &laggingGH{gitFixtureGH: b.GitHub.(gitFixtureGH), stale: task.Head, lag: lag}
			b.GitHub = gh
			b.confirmWait = 50 * time.Millisecond
			b.confirmInterval = time.Millisecond
			b.executeAgent = repairAgent()
			if e := b.repair(context.Background(), x, task, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil))); e != nil {
				t.Fatal(e)
			}
			state := b.Store.Snapshot().Towns[x.ID]
			out := state.Tasks[task.ID]
			if state.Intents[1].Status != "confirmed" || out.Stage != "queued" || out.House != Review || out.Cycles != 1 {
				t.Fatalf("lagging pull request head failed a landed push: %+v", out)
			}
			head, e := git(context.Background(), "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1")
			if e != nil || head != out.Head || head == task.Head {
				t.Fatal("existing branch not advanced")
			}
			if scenario == "catches_up" && gh.reads < lag+1 {
				t.Fatalf("confirmed before the pull request caught up after %d reads", gh.reads)
			}
		})
	}
}
func TestRepairRefusesConfirmationWhenBranchMovedPastPush(t *testing.T) {
	b, x, task, remote := fixtureWorkers(t)
	ctx := context.Background()
	b.confirmWait = 50 * time.Millisecond
	b.confirmInterval = time.Millisecond
	b.executeAgent = repairAgent()
	// Another writer advances the branch as soon as the repair push lands, so
	// the exact repair commit is no longer the branch head when the daemon
	// looks up the remote ref to confirm.
	raced := false
	b.remoteURL = func(string) string {
		head, e := git(ctx, "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1")
		if e == nil && head != task.Head && !raced {
			raced = true
			tree, _ := git(ctx, "", "--git-dir", remote, "rev-parse", head+"^{tree}")
			extra, e := git(ctx, "", "-c", "user.name=Other", "-c", "user.email=other@example.test", "--git-dir", remote, "commit-tree", tree, "-p", head, "-m", "someone else")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = git(ctx, "", "--git-dir", remote, "update-ref", "refs/heads/issue-1", extra); e != nil {
				t.Fatal(e)
			}
		}
		return remote
	}
	err := b.repair(ctx, x, task, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "not confirmed at the exact head") {
		t.Fatalf("moved branch confirmed as the repair: %v", err)
	}
	if !raced {
		t.Fatal("race never happened")
	}
	if b.Store.Snapshot().Towns[x.ID].Intents[1].Status != "uncertain" {
		t.Fatal("moved branch did not keep the intent uncertain")
	}
}

type advancingRepairGH struct {
	laggingGH
	remote string
	moved  bool
}

func (g *advancingRepairGH) Pull(ctx context.Context, repo string, n int) (Pull, error) {
	p, err := g.laggingGH.Pull(ctx, repo, n)
	if err != nil {
		return p, err
	}
	head, err := git(ctx, "", "--git-dir", g.remote, "rev-parse", "refs/heads/issue-1")
	if err != nil {
		return p, err
	}
	if head != g.stale && !g.moved {
		g.moved = true
		tree, err := git(ctx, "", "--git-dir", g.remote, "rev-parse", head+"^{tree}")
		if err != nil {
			return p, err
		}
		extra, err := git(ctx, "", "-c", "user.name=Other", "-c", "user.email=other@example.test", "--git-dir", g.remote, "commit-tree", tree, "-p", head, "-m", "concurrent push")
		if err != nil {
			return p, err
		}
		if _, err := git(ctx, "", "--git-dir", g.remote, "update-ref", "refs/heads/issue-1", extra); err != nil {
			return p, err
		}
		p.Head.SHA = extra
	}
	return p, nil
}

func TestRepairRefusesConcurrentPushDuringPullConfirmation(t *testing.T) {
	b, x, task, remote := fixtureWorkers(t)
	gh := &advancingRepairGH{laggingGH: laggingGH{gitFixtureGH: b.GitHub.(gitFixtureGH), stale: task.Head, lag: 1 << 20}, remote: remote}
	b.GitHub = gh
	b.confirmWait = 50 * time.Millisecond
	b.confirmInterval = time.Millisecond
	b.executeAgent = repairAgent()
	err := b.repair(context.Background(), x, task, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "not confirmed at the exact head") || !gh.moved {
		t.Fatalf("concurrent branch advance accepted: %v moved=%t", err, gh.moved)
	}
	if b.Store.Snapshot().Towns[x.ID].Intents[1].Status != "uncertain" {
		t.Fatal("concurrent push did not preserve uncertain repair intent")
	}
}

func TestCertifierAndOperatorVerifyCannotChangeReviewedRevision(t *testing.T) {
	for _, scenario := range []string{"valid", "agent_commit", "verify_commit", "omitted_evidence"} {
		t.Run(scenario, func(t *testing.T) {
			b, x, task, _ := fixtureWorkers(t)
			commit := []string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "changed HEAD"}
			b.executeAgent = func(ctx context.Context, _ *Town, tree sessionTree, _ string, _ *slog.Logger, _ string) (string, error) {
				if scenario == "agent_commit" {
					if _, e := git(ctx, tree.dir, commit...); e != nil {
						return "", e
					}
				}
				return `TOWN_REVIEW {"verdict":"clean","complete":true,"summary":"Full diff checked","checks":["Tests passed"],"findings":[]}`, nil
			}
			if scenario == "verify_commit" {
				x.Config.Verify = append([]string{"git"}, commit...)
			}
			known := map[string]string{}
			if scenario == "omitted_evidence" {
				known["old"] = "previous finding"
			}
			audit, e := b.certify(context.Background(), x, task, known, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if scenario == "valid" {
				if e != nil || !audit.Clean(task.Base, task.Head) {
					t.Fatal(audit, e)
				}
			} else if e == nil || audit != nil {
				t.Fatal("accepted altered or incomplete review", audit, e)
			}
		})
	}
}
func TestDiscussionChangeReroutesRepairWithoutAgent(t *testing.T) {
	b, x, task, _ := fixtureWorkers(t)
	g := b.GitHub.(gitFixtureGH)
	g.discussion = []Discussion{{ID: "comment:1", Body: "new concern"}}
	called := false
	b.executeAgent = func(context.Context, *Town, sessionTree, string, *slog.Logger, string) (string, error) {
		called = true
		return "", errors.New("unexpected agent")
	}
	if e := b.repair(context.Background(), x, task, func(Progress) {}, slog.Default()); e != nil {
		t.Fatal(e)
	}
	current := b.Store.Snapshot().Towns[x.ID].Tasks[task.ID]
	if called || current.Stage != "queued" || current.Audit != nil || current.House != Review {
		t.Fatal("stale discussion not rerouted")
	}
}

// worktrees reports the private worktree directories and town-repair branches a
// town still holds, across every extension repository.
func worktrees(t *testing.T, b *BotWorkers, id string) ([]string, []string) {
	t.Helper()
	base := filepath.Join(b.Root, "towns", Key(id), "extensions")
	roles, err := os.ReadDir(base)
	if err != nil {
		return nil, nil
	}
	dirs, branches := []string{}, []string{}
	for _, role := range roles {
		repository := filepath.Join(base, role.Name(), "repository.git")
		entries, e := os.ReadDir(filepath.Join(base, role.Name()))
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "work-") {
				dirs = append(dirs, filepath.Join(base, role.Name(), entry.Name()))
			}
		}
		listed, e := git(context.Background(), "", "--git-dir", repository, "branch", "--list", "town-repair-*", "--format", "%(refname:short)")
		if e != nil {
			t.Fatal(e)
		}
		for _, branch := range strings.Split(listed, "\n") {
			if strings.TrimSpace(branch) != "" {
				branches = append(branches, strings.TrimSpace(branch))
			}
		}
	}
	return dirs, branches
}

// Every repair failure before a durable intent leaves nothing worth keeping:
// the worktree and its town-repair branch must go, not accumulate forever.
func TestFailedRepairReleasesItsWorktreeAndBranch(t *testing.T) {
	for _, scenario := range []string{"bad_receipt", "no_commit"} {
		t.Run(scenario, func(t *testing.T) {
			b, x, task, _ := fixtureWorkers(t)
			b.executeAgent = func(ctx context.Context, _ *Town, tree sessionTree, _ string, _ *slog.Logger, _ string) (string, error) {
				if scenario == "no_commit" {
					return `TOWN_REPAIR {"summary":"Nothing to do","checks":["looked"]}`, nil
				}
				return "the agent forgot its receipt", nil
			}
			err := b.repair(context.Background(), x, task, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err == nil {
				t.Fatal("failed repair reported success")
			}
			if strings.Contains(err.Error(), "worktree preserved") {
				t.Fatalf("a repair with no saved commit preserved its worktree: %v", err)
			}
			if b.Store.Snapshot().Towns[x.ID].Intents[1] != nil {
				t.Fatal("no intent should exist for a repair that never pushed")
			}
			dirs, branches := worktrees(t, b, x.ID)
			if len(dirs) != 0 || len(branches) != 0 {
				t.Fatalf("failed repair leaked %v and %v", dirs, branches)
			}
		})
	}
}

// A repair whose push outcome is uncertain keeps its worktree, because the saved
// commit may still have to be published or verified. Once the intent is
// resolved, collection reclaims it.
func TestUncertainRepairKeepsItsWorktreeUntilTheIntentResolves(t *testing.T) {
	b, x, task, remote := fixtureWorkers(t)
	ctx := context.Background()
	b.confirmWait = 50 * time.Millisecond
	b.confirmInterval = time.Millisecond
	b.executeAgent = repairAgent()
	raced := false
	b.remoteURL = func(string) string {
		head, e := git(ctx, "", "--git-dir", remote, "rev-parse", "refs/heads/issue-1")
		if e == nil && head != task.Head && !raced {
			raced = true
			tree, _ := git(ctx, "", "--git-dir", remote, "rev-parse", head+"^{tree}")
			extra, e := git(ctx, "", "-c", "user.name=Other", "-c", "user.email=other@example.test", "--git-dir", remote, "commit-tree", tree, "-p", head, "-m", "someone else")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = git(ctx, "", "--git-dir", remote, "update-ref", "refs/heads/issue-1", extra); e != nil {
				t.Fatal(e)
			}
		}
		return remote
	}
	if err := b.repair(ctx, x, task, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("moved branch confirmed as the repair")
	}
	state := b.Store.Snapshot().Towns[x.ID]
	if state.Intents[1].Status != "uncertain" {
		t.Fatalf("saved repair is not uncertain: %+v", state.Intents[1])
	}
	dirs, branches := worktrees(t, b, x.ID)
	if len(dirs) != 1 || len(branches) != 1 {
		t.Fatalf("uncertain repair lost its saved work: %v %v", dirs, branches)
	}
	if state.Intents[1].Directory != dirs[0] {
		t.Fatalf("saved intent names %q, worktree is %q", state.Intents[1].Directory, dirs[0])
	}

	// Collection leaves the saved commit alone while the intent is unresolved.
	if err := b.CollectWorktrees(ctx, state); err != nil {
		t.Fatal(err)
	}
	if dirs, branches := worktrees(t, b, x.ID); len(dirs) != 1 || len(branches) != 1 {
		t.Fatalf("collection discarded an uncertain repair: %v %v", dirs, branches)
	}

	// Once the repair is confirmed, nothing needs the worktree.
	update(t, b.Store, func(st *State) { st.Towns[x.ID].Intents[1].Status = "confirmed" })
	if err := b.CollectWorktrees(ctx, b.Store.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if dirs, branches := worktrees(t, b, x.ID); len(dirs) != 0 || len(branches) != 0 {
		t.Fatalf("confirmed repair was never collected: %v %v", dirs, branches)
	}
}

// Collection is forced, so it must never reach a worktree another house is
// working in. The review house audits a PR in its own extension repository at
// the same time as the issue house collects finished repairs.
func TestCollectionLeavesAnotherHousesWorktreeAlone(t *testing.T) {
	b, x, _, _ := fixtureWorkers(t)
	ctx := context.Background()
	p, err := b.GitHub.Pull(ctx, x.Config.Repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := b.tree(ctx, x, p, "audit", false)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.close()
	if err := b.CollectWorktrees(ctx, x); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(audit.dir); err != nil {
		t.Fatalf("collection removed a live audit worktree: %v", err)
	}
	if _, err := git(ctx, audit.dir, "rev-parse", "HEAD"); err != nil {
		t.Fatalf("collection broke a live audit worktree: %v", err)
	}
}

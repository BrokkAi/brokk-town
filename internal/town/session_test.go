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

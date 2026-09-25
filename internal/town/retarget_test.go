package town

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// retarget moves a pull request to another branch without touching its head or
// base commit, which is exactly what GitHub does when someone changes the base.
func retarget(p Pull, branch string) Pull {
	p.Base.Ref = branch
	return p
}

func TestRetargetedPRIsNotMergedOutsideTheTownBranch(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	gh := newGH(1)
	// The operator changed the base to release; head and base commits, title,
	// body and discussion are all unchanged, so the saved audit still matches.
	gh.p = retarget(pull(1), "release")
	gh.gate.BaseRef = "release"
	sup := NewSupervisor(s, gh, nil)
	if _, err := sup.mergeReady(context.Background(), x, slog.Default()); err != nil {
		t.Fatal(err)
	}
	if gh.merged != 0 {
		t.Fatalf("merged %d times into a branch this town does not cover", gh.merged)
	}
	detail := s.Snapshot().Towns[x.ID].Tasks["pr:1"].Detail
	for _, want := range []string{"release", "main"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail %q does not name both branches", detail)
		}
	}
}

func TestMergeGateRequiresTheConfiguredBranch(t *testing.T) {
	audit := clean()
	gate := MergeGate{PolicyKnown: true, SquashAllowed: true, Base: baseSHA, BaseRef: "main", Head: headSHA, State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN"}
	if !gate.Allows(pull(1), audit, "main") {
		t.Fatal("a pull request on the configured branch was refused")
	}
	// Each observation of the base branch is checked on its own: one of them
	// lagging behind the other must not let the merge through.
	if gate.Allows(retarget(pull(1), "release"), audit, "main") {
		t.Fatal("the pull request's own base ref was not checked")
	}
	stale := gate
	stale.BaseRef = "release"
	if stale.Allows(pull(1), audit, "main") {
		t.Fatal("the merge gate's base ref was not checked")
	}
	if gate.Allows(pull(1), audit, "") {
		t.Fatal("an unknown town branch allowed a merge")
	}
	if gate.Allows(pull(1), audit, "release") {
		t.Fatal("a town covering another branch was allowed to merge")
	}
}

func TestReconcileRetiresAndRestoresARetargetedPR(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	if task := x.Tasks["pr:1"]; task.Audit == nil || task.Stage != "ready" {
		t.Fatalf("fixture is not a reviewed, ready PR: %+v", task)
	}

	// Retargeted away: the audit cannot survive, because it describes a merge
	// into a branch this town does not cover.
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], inventory(retarget(pull(1), "release")), time.Now())
	})
	task := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
	if task.Audit != nil {
		t.Fatal("a retargeted PR kept its audit")
	}
	if !task.Offbranch || !task.Blocked {
		t.Fatalf("a retargeted PR is still schedulable: %+v", task)
	}
	if !strings.Contains(task.Detail, "release") {
		t.Fatalf("detail does not name the new base: %q", task.Detail)
	}

	// Retargeted back: Town releases its own block and reviews again rather
	// than resuming from the audit it discarded.
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], inventory(pull(1)), time.Now())
	})
	task = s.Snapshot().Towns[x.ID].Tasks["pr:1"]
	if task.Offbranch || task.Blocked {
		t.Fatalf("the town did not release its own block: %+v", task)
	}
	if task.Audit != nil {
		t.Fatal("the discarded audit came back")
	}
	if task.Stage != "queued" || task.House != Review {
		t.Fatalf("a returning PR was not queued for review: %s/%s", task.House, task.Stage)
	}
}

func TestRetargetedPRIsNotDisturbedOnceMerged(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	update(t, s, func(st *State) {
		task := st.Towns[x.ID].Tasks["pr:1"]
		task.Stage, task.House = "merged", Release
	})
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], inventory(retarget(pull(1), "release")), time.Now())
	})
	task := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
	if task.Stage != "merged" || task.Blocked || task.Offbranch {
		t.Fatalf("a completed PR was reopened as off-branch: %+v", task)
	}
}

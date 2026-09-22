package town

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestUnknownDispatchSurvivesRestartAndRequiresTargetedRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	x := addTown(t, s)
	handle := WorkerRun{Bot: "issue-bot", Version: "0.5.3", Command: "fake", PID: 42, Socket: "/tmp/fake.sock", Issue: 7, Started: time.Now()}
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		town.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Issue, Stage: "queued"}
		w := town.Workers[Issue]
		w.Enabled, w.Status, w.Run = true, "working", &handle
	})
	workers := workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		t.Fatal("uncertain work was rerun")
		return RunResult{}, nil
	})
	s.Close()
	s, err = Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := s.Snapshot().Towns[x.ID].Workers[Issue]
	if w.Recovery == nil || w.Recovery.TaskID != "issue:7" || !strings.Contains(w.Task, "retry --task issue:7") {
		t.Fatalf("missing recovery: %+v", w)
	}
	var sup *Supervisor
	if eligible, _ := s.dispatchEligibility(x.ID, Issue, time.Now().Add(time.Hour)); eligible {
		t.Fatal("restart forgot unresolved outcome")
	}
	sup = NewSupervisor(s, nil, workers)
	if err := sup.Control(x.ID, Issue, "start", ""); err == nil {
		t.Fatal("generic start bypassed targeted recovery")
	}
	if err := sup.Control(x.ID, Issue, "retry", "issue:7"); err != nil {
		t.Fatal(err)
	}
	if w = s.Snapshot().Towns[x.ID].Workers[Issue]; w.Recovery != nil {
		t.Fatal("targeted retry did not clear hold")
	}
}

func TestIssueRecoveryRequiresAffirmativeSavedPublication(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	x.Workers[Issue].Recovery = &WorkerRecovery{TaskID: "issue:7", Detail: "uncertain"}
	x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Issue, Stage: "queued"}
	resolveIssueRecovery(x)
	if x.Workers[Issue].Recovery == nil {
		t.Fatal("absence treated as success")
	}
	x.Tasks["issue:7"].IssueJob = &IssueJob{Status: "pending"}
	resolveIssueRecovery(x)
	if x.Workers[Issue].Recovery == nil {
		t.Fatal("pending treated as success")
	}
	x.Tasks["issue:7"].IssueJob.Status = "submitted"
	resolveIssueRecovery(x)
	if x.Workers[Issue].Recovery != nil || x.Workers[Issue].Enabled {
		t.Fatal("publication not recovered or paused house enabled")
	}
}

func TestPersistedLiveHandleNeverAllowsReplacementDispatch(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		w := town.Workers[Bug]
		w.Enabled = true
		w.Run = &WorkerRun{Bot: "bug-bot", Version: "0.3.5", Command: "fake", PID: 42, Socket: "/tmp/fake.sock"}
	})
	if eligible, _ := s.dispatchEligibility(x.ID, Bug, time.Now()); eligible {
		t.Fatal("live handle allowed a duplicate scan")
	}
}

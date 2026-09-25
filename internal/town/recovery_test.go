package town

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestRepoInventoryResumesFromPersistedRecovery(t *testing.T) {
	for _, state := range []string{"legacy hold", "interrupted full", "interrupted inventory"} {
		for _, enabled := range []bool{true, false} {
			t.Run(state+map[bool]string{true: "/enabled", false: "/paused"}[enabled], func(t *testing.T) {
				dir := t.TempDir()
				s, err := Open(dir, false)
				if err != nil {
					t.Fatal(err)
				}
				x := addTown(t, s)
				started := time.Now().Add(-time.Hour).Round(0)
				update(t, s, func(st *State) {
					x := st.Towns[x.ID]
					x.Config.Branch = ""
					x.Error = "invalid branch"
					w := x.Workers[Repo]
					w.Enabled, w.Status = enabled, "waiting"
					w.Next = time.Now().Add(time.Hour)
					w.Logs = []Log{{At: started, Level: "ERROR", Text: "invalid branch"}}
					if state == "legacy hold" {
						w.Recovery = &WorkerRecovery{Started: started, Detail: "Interrupted repo scan; automatic replacement work is held"}
					} else {
						mode := "full"
						if state == "interrupted inventory" {
							mode = "inventory"
						}
						w.Run = &WorkerRun{Bot: "repo-bot", Version: "0.1.2", Command: "fake", PID: 42, Socket: "/tmp/fake.sock", Mode: mode, Started: started}
					}
				})
				s.Close()
				s, err = Open(dir, false)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				wantRecovery := state != "interrupted inventory"
				w := s.Snapshot().Towns[x.ID].Workers[Repo]
				if (w.Recovery != nil) != wantRecovery || w.Run != nil {
					t.Fatalf("reopened worker: %+v", w)
				}
				if eligible, _ := s.dispatchEligibility(x.ID, Repo, time.Now()); eligible != enabled {
					t.Fatalf("inventory eligibility = %v, enabled = %v", eligible, enabled)
				}
				gh := newGH(1)
				observer := &healthObserver{gh: gh}
				sup := NewSupervisor(s, gh, observer)
				if !enabled {
					sup.schedule(t.Context())
					sup.wg.Wait()
					if len(observer.seen) != 0 {
						t.Fatal("recovery started a paused repo house")
					}
					return
				}
				sup.schedule(t.Context())
				sup.wg.Wait()
				after := s.Snapshot().Towns[x.ID]
				if !after.Initialized || after.Error != "" || after.Head != baseSHA {
					t.Fatalf("saved startup failure was not reconciled: initialized=%v error=%q head=%q", after.Initialized, after.Error, after.Head)
				}
				if len(observer.seen) != 1 || (wantRecovery && observer.seen[0].Health) {
					t.Fatalf("recovery requests: %+v", observer.seen)
				}
				w = after.Workers[Repo]
				if (w.Recovery != nil) != wantRecovery || len(w.Logs) != 1 || w.Logs[0].Text != "invalid branch" {
					t.Fatal("inventory discarded recovery or historical failure evidence")
				}
				if wantRecovery && (!w.Recovery.Started.Equal(started) || w.Status != "failed" || !strings.Contains(w.Task, "repairs are held")) {
					t.Fatalf("repair uncertainty is not visible: %+v", w)
				}
				// A second inventory and another restart must not erase the hold.
				if err := sup.reconcileNow(t.Context(), after); err != nil {
					t.Fatal(err)
				}
				s.Close()
				reopened, err := Open(dir, false)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if got := reopened.Snapshot().Towns[x.ID].Workers[Repo]; (got.Recovery != nil) != wantRecovery {
					t.Fatalf("second restart lost recovery: %+v", got)
				}
			})
		}
	}
}

// A merge-confirmation inventory may start after a repair releases the
// reconciliation gate but before its scheduled worker finishes cleanup.
type overlappingRepoObserver struct {
	workerFunc
	store       *Store
	gh          *fakeGH
	run         WorkerRun
	fullEntered chan struct{}
	fullRelease chan struct{}
	readEntered chan *WorkerRecovery
	readRelease chan struct{}
}

func (o *overlappingRepoObserver) Observe(ctx context.Context, x *Town, request InventoryRequest, p func(Progress), log *slog.Logger) (RunResult, error) {
	run := o.run
	if !request.Health {
		run.Mode = "inventory"
		run.Started = run.Started.Add(time.Minute)
	}
	if err := o.store.Update(func(st *State) error {
		st.Towns[x.ID].Workers[Repo].Run = &run
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	if request.Health {
		close(o.fullEntered)
		<-o.fullRelease
		return RunResult{}, &WorkerInterruptedError{errors.New("worker stream disconnected after possible push")}
	}
	o.readEntered <- x.Workers[Repo].Recovery
	<-o.readRelease
	return o.gh.Observe(ctx, x, request, p, log)
}

func TestInterruptedRepairRecoveryPrecedesConfirmationInventory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := testStore(t, false)
		x := addTown(t, s)
		update(t, s, func(st *State) {
			st.Towns[x.ID].Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Hall, Stage: "declined", MayoralDecision: "declined"}
		})
		gh := newGH(1)
		gh.snapshot.Issues = []RemoteIssue{{Number: 7, State: "open", Title: "Declined issue"}}
		run := WorkerRun{Bot: "repo-bot", Version: "0.1.2", Command: "fake", PID: 42, Socket: "/tmp/fake.sock", Mode: "full", Started: time.Now()}
		o := &overlappingRepoObserver{store: s, gh: gh, run: run, fullEntered: make(chan struct{}), fullRelease: make(chan struct{}), readEntered: make(chan *WorkerRecovery), readRelease: make(chan struct{})}
		sup := NewSupervisor(s, gh, o)
		fullDone := make(chan struct{})
		go func() { defer close(fullDone); sup.execute(t.Context(), x, Repo) }()
		select {
		case <-o.fullEntered:
		case <-fullDone:
			t.Fatalf("repair dispatch never started: %+v", s.Snapshot().Towns[x.ID].Workers[Repo])
		}
		readDone := make(chan error, 1)
		go func() { readDone <- sup.reconcile(t.Context(), x, false, func(Progress) {}, slog.Default()) }()
		// Wait until the confirmation is blocked on the reconciliation gate.
		// The scheduler mutex then holds the interrupted worker at releaseRepair
		// while the confirmation starts, without relying on sleep timing.
		synctest.Wait()
		sup.mu.Lock()
		close(o.fullRelease)
		recovery := <-o.readEntered
		sup.mu.Unlock()
		<-fullDone
		if recovery == nil || !recovery.Started.Equal(run.Started) {
			t.Errorf("confirmation did not receive the interrupted repair identity: %+v", recovery)
		}
		w := s.Snapshot().Towns[x.ID].Workers[Repo]
		if w.Run == nil || w.Run.Mode != "inventory" || !w.Run.Started.Equal(run.Started.Add(time.Minute)) {
			t.Errorf("scheduled cleanup erased the active confirmation dispatch: %+v", w.Run)
		}
		if sup.claimRepair(x.ID) {
			t.Error("interrupted repair no longer holds replacement repairs")
			sup.releaseRepair(x.ID)
		}
		close(o.readRelease)
		if err := <-readDone; err != nil {
			t.Fatal(err)
		}
		if len(gh.comments) != 0 || len(gh.closed) != 0 || len(gh.closedPulls) != 0 || len(gh.filed) != 0 {
			t.Error("confirmation inventory authorized follow-up GitHub writes")
		}
		w = s.Snapshot().Towns[x.ID].Workers[Repo]
		if w.Run != nil || w.Recovery == nil || !w.Recovery.Started.Equal(run.Started) {
			t.Errorf("confirmation did not retire its own dispatch while preserving recovery: %+v", w)
		}
		s.Close()
		reopened, err := Open(filepath.Dir(s.path), false)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if got := reopened.Snapshot().Towns[x.ID].Workers[Repo].Recovery; got == nil || !got.Started.Equal(run.Started) {
			t.Fatalf("restart lost the interrupted repair identity: %+v", got)
		}
	})
}

type interruptedRepoObserver struct {
	workerFunc
	store *Store
	mode  string
}

func (o interruptedRepoObserver) Observe(_ context.Context, x *Town, _ InventoryRequest, _ func(Progress), _ *slog.Logger) (RunResult, error) {
	err := o.store.Update(func(st *State) error {
		st.Towns[x.ID].Workers[Repo].Run = &WorkerRun{Bot: "repo-bot", Version: "0.1.2", Command: "fake", PID: 42, Socket: "/tmp/fake.sock", Mode: o.mode, Started: time.Now()}
		return nil
	})
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{}, &WorkerInterruptedError{context.Canceled}
}

func TestInterruptedInventoryDoesNotCreateOrReplaceRepairRecovery(t *testing.T) {
	for _, mode := range []string{"inventory", "full"} {
		for _, existing := range []bool{false, true} {
			t.Run(mode+map[bool]string{true: "/held", false: "/unheld"}[existing], func(t *testing.T) {
				s := testStore(t, false)
				x := addTown(t, s)
				started := time.Now().Add(-time.Hour).Round(0)
				if existing {
					update(t, s, func(st *State) {
						st.Towns[x.ID].Workers[Repo].Recovery = &WorkerRecovery{Started: started, Detail: recoveryDetail(Repo, "")}
					})
				}
				sup := NewSupervisor(s, nil, interruptedRepoObserver{store: s, mode: mode})
				sup.execute(t.Context(), x, Repo)
				w := s.Snapshot().Towns[x.ID].Workers[Repo]
				if (w.Recovery != nil) != (existing || mode == "full") || w.Run != nil {
					t.Fatalf("interrupted worker: %+v", w)
				}
				if existing && !w.Recovery.Started.Equal(started) {
					t.Fatal("an interrupted read replaced the uncertain repair identity")
				}
				if w.Recovery != nil && w.Status != "failed" {
					t.Fatal("repair recovery is disguised as a scheduled wait")
				}
				if eligible, _ := s.dispatchEligibility(x.ID, Repo, time.Now().Add(time.Hour)); !eligible {
					t.Fatal("interrupted read prevented subsequent inventory")
				}
				select {
				case err := <-sup.fatal:
					t.Fatal(err)
				default:
				}
			})
		}
	}
}

func TestRepoRecoveryDoesNotAuthorizeReconcileWrites(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		x := st.Towns[x.ID]
		x.Workers[Repo].Recovery = &WorkerRecovery{Detail: recoveryDetail(Repo, "")}
		x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Hall, Stage: "declined", MayoralDecision: "declined", Detail: "declined"}
	})
	gh := newGH(1)
	gh.snapshot.Issues = []RemoteIssue{{Number: 7, State: "open", Title: "Declined issue"}}
	observer := &healthObserver{gh: gh}
	sup := NewSupervisor(s, gh, observer)
	if sup.claimRepair(x.ID) {
		t.Fatal("recovery authorized another repair")
	}
	if err := sup.reconcileNow(t.Context(), s.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.comments) != 0 || len(gh.closed) != 0 || len(gh.closedPulls) != 0 || len(gh.filed) != 0 {
		t.Fatal("recovery inventory made GitHub writes")
	}
	if err := sup.Control(x.ID, Repo, "start", ""); err != nil {
		t.Fatal(err)
	}
	if !sup.claimRepair(x.ID) {
		t.Fatal("explicit repair authorization did not release the hold")
	}
	sup.releaseRepair(x.ID)
	if err := sup.reconcileNow(t.Context(), s.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.closed) != 1 || gh.closed[0] != 7 {
		t.Fatalf("authorized reconciliation did not close the pending decline: %v", gh.closed)
	}
}

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

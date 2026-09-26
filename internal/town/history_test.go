package town

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func historyFixture(t *testing.T, n int) (*Store, string, time.Time) {
	t.Helper()
	s := testStore(t, false)
	x := addTown(t, s)
	now := time.Now().UTC()
	update(t, s, func(st *State) {
		x := st.Towns[x.ID]
		for _, w := range x.Workers {
			w.Enabled = false
		}
		for i := 1; i <= n; i++ {
			id := fmt.Sprintf("issue:%d", i)
			x.Tasks[id] = &Task{ID: id, Kind: "issue", Number: i, Title: "Historical title " + id, Stage: "closed", House: Issue, Updated: now.Add(-40 * 24 * time.Hour)}
		}
	})
	return s, x.ID, now
}
func TestHistoryArchivalLeavesHotStateAndSurvivesRestart(t *testing.T) {
	s, id, now := historyFixture(t, 3)
	if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	state := s.Snapshot()
	x := state.Towns[id]
	if len(x.Tasks) != 0 || x.ArchivedTasks != 3 || !x.HistoryCreated || state.Format != 2 {
		t.Fatal(x.ArchivedTasks, x.HistoryCreated, state.Format, len(x.Tasks))
	}
	raw, _ := json.Marshal(state.Public())
	if strings.Contains(string(raw), "Historical title") {
		t.Fatal("cold task remained in public snapshot")
	}
	page, err := s.History(context.Background(), strings.ToUpper(id), "", 2)
	if err != nil || page.Total != 3 || len(page.Items) != 2 || page.Next == "" {
		t.Fatal(page, err)
	}
	next, err := s.History(context.Background(), id, page.Next, 2)
	if err != nil || len(next.Items) != 1 || next.Next != "" || next.Items[0].ID == page.Items[0].ID {
		t.Fatal(next, err)
	}
	for _, e := range s.history[id] {
		info, err := os.Stat(filepath.Join(historyRoot(filepath.Dir(s.path), id), "objects", e.filename()))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(info, err)
		}
	}
	root := filepath.Dir(s.path)
	s.Close()
	again, err := Open(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	task, err := again.HistoryTask(context.Background(), id, "issue:1")
	if err != nil || task.Title != "Historical title issue:1" {
		t.Fatal(task, err)
	}
	if len(again.Snapshot().Towns[id].Tasks) != 0 {
		t.Fatal("restart hydrated all history")
	}
}
func TestHistoryRestoresReopenedDecisionAndIgnoresRepeatedClosedInventory(t *testing.T) {
	s, id, now := historyFixture(t, 1)
	update(t, s, func(st *State) {
		x := st.Towns[id]
		delete(x.Tasks, "issue:1")
		x.Tasks["pr:1"] = &Task{ID: "pr:1", Kind: "pr", Number: 1, Title: "Declined PR", Stage: "closed", House: Hall, External: true, MayoralDecision: "declined", Updated: now.Add(-40 * 24 * time.Hour)}
	})
	if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	p := pull(1)
	p.State = "closed"
	closed := RepoSnapshot{Branch: "main", Head: baseSHA, Pulls: []Pull{p}}
	restored, err := s.PrepareHistory(context.Background(), id, &closed)
	if err != nil || len(restored) != 0 || len(closed.Pulls) != 0 {
		t.Fatal(restored, closed, err)
	}
	update(t, s, func(st *State) { Reconcile(st, st.Towns[id], closed, now) })
	if len(s.Snapshot().Towns[id].Tasks) != 0 {
		t.Fatal("resync rematerialized cold history")
	}
	p.State = "open"
	opened := RepoSnapshot{Branch: "main", Head: baseSHA, Pulls: []Pull{p}}
	restored, err = s.PrepareHistory(context.Background(), id, &opened)
	if err != nil {
		t.Fatal(err)
	}
	update(t, s, func(st *State) {
		x := st.Towns[id]
		if err := restoreHistory(x, restored); err != nil {
			t.Fatal(err)
		}
		Reconcile(st, x, opened, now)
	})
	x := s.Snapshot().Towns[id]
	task := x.Tasks["pr:1"]
	if x.ArchivedTasks != 0 || task == nil || task.MayoralDecision != "declined" || task.Stage != "declined" {
		t.Fatal(x.ArchivedTasks, task)
	}
	page, err := s.History(context.Background(), id, "", 50)
	if err != nil || page.Total != 0 {
		t.Fatal(page, err)
	}
}
func TestHistoryRetainsUncertainActiveRecentAndPendingWork(t *testing.T) {
	for _, scenario := range []string{"uncertain", "recovery", "active", "recent", "followup", "branch", "claim", "dependency"} {
		t.Run(scenario, func(t *testing.T) {
			s, id, now := historyFixture(t, 1)
			update(t, s, func(st *State) {
				x := st.Towns[id]
				task := x.Tasks["issue:1"]
				switch scenario {
				case "uncertain":
					x.Intents[1] = &Intent{PR: 1, Kind: "merge", Base: baseSHA, Head: headSHA, Status: "uncertain"}
				case "recovery":
					x.Workers[Issue].Recovery = &WorkerRecovery{TaskID: task.ID, Detail: "Interrupted"}
				case "active":
					x.Workers[Issue].Run = &WorkerRun{Bot: "issue-bot", Version: "1.0.0", Command: "fixture", PID: 123, Socket: "/tmp/fixture.sock", Issue: 1, Started: now, Deadline: now.Add(time.Hour)}
				case "recent":
					task.Updated = now.Add(-time.Hour)
				case "followup":
					task.FollowUps = []Finding{{ID: "pending", Detail: "follow up"}}
				case "branch":
					task.BranchKept = true
				case "claim":
					task.IssueJob = &IssueJob{ClaimPending: true}
				case "dependency":
					x.Owned[2] = Ownership{Issue: 1, Branch: "own"}
					x.Tasks["pr:2"] = &Task{ID: "pr:2", Kind: "pr", Number: 2, House: Review, Stage: "queued", Updated: now}
				}
			})
			if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
				t.Fatal(err)
			}
			if s.Snapshot().Towns[id].Tasks["issue:1"] == nil {
				t.Fatal("unsafe task archived")
			}
		})
	}
}
func TestHistoryFailedHotCommitKeepsBothCopiesAndReopensSafely(t *testing.T) {
	s, id, now := historyFixture(t, 1)
	root := filepath.Dir(s.path)
	original := s.path
	// Keep the archive root fixed but make the hot snapshot rename fail.
	s.path = filepath.Join(root, "cannot-replace-directory")
	if err := os.Mkdir(s.path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveCompleted(context.Background(), id, now); err == nil {
		t.Fatal("expected hot-state persistence failure")
	}
	if s.Snapshot().Towns[id].Tasks["issue:1"] == nil {
		t.Fatal("failed commit lost hot task")
	}
	s.path = original
	s.Close()
	again, err := Open(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Snapshot().Towns[id].Tasks["issue:1"] == nil {
		t.Fatal("crash prefix lost identity")
	}
	if err := again.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	if again.Snapshot().Towns[id].ArchivedTasks != 1 {
		t.Fatal("retry did not finish archival")
	}
}
func TestHistoryCorruptionAndMissingIndexFailClosed(t *testing.T) {
	s, id, now := historyFixture(t, 1)
	if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	e := s.history[id]["issue:1"]
	root := filepath.Dir(s.path)
	path := filepath.Join(historyRoot(root, id), "objects", e.filename())
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HistoryTask(context.Background(), id, e.ID); err == nil {
		t.Fatal("corrupt evidence accepted")
	}
	remote := RepoSnapshot{Issues: []RemoteIssue{{Number: 1, State: "open"}}}
	if _, err := s.PrepareHistory(context.Background(), id, &remote); err == nil {
		t.Fatal("corrupt task reopened")
	}
	s.Close()
	if err := os.Remove(filepath.Join(historyRoot(root, id), "index.json")); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(root, false); err == nil {
		opened.Close()
		t.Fatal("missing identity index accepted")
	}
}
func TestHistoryDemoCancellationAndSymlinksPreserveTasks(t *testing.T) {
	s, id, now := historyFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.ArchiveCompleted(ctx, id, now); err == nil {
		t.Fatal("canceled archival accepted")
	}
	if len(s.Snapshot().Towns[id].Tasks) != 1 {
		t.Fatal("cancellation lost task")
	}
	base := historyRoot(filepath.Dir(s.path), id)
	os.RemoveAll(base)
	if err := os.Symlink(t.TempDir(), base); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveCompleted(context.Background(), id, now); err == nil {
		t.Fatal("followed history symlink")
	}
	demo := testStore(t, true)
	x := addTown(t, demo)
	update(t, demo, func(st *State) {
		st.Towns[x.ID].Tasks["issue:1"] = &Task{ID: "issue:1", Kind: "issue", Number: 1, House: Issue, Stage: "closed", Updated: now.Add(-time.Hour * 1000)}
	})
	if err := demo.ArchiveCompleted(context.Background(), x.ID, now); err != nil {
		t.Fatal(err)
	}
	if demo.Snapshot().Towns[x.ID].Tasks["issue:1"] == nil {
		t.Fatal("demo archived tasks")
	}
}
func TestHistoryTranscriptCleanupUsesArchivedTerminalIdentity(t *testing.T) {
	sup, id, root := storageFixture(t)
	path := savedTranscript(t, sup, id, root, "archived-task", true)
	now := time.Now()
	update(t, sup.Store, func(st *State) { st.Towns[id].Tasks["issue:1"].Updated = now.Add(-40 * 24 * time.Hour) })
	if err := sup.Store.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	a := findArtifact(t, sup, id, "archived-task.jsonl", 0)
	if !a.Eligible {
		t.Fatal(a)
	}
	result, err := sup.CleanupStorage(context.Background(), id, 0, []string{a.ID})
	if err != nil || result[0].Status != "removed" {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := sup.Store.HistoryTask(context.Background(), id, "issue:1"); err != nil {
		t.Fatal("cleanup lost historical identity", err)
	}
}

func TestHistorySupervisorRestoresBeforeRevisionChecks(t *testing.T) {
	s, id, now := historyFixture(t, 0)
	update(t, s, func(st *State) {
		x := st.Towns[id]
		x.Initialized = true
		x.Tasks["pr:1"] = &Task{ID: "pr:1", Kind: "pr", Number: 1, Title: "Closed", Stage: "closed", House: Hall, External: true, MayoralDecision: "declined", Head: headSHA, Base: baseSHA, Updated: now.Add(-40 * 24 * time.Hour)}
	})
	if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	gh := newGH(1)
	sup := NewSupervisor(s, gh, observing{gh: gh})
	if err := sup.reconcileNow(context.Background(), s.Snapshot().Towns[id]); err != nil {
		t.Fatal(err)
	}
	x := s.Snapshot().Towns[id]
	task := x.Tasks["pr:1"]
	if task == nil || task.Stage != "declined" || task.MayoralDecision != "declined" || x.ArchivedTasks != 0 {
		t.Fatal(task, x.ArchivedTasks)
	}
	for _, event := range s.Snapshot().Events {
		if event.Cargo == "pr:1" && strings.Contains(event.Title, "arrived") {
			t.Fatal("rehydration replayed arrival", event)
		}
	}
}

func TestHistoryBaselineAgeAndRecentClosure(t *testing.T) {
	s, id, now := historyFixture(t, 0)
	old := now.Add(-40 * 24 * time.Hour)
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[id], RepoSnapshot{Branch: "main", Head: baseSHA, Issues: []RemoteIssue{{Number: 1, State: "closed", Updated: old}, {Number: 2, State: "open", Updated: old}}}, now)
	})
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[id], RepoSnapshot{Branch: "main", Head: baseSHA, Issues: []RemoteIssue{{Number: 1, State: "closed", Updated: old}, {Number: 2, State: "closed", Updated: old}}}, now)
	})
	if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	x := s.Snapshot().Towns[id]
	if x.Tasks["issue:1"] != nil || x.Tasks["issue:2"] == nil || !x.Tasks["issue:2"].Updated.Equal(now) {
		t.Fatal(x.Tasks)
	}
}

func TestHistoryBoundsEachBatchAndRetainsDispatchWithoutHandle(t *testing.T) {
	s, id, now := historyFixture(t, maxHistoryBatch+1)
	update(t, s, func(st *State) { st.Towns[id].Workers[Release].Status = "working" })
	if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Towns[id].Tasks) != maxHistoryBatch+1 {
		t.Fatal("archived during dispatch")
	}
	update(t, s, func(st *State) { st.Towns[id].Workers[Release].Status = "idle" })
	if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	if x := s.Snapshot().Towns[id]; len(x.Tasks) != 1 || x.ArchivedTasks != maxHistoryBatch {
		t.Fatal(len(x.Tasks), x.ArchivedTasks)
	}
}

func TestHistoryNewFunnelRestoresExistingIssueIdentity(t *testing.T) {
	s, id, now := historyFixture(t, 1)
	update(t, s, func(st *State) { st.Towns[id].Tasks["issue:1"].Attempts = 2 })
	if err := s.ArchiveCompleted(context.Background(), id, now); err != nil {
		t.Fatal(err)
	}
	config := FunnelConfig{ID: "new", Provider: "github", Location: SourceLocation{"repository": id}, PriorityPolicy: "source-status", Enabled: true}
	identity := WorkIdentity{Funnel: "new", Provider: "github", Item: "1"}
	item := WorkItem{Identity: identity, Title: "Reopened via funnel", Status: WorkQueued, Eligible: true, Priority: Priority{Policy: "source-status"}, Provenance: Provenance{Identity: identity, Revision: "r1", ObservedAt: now, ExternalState: "open"}}
	page := DiscoveryPage{Funnel: "new", Provider: "github", Items: []WorkItem{item}, Complete: true, Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, ObservedAt: now}
	update(t, s, func(st *State) { st.Towns[id].Config.Funnels = FunnelConfigs{config} })
	restored, err := s.prepareFunnelHistory(context.Background(), s.Snapshot().Towns[id], config, page)
	if err != nil {
		t.Fatal(err)
	}
	update(t, s, func(st *State) {
		x := st.Towns[id]
		if err := restoreHistory(x, restored); err != nil {
			t.Fatal(err)
		}
		if err := ReconcileFunnelPage(st, x, page, now); err != nil {
			t.Fatal(err)
		}
	})
	x := s.Snapshot().Towns[id]
	if x.ArchivedTasks != 0 || x.Tasks["issue:1"].Attempts != 2 || x.Tasks["issue:1"].Source == nil {
		t.Fatal(x.Tasks, x.ArchivedTasks)
	}
	x.Tasks["issue:1"].Stage = "closed"
	x.Tasks["issue:1"].Updated = now.Add(-40 * 24 * time.Hour)
	if historyCandidate(x, x.Tasks["issue:1"], now, nil) {
		t.Fatal("source cursor identity archived")
	}
}

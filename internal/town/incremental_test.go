package town

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInventoryCursorFullBaselineDailyResyncAndBranchChanges(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	now := time.Now().UTC()
	if inventoryCursor(x, now) != nil {
		t.Fatal("uninitialized delta")
	}
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], RepoSnapshot{Branch: "main", Head: baseSHA, StartedAt: now.Add(-time.Minute)}, now)
	})
	x = s.Snapshot().Towns[x.ID]
	if cursor := inventoryCursor(x, now); cursor == nil || !cursor.Equal(now.Add(-time.Minute)) {
		t.Fatal(cursor)
	}
	if inventoryCursor(x, now.Add(25*time.Hour)) != nil || inventoryCursor(x, now.Add(-time.Hour)) != nil {
		t.Fatal("bad clock accepted")
	}
	x.Config.Branch = "other"
	if inventoryCursor(x, now) != nil {
		t.Fatal("branch change retained cursor")
	}
	s.Close()
	again, err := Open(filepath.Dir(s.path), false)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if inventoryCursor(again.Snapshot().Towns[x.ID], now) == nil {
		t.Fatal("restart lost completed baseline")
	}
}
func TestIncrementalAbsencePreservesClosingClaimAndFullCursor(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	now := time.Now().UTC()
	update(t, s, func(st *State) {
		x := st.Towns[x.ID]
		Reconcile(st, x, RepoSnapshot{Branch: "main", Head: baseSHA}, now.Add(-time.Hour))
		x.Tasks["issue:1"] = &Task{ID: "issue:1", Kind: "issue", Number: 1, House: Hall, Stage: "closing"}
	})
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], RepoSnapshot{Branch: "main", Head: baseSHA, Incremental: true, StartedAt: now.Add(-time.Minute)}, now)
	})
	x = s.Snapshot().Towns[x.ID]
	if x.Tasks["issue:1"].Stage != "closing" || !x.LastFullSync.Equal(now.Add(-time.Hour)) || !x.LastSync.Equal(now.Add(-time.Minute)) {
		t.Fatal(x.Tasks["issue:1"], x.LastSync, x.LastFullSync)
	}
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], RepoSnapshot{Branch: "main", Head: baseSHA}, now.Add(time.Minute))
	})
	if s.Snapshot().Towns[x.ID].Tasks["issue:1"].Stage != "declined" {
		t.Fatal("full scan no longer resolves absence")
	}
}
func TestWorkerNegotiatesIncrementalFieldsWithoutBreakingOlderAPI(t *testing.T) {
	for _, supports := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "incremental"}[supports], func(t *testing.T) {
			capture := filepath.Join(t.TempDir(), "request.json")
			t.Setenv("TOWN_WORKER_TEST_CAPTURE", capture)
			body := pythonFakeWorker
			if supports {
				body = strings.Replace(body, "capabilities.append('parent-socket')", "capabilities.append('parent-socket')\n        capabilities.append('incremental-inventory')", 1)
			}
			path := filepath.Join(t.TempDir(), "worker")
			writeFakeWorker(t, path, body)
			b := &BotWorkers{botCommands: map[Role]string{Issue: path}}
			bot, err := b.externalBot(context.Background(), Issue)
			if err != nil {
				t.Fatal(err)
			}
			p, err := startWorkerProcess(context.Background(), bot)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			since := time.Now().Add(-time.Hour)
			_, err = p.run(context.Background(), workerRequest{Protocol: 1, InventorySince: &since, InventoryBranch: "main"}, false, time.Now().Add(time.Minute), func(Progress) {}, nil)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var request map[string]any
			if err = json.Unmarshal(data, &request); err != nil {
				t.Fatal(err)
			}
			if (request["inventory_since"] != nil) != supports || (request["inventory_branch"] != nil) != supports {
				t.Fatal(request)
			}
		})
	}
}

type failedInventory struct {
	workerFunc
	seen InventoryRequest
}

func (w *failedInventory) Observe(_ context.Context, _ *Town, request InventoryRequest, _ func(Progress), _ *slog.Logger) (RunResult, error) {
	w.seen = request
	return RunResult{}, errors.New("fixture incomplete page")
}
func TestIncrementalFailureDoesNotAdvanceDurableCursor(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	now := time.Now().UTC()
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], RepoSnapshot{Branch: "main", Head: baseSHA}, now.Add(-time.Hour))
	})
	w := &failedInventory{}
	sup := NewSupervisor(s, newGH(1), w)
	if err := sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]); err == nil {
		t.Fatal("incomplete page accepted")
	}
	after := s.Snapshot().Towns[x.ID]
	if w.seen.Since == nil || !after.LastSync.Equal(now.Add(-time.Hour)) || !after.LastFullSync.Equal(now.Add(-time.Hour)) {
		t.Fatal(w.seen, after.LastSync, after.LastFullSync)
	}
}
func TestIncrementalCloseRechecksMissingPRBeforeAnyWrite(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	now := time.Now()
	update(t, s, func(st *State) { st.Towns[x.ID].Tasks["pr:1"].Stage = "closing" })
	gh := newGH(1)
	gh.p.MergedAt = &now
	gh.p.State = "closed"
	sup := NewSupervisor(s, gh, nil)
	if err := sup.closeRetiredPulls(context.Background(), s.Snapshot().Towns[x.ID], RepoSnapshot{Incremental: true}); err != nil {
		t.Fatal(err)
	}
	if len(gh.closedPulls) != 0 || len(gh.deleted) != 0 || len(gh.comments) != 0 {
		t.Fatal("missing delta entry authorized a write")
	}
	gh.p.Number = 2
	if err := sup.closeRetiredPulls(context.Background(), s.Snapshot().Towns[x.ID], RepoSnapshot{Incremental: true}); err == nil {
		t.Fatal("accepted wrong PR identity")
	}
	if len(gh.closedPulls) != 0 || len(gh.deleted) != 0 || len(gh.comments) != 0 {
		t.Fatal("wrong identity authorized write")
	}
}

package town

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	issuebot "github.com/BrokkAi/issue-bot"
)

func writeIssueBotState(t *testing.T, root string, town *Town, jobs map[int]*issuebot.Job) {
	t.Helper()
	dir, stateDir := Workspace(root, town.ID, Issue)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	saved := issuebot.State{
		Format: 1, Remote: "https://github.com/" + town.Config.Repo + ".git",
		Branch: town.Config.Branch, Directory: dir, Repo: town.Config.Repo,
		Host: "github.com", Jobs: jobs,
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBlockedIssueJobSurvivesInventoryAndRestartThenRetries(t *testing.T) {
	root := t.TempDir()
	storeDir := t.TempDir()
	store, err := Open(storeDir, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	town := addTown(t, store)
	update(t, store, func(st *State) {
		current := st.Towns[town.ID]
		current.Initialized = true
		current.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, Title: "Clarify API", House: Issue, Stage: "queued"}
	})
	town = store.Snapshot().Towns[town.ID]
	job := &issuebot.Job{
		Issue: issuebot.Issue{Number: 7}, Branch: "issue-bot/7", Status: "blocked", Tries: 2,
		Failure: "Agent returned blocked.", Result: &issuebot.Result{Status: "blocked", Detail: "The API contract is missing."},
		RetryAt: time.Now().Add(10 * time.Minute),
	}
	writeIssueBotState(t, root, town, map[int]*issuebot.Job{7: job})
	workers := &BotWorkers{Root: root, Store: store}
	if err := workers.SyncIssues(town); err != nil {
		t.Fatal(err)
	}
	assertBlocked := func(current *Town) {
		t.Helper()
		task := current.Tasks["issue:7"]
		if !task.Blocked || task.Stage != "blocked" || task.Attempts != 2 || task.Detail != job.Result.Detail || task.IssueJob == nil || !task.IssueJob.RetryEligible {
			t.Fatalf("blocked job was not imported: %+v", task)
		}
	}
	assertBlocked(store.Snapshot().Towns[town.ID])

	update(t, store, func(st *State) {
		Reconcile(st, st.Towns[town.ID], RepoSnapshot{Branch: "main", Head: baseSHA, Issues: []RemoteIssue{{Number: 7, Title: "Clarify API", State: "open"}}}, time.Now())
	})
	if err := workers.SyncIssues(store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	assertBlocked(store.Snapshot().Towns[town.ID])

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(storeDir, false)
	if err != nil {
		t.Fatal(err)
	}
	workers.Store = store
	assertBlocked(store.Snapshot().Towns[town.ID])

	supervisor := NewSupervisor(store, nil, workers)
	if err := supervisor.Control(town.ID, Issue, "retry", "issue:7"); err != nil {
		t.Fatal(err)
	}
	task := store.Snapshot().Towns[town.ID].Tasks["issue:7"]
	if task.Blocked || task.Stage != "queued" || task.Attempts != 0 || task.IssueJob.Status != "pending" || task.IssueJob.RetryEligible {
		t.Fatalf("retry did not recover the task: %+v", task)
	}
	saved, err := issuebot.ReadState(workers.issueConfig(store.Snapshot().Towns[town.ID]))
	if err != nil || saved.Jobs[7].Status != "pending" || saved.Jobs[7].Tries != 0 || !saved.Jobs[7].RetryAt.IsZero() {
		t.Fatalf("issue-bot retry was not durable: state=%+v err=%v", saved, err)
	}
}

func TestSubmittedIssueJobImportsOwnershipWithoutDuplicateDelivery(t *testing.T) {
	root := t.TempDir()
	store := testStore(t, false)
	town := addTown(t, store)
	update(t, store, func(st *State) {
		st.Towns[town.ID].Tasks["issue:8"] = &Task{ID: "issue:8", Kind: "issue", Number: 8, Title: "Done", House: Issue, Stage: "queued"}
	})
	town = store.Snapshot().Towns[town.ID]
	job := &issuebot.Job{Issue: issuebot.Issue{Number: 8}, Branch: "issue-bot/8", Status: "submitted", Tries: 1, URL: "https://github.com/acme/orchard/pull/18", Result: &issuebot.Result{Status: "solved", Detail: "Implemented."}}
	writeIssueBotState(t, root, town, map[int]*issuebot.Job{8: job})
	workers := &BotWorkers{Root: root, Store: store}
	if err := workers.SyncIssues(town); err != nil {
		t.Fatal(err)
	}
	before := len(store.Snapshot().Events)
	if err := workers.SyncIssues(store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	current := store.Snapshot()
	if current.Towns[town.ID].Tasks["issue:8"].Stage != "implemented" || current.Towns[town.ID].Owned[18].Issue != 8 || len(current.Events) != before {
		t.Fatalf("submitted job was not imported idempotently: %+v", current.Towns[town.ID])
	}
}

package town

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestADecisionNamesThePersonWhoMadeIt(t *testing.T) {
	store, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	x := hallTown(t, store)
	sup := NewSupervisor(store, nil, nil)
	if err := sup.As("alice").Control(x.ID, Hall, "admit", "issue:1"); err != nil {
		t.Fatal(err)
	}
	if err := sup.Control(x.ID, Hall, "decline", "pr:4"); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if got := state.Towns[x.ID].Tasks["issue:1"].Detail; got != "alice accepted this task." {
		t.Fatalf("detail %q", got)
	}
	events := state.Events[len(state.Events)-2:]
	if events[0].Actor != "alice" || events[0].Title != "alice accepted: Bug Bot finding" {
		t.Fatalf("alice's decision %+v", events[0])
	}
	if events[1].Actor != "" || events[1].Title != "You declined: Outside PR" {
		t.Fatalf("the access key's decision %+v", events[1])
	}
}

func TestASnoozeAndARetryNameThePerson(t *testing.T) {
	store, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	x := hallTown(t, store)
	update(t, store, func(st *State) {
		task := st.Towns[x.ID].Tasks["issue:1"]
		task.House, task.Stage, task.MayoralDecision = Issue, "queued", "admitted"
	})
	sup := NewSupervisor(store, nil, nil)
	bob := sup.As("bob")
	if err := bob.Defer(x.ID, "issue:1", time.Now().Add(time.Hour), "after the release"); err != nil {
		t.Fatal(err)
	}
	if err := bob.Control(x.ID, Issue, "retry", "issue:1"); err != nil {
		t.Fatal(err)
	}
	for _, e := range store.Snapshot().Events[len(store.Snapshot().Events)-2:] {
		if e.Actor != "bob" || e.From != "operator" {
			t.Fatalf("event %+v does not name bob", e)
		}
	}
}

func TestASubmittedRequestNamesItsRequester(t *testing.T) {
	store, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	x := addTown(t, store)
	sup := NewSupervisor(store, nil, nil)
	var body string
	sup.Publisher = requestPublisher{post: func(_ context.Context, _, title, b string) (RemoteIssue, error) {
		body = b
		return RemoteIssue{Number: 9, Title: title, Body: b, State: "open"}, nil
	}}
	forged := sampleRequest()
	forged.RequestedBy = "mallory"
	saved, err := sup.As("carol").SubmitRequest(x.ID, forged)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RequestedBy != "carol" {
		t.Fatalf("requester %q", saved.RequestedBy)
	}
	sup.publishRequest(context.Background(), x.ID, saved.ID)
	if !strings.Contains(body, "Requested by @carol through Brokk Town.") || strings.Contains(body, "mallory") {
		t.Fatalf("issue body %q", body)
	}
	unnamed := sampleRequest()
	unnamed.ID = "abcdef01-abcd-4321-1234-123456789abc"
	if r, err := sup.SubmitRequest(x.ID, unnamed); err != nil || r.RequestedBy != "" || strings.Contains(r.githubBody(), "Requested by") {
		t.Fatalf("an access-key request %+v %v", r, err)
	}
}

func TestAJudgmentNamesWhoRecordedIt(t *testing.T) {
	store, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	x := addTown(t, store)
	update(t, store, func(st *State) {
		current := st.Towns[x.ID]
		current.Initialized = true
		issue := RemoteIssue{Number: 3, Title: "Stale reports", Body: "<!-- bug-bot: finding -->", URL: "https://github.com/acme/orchard/issues/3", State: "open"}
		Reconcile(st, current, RepoSnapshot{Branch: "main", Head: baseSHA, Issues: []RemoteIssue{issue}, Released: map[string]bool{}}, time.Now())
	})
	if err := NewSupervisor(store, nil, nil).As("dana").JudgeOutcome(x.ID, "finding-filed:issue:3", "useful", "Real bug"); err != nil {
		t.Fatal(err)
	}
	for _, o := range store.Snapshot().Towns[x.ID].Outcomes {
		if o.ID == "finding-filed:issue:3" {
			if o.Judgment == nil || o.Judgment.RecordedBy != "dana" {
				t.Fatalf("judgment %+v", o.Judgment)
			}
			return
		}
	}
	t.Fatal("no filed finding")
}

func TestStateWithAnInvalidActorDoesNotLoad(t *testing.T) {
	store, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	x := addTown(t, store)
	err = store.Update(func(st *State) error {
		st.Acted(x.ID, "control", "alice", "bug", "", "start bug", time.Now())
		st.Events[len(st.Events)-1].Actor = "<script>"
		return nil
	})
	if err == nil {
		t.Fatal("an invalid actor was saved")
	}
	if !ValidLogin("octo-cat") || !ValidLogin("alice_acme") || ValidLogin("-x") || ValidLogin("a b") || ValidLogin("") {
		t.Fatal("ValidLogin")
	}
	if sup := NewSupervisor(store, nil, nil); sup.As("not a login").actor != "" {
		t.Fatal("an invalid login acted as someone")
	}
}

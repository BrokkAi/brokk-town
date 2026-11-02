package town

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDemoSeedsInspectableBoardAndNoPrivateAgentData(t *testing.T) {
	store := testStore(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, store, time.Hour) }()
	deadline := time.Now().Add(time.Second)
	for len(store.Snapshot().Towns) != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	state := store.Snapshot()
	if len(state.Towns) != 2 {
		t.Fatal("demo did not seed both towns")
	}
	town := state.Towns["brokkai/orchard"]
	if town == nil || len(town.Tasks) < 6 {
		t.Fatalf("demo board is too small: %+v", town)
	}
	stages := map[string]bool{}
	blocked := false
	for _, task := range town.Tasks {
		stages[task.Stage] = true
		blocked = blocked || task.Blocked
	}
	for _, stage := range []string{"inconclusive", "queued", "ready", "shipped"} {
		if !stages[stage] {
			t.Fatalf("demo omitted %s stage", stage)
		}
	}
	for _, stage := range []string{"complete", "closed"} {
		if !stages[stage] {
			t.Fatalf("demo omitted %s done-funnel stage", stage)
		}
	}
	if !blocked {
		t.Fatal("demo omitted blocked work")
	}
	if town.Workers[Bug].Agent == nil || town.Workers[Review].Agent == nil || town.Workers[Bug].Status != "working" || town.Workers[Review].Status != "pausing" {
		t.Fatalf("demo active profiles/statuses missing: bug=%+v review=%+v", town.Workers[Bug], town.Workers[Review])
	}
	public, _ := json.Marshal(state.Public())
	if strings.Contains(string(public), "command") || strings.Contains(string(public), "environment") {
		t.Fatal("demo public state leaked private agent settings")
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("demo ended with %v", err)
	}
}

// Each demo town's opening report describes that town, not its neighbor.
func TestDemoReportsDescribeTheirOwnTown(t *testing.T) {
	store := testStore(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, store, time.Hour) }()
	deadline := time.Now().Add(time.Second)
	for len(store.Snapshot().Towns) != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	towns := store.Snapshot().Towns
	for id, name := range map[string]string{"brokkai/orchard": "orchard", "brokkai/paper-trail": "paper-trail"} {
		town := towns[id]
		if town == nil || len(town.Reports) == 0 {
			t.Fatalf("town %s has no opening report", id)
		}
		if title := strings.ToLower(town.Reports[0].Title); !strings.Contains(title, name) {
			t.Fatalf("town %s opened with %q", id, town.Reports[0].Title)
		}
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("demo ended with %v", err)
	}
}

// The demo's external intake includes an issue Simplifier Bot declines in auto
// mode, recorded exactly as the live service records that decline. Task
// numbers cycle, and a store can outlive a run: a decline already holding the
// reused number is closed first, so the decline plays again rather than the
// task sitting unchanged.
func TestDemoSimplifierDeclinesAnOverreachingExternalIssue(t *testing.T) {
	store := testStore(t, true)
	if err := store.Update(seedDemo); err != nil {
		t.Fatal(err)
	}
	const id = "issue:742"
	update(t, store, func(s *State) {
		// Left by an earlier lap or run under the number the first lap reuses.
		s.Towns["brokkai/orchard"].Tasks[id] = &Task{ID: id, Kind: "issue", Number: 742, Title: demoDeclinedTitle, House: Hall, Stage: "declined", External: true, Simplification: &Simplification{Mode: "auto", Decision: "decline", Detail: "Earlier run."}, Updated: time.Now().Add(-time.Hour)}
	})
	start := time.Now()
	for step := 0; step <= 8; step++ {
		now := start.Add(time.Duration(step) * time.Second)
		if err := store.Update(func(s *State) error {
			if !demoStep(s, step, now) {
				return fmt.Errorf("step %d did not run", step)
			}
			return nil
		}); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		task := store.Snapshot().Towns["brokkai/orchard"].Tasks[id]
		if step == 7 && (task.Stage != "closed" || task.MayoralDecision != "") {
			t.Fatalf("the reused number was not released from custody before its next arrest: %+v", task)
		}
	}
	state := store.Snapshot()
	if err := validateState(state, true); err != nil {
		t.Fatalf("demo state is invalid: %v", err)
	}
	town := state.Towns["brokkai/orchard"]
	declined := town.Tasks[id]
	if declined == nil || declined.Kind != "issue" || declined.Number != 742 || declined.Title != demoDeclinedTitle || !declined.External || declined.House != Hall || declined.Stage != "declined" || declined.MayoralDecision != "" || !autoDeclined(declined) || !closableDecline(declined) {
		t.Fatalf("the task is not an auto-mode Simplifier decline: %+v", declined)
	}
	if s := declined.Simplification; s.Summary == "" || s.Detail == "" || declined.Detail != "Simplifier Bot declined this issue. Town is closing it." {
		t.Fatalf("the dismissal does not explain itself: %+v %+v", declined, s)
	}
	dismissed := false
	for _, e := range state.Events {
		if e.Kind == "decision" && e.From == string(Simplifier) && e.To == "hall" && e.Cargo == id && e.Title == "Simplifier Bot declined an external issue: "+demoDeclinedTitle {
			dismissed = true
		}
	}
	if !dismissed {
		t.Fatalf("the activity log did not report the decline: %+v", state.Events)
	}
	// Triage can still accept it anyway, as it can any live auto decline.
	update(t, store, func(s *State) {
		if err := s.decideTask(s.Towns["brokkai/orchard"], s.Towns["brokkai/orchard"].Tasks[id], "admit", "you", start); err != nil {
			t.Fatalf("triage could not accept the declined task: %v", err)
		}
	})
}

package town

import (
	"context"
	"encoding/json"
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

package town

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllPausedHousesStartAndProcessesAreReusedAndStopped(t *testing.T) {
	store := testStore(t, false)
	town := addTown(t, store)
	update(t, store, func(s *State) {
		for _, w := range s.Towns[town.ID].Workers {
			w.Enabled = false
		}
	})
	b := &BotWorkers{Root: t.TempDir(), Store: store, botCommands: map[Role]string{}}
	defer b.Close()
	for _, role := range AgentRoles {
		path := filepath.Join(t.TempDir(), "worker")
		caps, _ := json.Marshal(workerCapabilities[role])
		body := strings.Replace(pythonFakeWorker, "'bot': 'issue-bot'", "'bot': '"+workerBotNames[role]+"'", 1)
		body = strings.Replace(body, "capabilities = ['run', 'progress', 'issue-result', 'exact-issue']", "capabilities = "+string(caps), 1)
		writeFakeWorker(t, path, body)
		b.botCommands[role] = path
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := b.SyncProcesses(ctx, store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if len(b.pool) != 8 {
		t.Fatalf("started %d workers", len(b.pool))
	}
	processes := map[string]*workerProcess{}
	for k, p := range b.pool {
		processes[k] = p
		if !p.alive() {
			t.Fatal("idle worker exited")
		}
	}
	if err := b.SyncProcesses(ctx, store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	p := b.pool[town.ID+":"+string(Issue)]
	for range 2 {
		_, err := p.run(ctx, workerRequest{Protocol: 1}, false, time.Now().Add(time.Minute), func(Progress) {}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	for key, p := range processes {
		if b.pool[key] != p {
			t.Fatal("worker replaced between jobs")
		}
	}
	update(t, store, func(s *State) { s.Towns[town.ID].Deleted = true })
	if err := b.SyncProcesses(ctx, store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	for _, p := range processes {
		if p.alive() {
			t.Fatal("deleted town left worker running")
		}
	}
	if len(b.pool) != 0 {
		t.Fatal("deleted town retained processes")
	}
}

func TestProcessCancellationPreservesUncertainOutcome(t *testing.T) {
	t.Setenv("TOWN_WORKER_TEST_MODE", "hang")
	t.Setenv("TOWN_WORKER_TEST_RELEASE", filepath.Join(t.TempDir(), "release"))
	path := filepath.Join(t.TempDir(), "worker")
	writeFakeWorker(t, path, pythonFakeWorker)
	b := &BotWorkers{botCommands: map[Role]string{Issue: path}}
	bot, err := b.externalBot(context.Background(), Config{}, Issue)
	if err != nil {
		t.Fatal(err)
	}
	p, err := startWorkerProcess(context.Background(), bot)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := p.run(ctx, workerRequest{Protocol: 1}, false, time.Now().Add(time.Minute), func(Progress) {
			select {
			case <-started:
			default:
				close(started)
			}
		}, nil)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("interrupted run reported success")
	}
	if p.alive() {
		t.Fatal("canceled worker survived")
	}
}

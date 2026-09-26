package town

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

func attentionTask(st *State, id string, number int, blocked bool) {
	taskID := "issue:" + fmtNumber(number)
	if task := st.Towns[id].Tasks[taskID]; task != nil {
		task.Blocked = blocked
		return
	}
	st.Towns[id].Tasks[taskID] = &Task{ID: taskID, Kind: "issue", Number: number, House: Issue, Stage: "queued", Blocked: blocked}
}
func fmtNumber(n int) string { return fmt.Sprint(n) }
func waitAttention(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("attention watcher did not settle")
		}
		time.Sleep(time.Millisecond * 5)
	}
}
func startAttention(t *testing.T, s *Supervisor) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.watchAttention(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("attention watcher did not stop")
		}
	})
	return cancel
}
func attentionLines(path string) []string {
	data, _ := os.ReadFile(path)
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
func fixtureHook(path string) []string { return []string{"/bin/sh", "-c", `cat >> "$1"`, "hook", path} }

func TestAttentionTransitionsDurableAndOffWorkerLoop(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	s := NewSupervisor(store, nil, nil)
	output := filepath.Join(t.TempDir(), "notices")
	command := fixtureHook(output)
	if err := s.SetAttentionHook(AttentionHookEdit{Command: &command}); err != nil {
		t.Fatal(err)
	}
	update(t, store, func(st *State) { attentionTask(st, x.ID, 1, true) })
	if len(store.Snapshot().AttentionPending) != 0 {
		t.Fatal("disabled hook queued work")
	}
	if err := s.SetAttentionHook(AttentionHookEdit{Enabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	// Repeated snapshots and a short-lived new identity must not duplicate or
	// lose notifications, even before the watcher has had time to run.
	for range 3 {
		update(t, store, func(*State) {})
	}
	update(t, store, func(st *State) { attentionTask(st, x.ID, 2, true) })
	update(t, store, func(st *State) { attentionTask(st, x.ID, 2, false) })
	before := store.Snapshot()
	startAttention(t, s)
	waitAttention(t, func() bool {
		return len(attentionLines(output)) == 2 && store.Snapshot().AttentionActive == nil && len(store.Snapshot().AttentionPending) == 0
	})
	if !reflect.DeepEqual(before.Towns, store.Snapshot().Towns) {
		t.Fatal("hook changed scheduler state")
	}
	for _, line := range attentionLines(output) {
		var n AttentionNotice
		if json.Unmarshal([]byte(line), &n) != nil || n.Town != x.ID || n.Reason != "blocked" || !n.valid() {
			t.Fatal(line)
		}
	}
	update(t, store, func(st *State) { attentionTask(st, x.ID, 1, false) })
	update(t, store, func(st *State) { attentionTask(st, x.ID, 1, true) })
	waitAttention(t, func() bool { return len(attentionLines(output)) == 3 && store.Snapshot().AttentionActive == nil })
	if err := s.SetAttentionHook(AttentionHookEdit{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	update(t, store, func(st *State) { attentionTask(st, x.ID, 3, true) })
	if len(store.Snapshot().AttentionPending) != 0 {
		t.Fatal("disabled hook queued work")
	}
}

func TestAttentionRestartKeepsQueueButNeverReplaysAnUncertainClaim(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	s := NewSupervisor(store, nil, nil)
	output := filepath.Join(t.TempDir(), "notices")
	command := fixtureHook(output)
	if err := s.SetAttentionHook(AttentionHookEdit{Enabled: ptr(true), Command: &command}); err != nil {
		t.Fatal(err)
	}
	update(t, store, func(st *State) { attentionTask(st, x.ID, 1, true); attentionTask(st, x.ID, 2, true) })
	update(t, store, func(st *State) {
		for key, n := range st.AttentionPending {
			if n.Task == "issue:1" {
				st.AttentionActive = &n
				delete(st.AttentionPending, key)
			}
		}
	})
	dir := filepath.Dir(store.path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.Snapshot().ServiceConfig.AttentionHook.Enabled {
		t.Fatal("lost hook settings")
	}
	startAttention(t, NewSupervisor(reopened, nil, nil))
	waitAttention(t, func() bool { return len(attentionLines(output)) == 1 && reopened.Snapshot().AttentionActive == nil })
	var n AttentionNotice
	_ = json.Unmarshal([]byte(attentionLines(output)[0]), &n)
	if n.Task != "issue:2" {
		t.Fatal("replayed uncertain delivery", n)
	}
}

func TestAttentionProjectionAndSnoozeExpiry(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	update(t, store, func(st *State) {
		st.ServiceConfig.AttentionHook = AttentionHook{Enabled: true, Command: []string{"fixture"}}
		attentionTask(st, x.ID, 1, true)
		task := st.Towns[x.ID].Tasks["issue:1"]
		task.DeferredUntil = time.Now().Add(time.Hour)
		attentionTask(st, x.ID, 2, true)
		st.Towns[x.ID].Tasks["issue:2"].Stage = "closed"
		st.Towns[x.ID].Workers[Review].Status = "failed"
	})
	st := store.Snapshot()
	if len(st.AttentionPending) != 1 {
		t.Fatal(st.AttentionPending)
	}
	previous := clone(st)
	queueAttention(previous, &st, time.Now().Add(2*time.Hour))
	if len(st.AttentionPending) != 2 {
		t.Fatal("snooze expiry did not introduce attention", st.AttentionPending)
	}
	for _, n := range st.AttentionPending {
		if n.Task == "issue:2" {
			t.Fatal("terminal task notified")
		}
	}
}

func TestAttentionHookTimeoutFailureAndPrivacy(t *testing.T) {
	notice := AttentionNotice{Town: "acme/project", Task: "issue:1", Role: Issue, Reason: "blocked", Seq: 7}
	started := time.Now()
	command := []string{"/bin/sh", "-c", "while :; do printf private-output; done"}
	if got := runAttentionHook(context.Background(), t.TempDir(), command, notice, 80*time.Millisecond); got != "interrupted_or_timed_out" || time.Since(started) > 2*time.Second {
		t.Fatal(got, time.Since(started))
	}
	store := testStore(t, false)
	x := addTown(t, store)
	s := NewSupervisor(store, nil, nil)
	sink := &osrun.Tail{Capacity: 4096}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(sink, nil)))
	defer slog.SetDefault(previous)
	command = []string{"/bin/sh", "-c", "printf private-output; printf private-error >&2; exit 9", "private-argument"}
	if err := s.SetAttentionHook(AttentionHookEdit{Enabled: ptr(true), Command: &command}); err != nil {
		t.Fatal(err)
	}
	update(t, store, func(st *State) { attentionTask(st, x.ID, 1, true) })
	before := store.Snapshot().Towns
	startAttention(t, s)
	waitAttention(t, func() bool {
		text, _ := sink.Text()
		return strings.Contains(text, "did not complete") && store.Snapshot().AttentionActive == nil
	})
	logs, _ := sink.Text()
	public, _ := json.Marshal(store.Snapshot().Public())
	if strings.Contains(logs, "private-") || strings.Contains(string(public), "private-") || strings.Contains(string(public), "attention_pending") {
		t.Fatal("private hook data exposed")
	}
	if !reflect.DeepEqual(before, store.Snapshot().Towns) {
		t.Fatal("failure changed town state")
	}
}

func TestDemoNeverQueuesOrRunsAttentionHook(t *testing.T) {
	store := testStore(t, true)
	x := addTown(t, store)
	s := NewSupervisor(store, nil, nil)
	output := filepath.Join(t.TempDir(), "forbidden")
	command := fixtureHook(output)
	if err := s.SetAttentionHook(AttentionHookEdit{Enabled: ptr(true), Command: &command}); err != nil {
		t.Fatal(err)
	}
	update(t, store, func(st *State) { attentionTask(st, x.ID, 1, true); st.Towns[x.ID].Workers[Review].Status = "failed" })
	startAttention(t, s)
	if len(store.Snapshot().AttentionPending) != 0 {
		t.Fatal("demo queued a hook")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("demo ran hook")
	}
}

package botapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/artifact"
	"github.com/BrokkAi/brokk-town/internal/filelock"
)

func TestDirsRefuseNamesThatEscapeTheRoot(t *testing.T) {
	root := t.TempDir()
	checkout, state, err := Dirs(root, "0123abcd", "issue")
	if err != nil || checkout != filepath.Join(root, "towns", "0123abcd", "issue", "checkout") || state != filepath.Join(root, "towns", "0123abcd", "issue", "state") {
		t.Fatalf("Dirs = %q %q %v", checkout, state, err)
	}
	for _, bad := range [][2]string{{"", "issue"}, {"..", "issue"}, {"a/b", "issue"}, {"town", "../state"}, {"Town", "issue"}, {"town", ""}, {strings.Repeat("a", 65), "issue"}} {
		if _, _, err := Dirs(root, bad[0], bad[1]); err == nil {
			t.Fatalf("Dirs accepted %q", bad)
		}
	}
}

// A request is all a bot is sent, so it must survive JSON on its own and name
// no path on the service's filesystem.
func TestRequestRoundTripsAndCarriesNoServicePath(t *testing.T) {
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	request := Request{Town: "0123abcd", Role: "review", Remote: "https://github.com/o/r.git", Branch: "main", Repo: "o/r", Host: "github.com", PR: 7, Mode: "judge", Retry: true, Since: &since, Arrival: json.RawMessage(`{"title":"x"}`), Agent: Agent{Command: []string{"agent"}, Model: "m"}, Policy: &Policy{Labels: []string{"bug"}}}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"directory"`, `"state_directory"`} {
		if strings.Contains(string(data), field) {
			t.Fatalf("request names a service path: %s", data)
		}
	}
	var back Request
	if err = json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, request) {
		t.Fatalf("round trip changed the request: %+v %v", back, err)
	}
}

func TestWorkerResolvesWorkspaceAndStreamsEvents(t *testing.T) {
	root := t.TempDir()
	var events []Event
	var straggler func(phase, task string)
	w := Worker{Name: "bug-bot", Role: "bug", Root: root, Handle: func(_ context.Context, job Job) (Result, error) {
		checkout, state, _ := Dirs(root, "precinct", "bug")
		if job.Directory != checkout || job.StateDirectory != state {
			t.Errorf("job workspace = %q %q", job.Directory, job.StateDirectory)
		}
		if !reflect.DeepEqual(job.Launch.Command, []string{"configured-agent"}) || job.Launch.Model != "m" {
			t.Errorf("explicit agent not passed through: %+v", job.Launch)
		}
		job.Progress("", "  "+strings.Repeat("x", 5000))
		job.Log.With("issue", 7).WithGroup("git").Warn("pushed", "branch", "fix")
		straggler = job.Progress
		return Result{}, errors.New("stopped")
	}}
	_, err := w.Run(context.Background(), Request{Town: "precinct", Role: "bug", Agent: Agent{Command: []string{"configured-agent"}, Model: "m"}}, func(e Event) { events = append(events, e) })
	if err == nil || err.Error() != "stopped" {
		t.Fatalf("handler error lost: %v", err)
	}
	straggler("late", "after return")
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if p := events[0].Progress; p == nil || p.Phase != "running" || len(p.Task) > 4096+len("…[truncated]") || strings.HasPrefix(p.Task, " ") {
		t.Fatalf("progress not normalized: %+v", events[0])
	}
	l := events[1].Log
	if l == nil || l.Level != "WARN" || l.Message != "pushed" || l.Attrs["bot"] != "bug-bot" || l.Attrs["issue"] != "7" || l.Attrs["git.branch"] != "fix" {
		t.Fatalf("log not carried: %+v", events[1])
	}
	if _, err := w.Run(context.Background(), Request{Town: "precinct", Role: "review"}, nil); err == nil {
		t.Fatal("bug-bot ran the review unit")
	}
}

func TestWorkerReportsOnlyTheTranscriptsARunWrote(t *testing.T) {
	root := t.TempDir()
	_, state, _ := Dirs(root, "precinct", "review")
	sessions := filepath.Join(state, "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "old.jsonl"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := Worker{Name: "review-bot", Role: "review", Root: root, Handle: func(ctx context.Context, job Job) (Result, error) {
		if err := os.WriteFile(filepath.Join(job.StateDirectory, "sessions", "new.jsonl"), []byte("during\n"), 0600); err != nil {
			return Result{}, err
		}
		// A canceled run still reports what it wrote.
		return Result{}, context.Canceled
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, _ := w.Run(ctx, Request{Town: "precinct", Role: "review"}, nil)
	digest, size, _ := artifact.Digest(context.Background(), filepath.Join(sessions, "new.jsonl"))
	if !reflect.DeepEqual(result.Transcripts, []Transcript{{Path: "state/sessions/new.jsonl", Digest: digest, Bytes: size}}) {
		t.Fatalf("transcripts = %+v", result.Transcripts)
	}
}

func TestStorageInventoriesAndRemovesOnlyMatchingTranscripts(t *testing.T) {
	root := t.TempDir()
	w := Worker{Name: "issue-bot", Role: "issue", Root: root}
	checkout, state, _ := Dirs(root, "precinct", "issue")
	sessions := filepath.Join(state, "sessions")
	for _, dir := range []string{checkout, sessions} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(checkout, "README"), "code")
	write(filepath.Join(state, "state.json"), "{}")
	write(filepath.Join(sessions, "a.jsonl"), "a\n")
	write(filepath.Join(sessions, "b.jsonl"), "b\n")

	got, err := w.Storage(context.Background(), StorageRequest{Town: "precinct", Role: "issue", Verify: []string{"state/sessions/a.jsonl"}})
	if err != nil || got.Incomplete {
		t.Fatal(got, err)
	}
	kinds := map[string]StorageEntry{}
	for _, e := range got.Entries {
		kinds[e.Path] = e
	}
	if kinds["checkout"].Kind != "workspace" || kinds["state/state.json"].Kind != "worker state" || kinds["state/sessions/a.jsonl"].Kind != "transcript" || len(kinds) != 4 {
		t.Fatalf("inventory = %+v", got.Entries)
	}
	a, b := kinds["state/sessions/a.jsonl"], kinds["state/sessions/b.jsonl"]
	if a.Digest == "" || b.Digest != "" {
		t.Fatalf("digested the wrong transcripts: a=%q b=%q", a.Digest, b.Digest)
	}

	// The bot's own state lock refuses removal while it works.
	lock, err := os.OpenFile(filepath.Join(state, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = filelock.TryLock(lock); err != nil {
		t.Fatal(err)
	}
	remove := StorageRequest{Town: "precinct", Role: "issue", Remove: []Transcript{
		{Path: "state/sessions/a.jsonl", Digest: a.Digest, Bytes: a.Bytes},
		{Path: "state/sessions/b.jsonl", Digest: strings.Repeat("0", 64), Bytes: 2},
		{Path: "state/state.json", Digest: a.Digest, Bytes: a.Bytes},
		{Path: "state/sessions/../../checkout/README", Digest: a.Digest, Bytes: a.Bytes},
	}}
	refused, err := w.Storage(context.Background(), remove)
	if err != nil || len(refused.Removed) != 4 || refused.Removed[0].Status != "retained" {
		t.Fatal(refused, err)
	}
	filelock.Unlock(lock)
	lock.Close()

	done, err := w.Storage(context.Background(), remove)
	if err != nil {
		t.Fatal(err)
	}
	statuses := []string{}
	for _, r := range done.Removed {
		statuses = append(statuses, r.Status)
	}
	if !reflect.DeepEqual(statuses, []string{"removed", "retained", "retained", "retained"}) {
		t.Fatalf("removal statuses = %v (%+v)", statuses, done.Removed)
	}
	for path, gone := range map[string]bool{filepath.Join(sessions, "a.jsonl"): true, filepath.Join(sessions, "b.jsonl"): false, filepath.Join(state, "state.json"): false, filepath.Join(checkout, "README"): false} {
		if _, err := os.Stat(path); os.IsNotExist(err) != gone {
			t.Fatalf("%s removed=%v, want %v", path, os.IsNotExist(err), gone)
		}
	}
}

func TestStreamIsSafeForConcurrentEmitters(t *testing.T) {
	var mu sync.Mutex
	count := 0
	s := &stream{emit: func(Event) { mu.Lock(); count++; mu.Unlock() }}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.progress("p", "t") }()
	}
	wg.Wait()
	s.close()
	s.progress("p", "after close")
	if count != 8 {
		t.Fatalf("delivered %d events", count)
	}
}

func TestTruncateKeepsRunes(t *testing.T) {
	if got := Truncate(strings.Repeat("界", 3), 4); got != "界…[truncated]" {
		t.Fatalf("Truncate split a rune: %q", got)
	}
	if got := Truncate("short", 10); got != "short" {
		t.Fatal(got)
	}
}

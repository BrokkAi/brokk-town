package releasebot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The hook records each invocation's stdin, arguments and the state file it
// can read from its working directory.
const hookScript = `#!/bin/sh
out=$1
shift
id=$(printf %04d "$(ls "$out" | grep -c '^event-')")
cp state.json "$out/state-$id.json"
for a in "$@"; do printf '%s\n' "$a"; done > "$out/args-$id"
cat > "$out/event-$id.json"
`

var hookArgs = []string{"two words", "$HOME;`id`|*&>x"}

type hookRecord struct {
	raw   []byte
	event NotificationEvent
	state State
	args  []string
}

func withHook(t *testing.T, f *fixture) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "hook")
	if err := os.WriteFile(script, []byte(hookScript), 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	f.engine.config.Notify = append([]string{script, out}, hookArgs...)
	f.engine.config.NotifyTimeout = Duration(10 * time.Second)
	return out
}

func hookRecords(t *testing.T, out string) []hookRecord {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(out, "event-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	var records []hookRecord
	for _, file := range files {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file), "event-"), ".json")
		var r hookRecord
		if r.raw, err = os.ReadFile(file); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(r.raw, &r.event); err != nil {
			t.Fatalf("unparseable event %q: %v", r.raw, err)
		}
		state, err := os.ReadFile(filepath.Join(out, "state-"+id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(state, &r.state); err != nil {
			t.Fatal(err)
		}
		args, err := os.ReadFile(filepath.Join(out, "args-"+id))
		if err != nil {
			t.Fatal(err)
		}
		r.args = strings.Split(strings.TrimSuffix(string(args), "\n"), "\n")
		records = append(records, r)
	}
	return records
}

func events(records []hookRecord) []string {
	var names []string
	for _, r := range records {
		names = append(names, r.event.Event)
	}
	return names
}

// assertPrivate checks that the payload carries identifiers only.
func assertPrivate(t *testing.T, f *fixture, raw []byte, secrets ...string) {
	t.Helper()
	cfg := f.engine.config
	for _, private := range append([]string{cfg.Directory, cfg.StateDirectory, cfg.Remote, filepath.Dir(cfg.Directory), "RELEASE_BOT_NOTIFY_SECRET", "notify-env-secret", "fixture artifact", "codex-acp"}, secrets...) {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("payload leaks %q: %s", private, raw)
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	allowed := []string{"version", "event", "time", "repository", "branch", "job", "release", "target", "commit", "tag", "attempts", "max_attempts"}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			t.Fatalf("unexpected payload field %q", key)
		}
	}
}

func publishingAgent(t *testing.T, f *fixture, complete bool, publications *int) scriptedAgent {
	return scriptedAgent(func(_ context.Context, p string) (Result, error) {
		if strings.HasPrefix(p, "# Publishability") {
			return Result{Status: "ready", Plan: f.plan()}, nil
		}
		*publications++
		return f.published(t, complete), nil
	})
}

func TestNotifyConfiguration(t *testing.T) {
	if cfg := DefaultConfig(); len(cfg.Notify) != 0 || cfg.NotifyTimeout != 0 {
		t.Fatal("notification is not disabled by default")
	}
	base := `{"remote":"git@github.com:org/repo.git"`
	for body, valid := range map[string]bool{
		base + `,"notify":["/opt/notify","a b"],"notify_timeout":"30s"}`:      true,
		base + `,"notify":[],"notify_timeout":"30s"}`:                         true,
		base + `,"notify":[]}`:                                                true,
		base + `,"notify":["/opt/notify"]}`:                                   false,
		base + `,"notify":["/opt/notify"],"notify_timeout":"0s"}`:             false,
		base + `,"notify":["/opt/notify"],"notify_timeout":"-1s"}`:            false,
		base + `,"notify_timeout":"-1s"}`:                                     false,
		base + `,"notify":[""],"notify_timeout":"30s"}`:                       false,
		base + `,"notify":["  "],"notify_timeout":"30s"}`:                     false,
		base + `,"notify":["/opt/notify","a\u0000b"],"notify_timeout":"30s"}`: false,
		base + `,"notify":"/opt/notify","notify_timeout":"30s"}`:              false,
	} {
		file := filepath.Join(t.TempDir(), "config.json")
		writeTestFile(t, file, body)
		if _, err := ReadConfig(file); (err == nil) != valid {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func TestNotifyVerifiedPublicationOnceAfterStateSaved(t *testing.T) {
	t.Setenv("RELEASE_BOT_NOTIFY_SECRET", "notify-env-secret")
	f := newFixture(t)
	out := withHook(t, f)
	publications := 0
	f.engine.agent = publishingAgent(t, f, true, &publications)
	if err := f.engine.run(context.Background(), true, false); err != nil {
		t.Fatal(err)
	}
	records := hookRecords(t, out)
	if len(records) != 1 {
		t.Fatalf("events = %v", events(records))
	}
	r := records[0]
	e := r.event
	if e.Version != NotificationVersion || e.Event != "verified" || e.Commit != f.head || e.Tag != "v1.0.0" || e.Release != "release:v1.0.0" ||
		e.Branch != "master" || e.Attempts != 1 || e.MaxAttempts != 3 || !strings.HasPrefix(e.Job, "job:brb/release-") || !e.Time.Equal(f.now) {
		t.Fatalf("bad event: %s", r.raw)
	}
	if r.state.Released != f.head || r.state.Job != nil || r.state.LastResult == nil || r.state.LastResult.Tag != "v1.0.0" || len(r.state.History) != 1 {
		t.Fatalf("hook ran before the receipt was saved: %+v", r.state)
	}
	if !slices.Equal(r.args, hookArgs) {
		t.Fatalf("arguments changed: %q", r.args)
	}
	assertPrivate(t, f, r.raw)
	// Ordinary later polls do not repeat the event.
	f.now = f.now.Add(time.Hour)
	if err := f.engine.run(context.Background(), true, false); err != nil {
		t.Fatal(err)
	}
	if n := len(hookRecords(t, out)); n != 1 || publications != 1 {
		t.Fatalf("repeated: events=%d publications=%d", n, publications)
	}
}

func TestNotifyPartialPublicationThenReconciliation(t *testing.T) {
	f := newFixture(t)
	out := withHook(t, f)
	publications := 0
	f.engine.agent = publishingAgent(t, f, false, &publications)
	if err := f.engine.run(context.Background(), true, false); err == nil || errors.Is(err, errAttemptsExhausted) {
		t.Fatalf("partial publication: %v", err)
	}
	if n := len(hookRecords(t, out)); n != 0 {
		t.Fatal("partial publication notified")
	}
	writeTestFile(t, f.registry, "completed")
	f.now = f.now.Add(time.Hour)
	if err := f.engine.run(context.Background(), true, false); err != nil {
		t.Fatal(err)
	}
	records := hookRecords(t, out)
	if len(records) != 1 || records[0].event.Event != "verified" || records[0].event.Tag != "v1.0.0" || records[0].event.Attempts != 1 || publications != 1 {
		t.Fatalf("reconciliation: events=%v publications=%d", events(records), publications)
	}
	f.now = f.now.Add(time.Hour)
	if err := f.engine.run(context.Background(), true, false); err != nil {
		t.Fatal(err)
	}
	if n := len(hookRecords(t, out)); n != 1 {
		t.Fatal("ordinary poll repeated verified")
	}
}

func TestNotifyExhaustedOncePerRunAndStartupReconciliation(t *testing.T) {
	f := newFixture(t)
	out := withHook(t, f)
	f.engine.config.Attempts = 1
	f.engine.config.Poll = Duration(time.Millisecond)
	publications := 0
	f.engine.agent = publishingAgent(t, f, false, &publications)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Newly exhausted: the daemon exits instead of polling again.
	if err := f.engine.run(ctx, false, false); !errors.Is(err, errAttemptsExhausted) {
		t.Fatal(err)
	}
	records := hookRecords(t, out)
	if len(records) != 1 || records[0].event.Event != "exhausted" {
		t.Fatalf("events = %v", events(records))
	}
	e := records[0].event
	if e.Attempts != 1 || e.MaxAttempts != 1 || e.Target != f.head || e.Commit != f.head || e.Tag != "v1.0.0" || e.Release != "" {
		t.Fatalf("bad event: %s", records[0].raw)
	}
	failure := fixtureState(t, f).Job.Failure
	if failure == "" {
		t.Fatal("fixture has no recorded failure")
	}
	assertPrivate(t, f, records[0].raw, failure, f.registry)
	// Startup with an exhausted pending job notifies once more, with the same job.
	restarted := *f.engine
	restarted.starting = true
	if err := restarted.run(ctx, false, false); !errors.Is(err, errAttemptsExhausted) {
		t.Fatal(err)
	}
	records = hookRecords(t, out)
	if len(records) != 2 || records[1].event.Event != "exhausted" || records[1].event.Job != e.Job {
		t.Fatalf("events = %v", events(records))
	}
	// Successful startup reconciliation reports verified instead.
	writeTestFile(t, f.registry, "completed")
	restarted = *f.engine
	restarted.starting = true
	if err := restarted.run(ctx, true, false); err != nil {
		t.Fatal(err)
	}
	records = hookRecords(t, out)
	if !slices.Equal(events(records), []string{"exhausted", "exhausted", "verified"}) || records[2].event.Job != e.Job || publications != 1 {
		t.Fatalf("events=%v publications=%d", events(records), publications)
	}
}

func TestNotifyBackoffAndCancellationAreSilent(t *testing.T) {
	f := newFixture(t)
	out := withHook(t, f)
	f.engine.agent = scriptedAgent(func(context.Context, string) (Result, error) {
		return Result{}, errors.New("fixture failure")
	})
	if err := f.engine.run(context.Background(), true, false); err == nil || errors.Is(err, errAttemptsExhausted) {
		t.Fatalf("expected ordinary failure: %v", err)
	}
	if err := f.engine.run(context.Background(), true, false); err != nil {
		t.Fatal("backoff should skip:", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.now = f.now.Add(time.Hour)
	f.engine.agent = scriptedAgent(func(ctx context.Context, _ string) (Result, error) {
		cancel()
		return Result{}, ctx.Err()
	})
	if err := f.engine.run(ctx, false, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if n := len(hookRecords(t, out)); n != 0 {
		t.Fatalf("%d events for backoff or cancellation", n)
	}
}

func TestNotifyFailuresPreserveReleaseOutcome(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-notifier")
	for name, tc := range map[string]struct {
		command []string
		want    error
	}{
		"missing path": {[]string{missing}, errNotifyMissing},
		"missing name": {[]string{"release-bot-no-such-notifier"}, errNotifyMissing},
		"nonzero exit": {[]string{"sh", "-c", "cat >/dev/null; echo refused >&2; exit 3"}, errNotifyExit},
		"timeout":      {[]string{"sh", "-c", "sleep 30"}, errNotifyTimeout},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			var logs bytes.Buffer
			f.engine.log = slog.New(slog.NewTextHandler(&logs, nil))
			f.engine.config.Notify = tc.command
			f.engine.config.NotifyTimeout = Duration(200 * time.Millisecond)
			if err := f.engine.notify(context.Background(), NotificationEvent{Event: "verified"}); !errors.Is(err, tc.want) {
				t.Fatalf("classified as %v", err)
			}
			if !strings.Contains(logs.String(), tc.want.Error()) {
				t.Fatalf("failure not reported: %s", logs.String())
			}
			// A verified release stays verified and is not published again.
			publications := 0
			f.engine.agent = publishingAgent(t, f, true, &publications)
			if err := f.engine.run(context.Background(), true, false); err != nil {
				t.Fatal(err)
			}
			if s := fixtureState(t, f); s.Released != f.head || s.Job != nil {
				t.Fatalf("hook failure changed the baseline: %+v", s)
			}
			f.now = f.now.Add(time.Hour)
			if err := f.engine.run(context.Background(), true, false); err != nil || publications != 1 {
				t.Fatalf("republished after hook failure: %v %d", err, publications)
			}
			if !strings.Contains(logs.String(), "event=verified") {
				t.Fatal("verified hook failure not logged")
			}
		})
	}
	t.Run("exhausted", func(t *testing.T) {
		f := newFixture(t)
		f.engine.config.Attempts = 1
		f.engine.config.Notify = []string{"sh", "-c", "exit 9"}
		f.engine.config.NotifyTimeout = Duration(5 * time.Second)
		f.engine.agent = scriptedAgent(func(context.Context, string) (Result, error) {
			return Result{}, errors.New("fixture failure")
		})
		if err := f.engine.run(context.Background(), false, false); !errors.Is(err, errAttemptsExhausted) {
			t.Fatal(err)
		}
		if s := fixtureState(t, f); s.Job.Tries != 1 || s.Job.Failure != "preflight agent: fixture failure" || !s.Job.RetryAt.After(f.now) {
			t.Fatalf("hook failure altered the job: %+v", s.Job)
		}
	})
}

func TestNotifyTimeoutTerminatesDescendants(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(t.TempDir(), "descendant-survived")
	f.engine.config.Notify = []string{"sh", "-c", `(sleep 1; touch "$0") & wait`, marker}
	f.engine.config.NotifyTimeout = Duration(100 * time.Millisecond)
	start := time.Now()
	if err := f.engine.notify(context.Background(), NotificationEvent{Event: "verified"}); !errors.Is(err, errNotifyTimeout) {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout did not bound the command")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("descendant outlived the notification timeout")
	}
}

func TestNotifyDisabledRunsNothing(t *testing.T) {
	f := newFixture(t)
	if err := f.engine.notify(context.Background(), NotificationEvent{Event: "verified"}); err != nil {
		t.Fatal(err)
	}
	publications := 0
	f.engine.agent = publishingAgent(t, f, true, &publications)
	if err := f.engine.run(context.Background(), true, false); err != nil || publications != 1 {
		t.Fatal(err)
	}
}

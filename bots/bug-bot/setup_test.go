package bugbot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

const setupTask = "Running workspace setup"

// setupCounter configures a setup command that appends one line per run to a
// file outside the scan worktree and then runs script in the worktree.
func setupCounter(t *testing.T, e *engine, script string) func() int {
	t.Helper()
	counter := filepath.Join(canonicalTestDir(t), "runs")
	e.config.Setup = []string{"sh", "-c", `echo run >> "$0"; ` + script, counter}
	if err := e.config.Validate(); err != nil {
		t.Fatal(err)
	}
	return func() int {
		raw, err := os.ReadFile(counter)
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(raw), "run\n")
	}
}

func TestSetupConfig(t *testing.T) {
	p := filepath.Join(canonicalTestDir(t), "config.json")
	for _, setup := range []string{`[]`, `[""]`, `["  "]`, `"./install"`, `[1]`, `{"command":["./install"]}`, `["./install",2]`} {
		writeTestFile(t, p, `{"remote":"https://github.com/o/r.git","setup":`+setup+`}`)
		if _, err := ReadConfig(p); err == nil {
			t.Fatalf("accepted setup %s", setup)
		}
	}
	for raw, want := range map[string][]string{
		`{"remote":"https://github.com/o/r.git"}`:                                  nil,
		`{"remote":"https://github.com/o/r.git","setup":null}`:                     nil,
		`{"remote":"https://github.com/o/r.git","setup":["./install","--frozen"]}`: {"./install", "--frozen"},
	} {
		writeTestFile(t, p, raw)
		c, err := ReadConfig(p)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(c.Setup, want) || (want == nil) != (c.Setup == nil) {
			t.Fatalf("%s: setup %q, want %q", raw, c.Setup, want)
		}
	}
	example, err := ReadConfig("bug-bot.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if DefaultConfig().Setup != nil || example.Setup != nil {
		t.Fatal("setup is configured by default")
	}
}

// Setup runs in the detached worktree with BUG_COMMIT at HEAD, and the untracked
// and ignored prerequisites it creates are visible to discovery and review.
func TestSetupPreparesWorktreeForAgents(t *testing.T) {
	e, s, f, a, source := fixture(t)
	localGit(t, source, "switch", "main")
	writeTestFile(t, filepath.Join(source, ".gitignore"), "deps/\n")
	localGit(t, source, "add", ".")
	localGit(t, source, "commit", "-m", "ignore dependencies")
	localGit(t, source, "push", "origin", "main")
	runs := setupCounter(t, &e, `test "$(git rev-parse HEAD)" = "$BUG_COMMIT" &&
test "$(git rev-parse --show-toplevel)" = "$(pwd -P)" &&
test -z "$(git symbolic-ref -q HEAD)" &&
mkdir -p deps && echo installed > deps/tool && echo "$BUG_COMMIT" > .prepared`)
	var stages []string
	e.agent = func(cfg Config, stage string) Agent {
		a.onExecute = func() error {
			for _, name := range []string{".prepared", "deps/tool"} {
				if _, err := os.Stat(filepath.Join(cfg.Directory, name)); err != nil {
					return fmt.Errorf("%s stage missing prerequisite: %w", stage, err)
				}
			}
			stages = append(stages, stage)
			return nil
		}
		return a
	}
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if runs() != 1 || !slices.Equal(stages, []string{"discovery", "review"}) || f.creates != 1 {
		t.Fatalf("setup runs %d, agent stages %v, creates %d", runs(), stages, f.creates)
	}
}

// Setup runs once per attempt, not per startup retry or review batch, and again
// when a later attempt resumes pending review.
func TestSetupRunsOncePerAttempt(t *testing.T) {
	e, s, f, a, _ := fixture(t)
	for i := range 25 {
		f.items = append(f.items, Issue{Number: i + 1, Title: "Unrelated", Body: "Unrelated report", State: "open"})
	}
	if len(reviewChunks(f.items)) < 2 {
		t.Fatal("fixture history does not need several review batches")
	}
	runs := setupCounter(t, &e, "true")
	calls := 0
	a.onExecute = func() error {
		calls++
		switch calls {
		case 1:
			return &runner.SetupError{Err: errors.New("exited before prompt")}
		case 3:
			return errors.New("review interrupted")
		}
		return nil
	}
	if err := e.step(context.Background(), s, true); err == nil || !strings.Contains(err.Error(), "review interrupted") {
		t.Fatalf("expected interrupted review, got %v", err)
	}
	if runs() != 1 || a.scans != 1 || s.Scan.Candidates[0].Status != "pending" {
		t.Fatalf("first attempt: setup runs %d, scans %d, scan %+v", runs(), a.scans, s.Scan)
	}
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if runs() != 2 || a.scans != 1 || a.reviews < 2 || f.creates != 1 {
		t.Fatalf("resumed attempt: setup runs %d, scans %d, reviews %d, creates %d", runs(), a.scans, a.reviews, f.creates)
	}
}

// A failed setup consumes the attempt, keeps pending findings unpublished, and
// waits the retry delay from when the failure completed.
func TestSetupFailureConsumesAttempt(t *testing.T) {
	e, s, f, a, _ := fixture(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.config.Poll = Duration(time.Minute)
	a.onExecute = func() error {
		if a.scans > 0 {
			return errors.New("review interrupted")
		}
		return nil
	}
	if err := e.step(context.Background(), s, true); err == nil {
		t.Fatal("expected interrupted review")
	}
	a.onExecute = nil
	runs := setupCounter(t, &e, "echo dependency install failed >&2; exit 7")
	// Setup takes 20 minutes of fake time; the delay starts when it fails.
	e.observe = func(p Progress) {
		if p.Task == setupTask {
			now = now.Add(20 * time.Minute)
		}
	}
	now = s.Scan.RetryAt
	scans, reviews := a.scans, a.reviews
	err := e.step(context.Background(), s, false)
	if err == nil || !strings.Contains(err.Error(), "workspace setup") || !strings.Contains(err.Error(), "dependency install failed") {
		t.Fatalf("expected setup failure, got %v", err)
	}
	if runs() != 1 || a.scans != scans || a.reviews != reviews || f.creates != 0 {
		t.Fatalf("agent or publication ran after setup failure: runs %d, scans %d, reviews %d, creates %d", runs(), a.scans, a.reviews, f.creates)
	}
	saved, err := ReadState(e.config)
	if err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(time.Duration(e.config.RetryDelay))
	if saved.Scan.Tries != 2 || !saved.Scan.RetryAt.Equal(deadline) || !strings.Contains(saved.Scan.Failure, "workspace setup") || saved.Scan.Candidates[0].Status != "pending" {
		t.Fatalf("saved scan after setup failure: %+v, want retry at %s", saved.Scan, deadline)
	}
	now = deadline.Add(-time.Nanosecond)
	if err := e.step(context.Background(), saved, false); err != nil || runs() != 1 {
		t.Fatalf("setup retried before deadline: runs %d, %v", runs(), err)
	}
}

// Timeout and cancellation kill the setup process tree and stop the attempt.
func TestSetupTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			e, s, _, a, _ := fixture(t)
			pidfile := filepath.Join(canonicalTestDir(t), "pid")
			e.config.Setup = []string{"sh", "-c", `sleep 60 & echo $! > "$0"; wait`, pidfile}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := context.Canceled
			if mode == "timeout" {
				e.config.Timeout = Duration(3 * time.Second)
				want = context.DeadlineExceeded
			} else {
				go func() {
					for ctx.Err() == nil {
						if raw, err := os.ReadFile(pidfile); err == nil && strings.HasSuffix(string(raw), "\n") {
							cancel()
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				}()
			}
			err := e.step(ctx, s, true)
			if !errors.Is(err, want) || !strings.Contains(err.Error(), "workspace setup") {
				t.Fatalf("expected %v from setup, got %v", want, err)
			}
			if a.scans != 0 || a.reviews != 0 {
				t.Fatal("agent ran after setup was stopped")
			}
			raw, err := os.ReadFile(pidfile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					syscall.Kill(pid, syscall.SIGKILL)
					t.Fatal("setup child process survived")
				}
			}
		})
	}
}

// Setup may add prerequisites but not move HEAD or edit tracked source.
func TestSetupSourceChangesRefused(t *testing.T) {
	for mode, script := range map[string]string{
		"edit":   "echo generated > README.md",
		"commit": "git -c user.name=Fixture -c user.email=fixture@example.com commit -q --allow-empty -m moved",
	} {
		t.Run(mode, func(t *testing.T) {
			e, s, f, a, _ := fixture(t)
			setupCounter(t, &e, script)
			err := e.step(context.Background(), s, true)
			if err == nil || !strings.Contains(err.Error(), "workspace setup changed the scan worktree") {
				t.Fatalf("expected source integrity failure, got %v", err)
			}
			if a.scans != 0 || f.creates != 0 || s.Scan.Tries != 1 {
				t.Fatalf("scans %d, creates %d, scan %+v", a.scans, f.creates, s.Scan)
			}
		})
	}
}

// Dry runs prepare the workspace; status, report, waiting polls and
// reconciliation-only completion do not.
func TestSetupExecutionPaths(t *testing.T) {
	t.Setenv("BUG_BOT_SETUP_SENTINEL", "sentinel-value")
	e, s, f, _, _ := fixture(t)
	runs := setupCounter(t, &e, `test -n "$BUG_BOT_SETUP_SENTINEL"`)
	var progress []Progress
	e.observe = func(p Progress) { progress = append(progress, p) }
	e.config.DryRun = true
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if runs() != 1 || f.creates != 0 || s.Completed[0].Status != "dry_run" {
		t.Fatalf("dry run: setup runs %d, creates %d", runs(), f.creates)
	}
	setupReported := false
	for _, p := range progress {
		if p.Phase == "preparing" && p.Task == setupTask {
			setupReported = true
		}
		if strings.Contains(p.Task+p.Failure, "sentinel-value") || strings.Contains(p.Task, p.Commit) && p.Commit != "" {
			t.Fatalf("progress displays environment values: %+v", p)
		}
	}
	if !setupReported {
		t.Fatal("progress does not identify workspace setup")
	}
	// A waiting poll before the next scan does not prepare a workspace.
	if err := e.step(context.Background(), s, false); err != nil || runs() != 1 {
		t.Fatalf("waiting poll ran setup: runs %d, %v", runs(), err)
	}
	if _, err := ReadState(e.config); err != nil {
		t.Fatal(err)
	}
	if err := Report(e.config, "", io.Discard); err != nil || runs() != 1 {
		t.Fatalf("status or report ran setup: runs %d, %v", runs(), err)
	}

	// Reconciling an uncertain publication completes the scan without setup.
	e, s, f, a, _ := fixture(t)
	runs = setupCounter(t, &e, "true")
	e.config.Attempts = 1
	f.lost = true
	if err := e.step(context.Background(), s, true); err == nil {
		t.Fatal("expected transport error")
	}
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if runs() != 1 || a.scans != 1 || s.Scan != nil || s.Completed[0].Status != "submitted" {
		t.Fatalf("reconciliation: setup runs %d, scans %d, scan %+v", runs(), a.scans, s.Scan)
	}
}

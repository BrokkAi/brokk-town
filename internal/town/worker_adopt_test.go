package town

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

type runOutcome struct {
	result RunResult
	err    error
}

// startFakeIssueRun launches the Python fake issue-bot in mode through
// BotWorkers.Run and returns once its durable handle is committed and the run
// stream has delivered its first event, so a cancellation issued afterwards
// reaches an accepted run rather than racing the run request.
func startFakeIssueRun(t *testing.T, ctx context.Context, mode string) (*BotWorkers, *Store, *Town, WorkerRun, <-chan runOutcome, string) {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	release := filepath.Join(dir, "release")
	writeFakeWorker(t, fake, pythonFakeWorker)
	t.Setenv("TOWN_WORKER_TEST_MODE", mode)
	t.Setenv("TOWN_WORKER_TEST_RELEASE", release)
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Branch = "main"
	x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Issue, Stage: "queued"}
	workers := &BotWorkers{Root: dir, Store: store, BotCommands: map[Role]string{Issue: fake}}
	done := make(chan runOutcome, 1)
	var streamed sync.Once
	streaming := make(chan struct{})
	go func() {
		result, err := workers.Run(ctx, x, Issue, func(p Progress) {
			if p.Seq > 0 {
				streamed.Do(func() { close(streaming) })
			}
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		done <- runOutcome{result, err}
	}()
	var run WorkerRun
	waitFor(t, 20*time.Second, func() bool {
		w := store.Snapshot().Towns[x.ID].Workers[Issue]
		if w.Run == nil {
			return false
		}
		run = *w.Run
		return true
	})
	select {
	case <-streaming:
	case outcome := <-done:
		t.Fatalf("run ended before streaming: %v", outcome.err)
	case <-time.After(20 * time.Second):
		t.Fatal("run stream never delivered an event")
	}
	t.Cleanup(func() { _ = osrun.KillGroup(run.PID) })
	return workers, store, x, run, done, release
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if !time.Now().Before(deadline) {
			t.Fatal("condition was not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServiceShutdownLeavesDetachableWorkerAndAdoptionResumesIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workers, store, x, run, done, release := startFakeIssueRun(t, ctx, "detach")
	if !run.Detachable || run.Issue != 7 || run.Bot != "issue-bot" || run.Version != "9.8.7" || run.Deadline.IsZero() {
		t.Fatalf("incomplete run handle: %+v", run)
	}
	// A plain cancellation is what service shutdown delivers.
	cancel()
	outcome := <-done
	if !errors.Is(outcome.err, errWorkerDetached) {
		t.Fatalf("shutdown should detach, got %v", outcome.err)
	}
	if !osrun.Alive(run.PID) {
		t.Fatal("shutdown killed the external bot process")
	}
	if _, err := os.Stat(run.Socket); err != nil {
		t.Fatalf("socket was removed during detach: %v", err)
	}
	if store.Snapshot().Towns[x.ID].Workers[Issue].Run == nil {
		t.Fatal("run handle was dropped on detach")
	}
	// Town observed progress event 1 before stopping; the replay starts after it.
	run.Seq = 1
	if err := os.WriteFile(release, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	replayed := []Progress{}
	result, err := workers.Adopt(context.Background(), x, Issue, run, func(p Progress) {
		mu.Lock()
		defer mu.Unlock()
		replayed = append(replayed, p)
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if owned := result.Owned[7]; owned.Branch != "town/7" || owned.Issue != 7 {
		t.Fatalf("adopted result lost ownership: %+v", result.Owned)
	}
	mu.Lock()
	if len(replayed) != 0 {
		t.Fatalf("attach replayed events Town had already observed: %+v", replayed)
	}
	mu.Unlock()
	waitFor(t, 10*time.Second, func() bool { return !osrun.Alive(run.PID) })
	if _, err := os.Stat(filepath.Dir(run.Socket)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adoption did not clean the run directory: %v", err)
	}
}

func TestAdoptingIdleWorkerShutsItDownAndReschedules(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	writeFakeWorker(t, fake, pythonFakeWorker)
	t.Setenv("TOWN_WORKER_TEST_MODE", "detach")
	store := testStore(t, false)
	x := addTown(t, store)
	workers := &BotWorkers{Root: dir, Store: store, BotCommands: map[Role]string{Issue: fake}}
	// Start a worker by hand and stop before any run request, which is what a
	// shutdown between committing the handle and posting the run leaves behind.
	bot, err := workers.externalBot(context.Background(), x.Config, Issue)
	if err != nil {
		t.Fatal(err)
	}
	var recorded WorkerRun
	blocked, cancel := context.WithCancel(context.Background())
	_, err = runWorker(blocked, bot, workerRequest{Protocol: 1, Issue: 7}, false, time.Now().Add(time.Hour), func(Progress) {}, func(run WorkerRun) error {
		recorded = run
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || errors.Is(err, errWorkerDetached) {
		t.Fatalf("cancellation before the run request must not detach: %v", err)
	}
	if osrun.Alive(recorded.PID) {
		t.Fatal("an idle worker was left behind")
	}
	// An adopted idle worker (the request was lost in flight) is shut down and
	// reported as never started, which reschedules the house without failure.

	socketDir, err := os.MkdirTemp("", "bt-worker-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDir, "worker.sock")
	output, err := os.Create(filepath.Join(socketDir, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := osrun.StartDetached("", []string{fake, "worker", "--socket", socket}, nil, output)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	output.Close()
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = osrun.KillGroup(cmd.Process.Pid) })

	run := WorkerRun{Bot: "issue-bot", Version: bot.version, Command: bot.command, Hash: bot.hash, PID: cmd.Process.Pid, Socket: socket, Output: filepath.Join(socketDir, "worker.log"), Detachable: true, Issue: 7, Started: time.Now(), Deadline: time.Now().Add(time.Hour)}
	_, err = workers.Adopt(context.Background(), x, Issue, run, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, errWorkerNeverStarted) || !errors.Is(err, context.Canceled) {
		t.Fatalf("idle worker should report never started as a cancellation: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool { return !osrun.Alive(cmd.Process.Pid) })
	if _, err := os.Stat(socketDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("idle worker directory was not cleaned")
	}
}

func TestOperatorStopKillsWorkerProcess(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	_, _, _, run, done, _ := startFakeIssueRun(t, ctx, "detach")
	cancel(errStopWorker)
	outcome := <-done
	if outcome.err == nil || errors.Is(outcome.err, errWorkerDetached) {
		t.Fatalf("stop must not detach: %v", outcome.err)
	}
	waitFor(t, 10*time.Second, func() bool { return !osrun.Alive(run.PID) })
	if _, err := os.Stat(filepath.Dir(run.Socket)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stop did not clean the run directory: %v", err)
	}
}

func TestAdoptStopAuthenticatesThenKillsWorkerProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	workers, _, x, run, done, _ := startFakeIssueRun(t, ctx, "detach")
	cancel()
	if outcome := <-done; !errors.Is(outcome.err, errWorkerDetached) {
		t.Fatalf("shutdown should leave the worker for adoption, got %v", outcome.err)
	}
	if !osrun.Alive(run.PID) {
		t.Fatal("shutdown killed the worker before adoption")
	}

	stopped, stop := context.WithCancelCause(context.Background())
	stop(errStopWorker)
	_, err := workers.Adopt(stopped, x, Issue, run, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("adopted stop should report cancellation, got %v", err)
	}
	waitFor(t, 10*time.Second, func() bool { return !osrun.Alive(run.PID) })
	if _, err := os.Stat(filepath.Dir(run.Socket)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adopted stop did not clean the run directory: %v", err)
	}
}

func TestAdoptStopDuringAuthenticationKillsWorkerProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	workers, _, town, run, done, release := startFakeIssueRun(t, ctx, "detach")
	cancel()
	if outcome := <-done; !errors.Is(outcome.err, errWorkerDetached) {
		t.Fatalf("shutdown should leave the worker for adoption, got %v", outcome.err)
	}
	initializeBlock := release + ".initialize-block"
	initializeStarted := initializeBlock + ".started"
	initializeRelease := initializeBlock + ".release"
	if err := os.WriteFile(initializeBlock, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}

	adoption, stop := context.WithCancelCause(context.Background())
	adopted := make(chan error, 1)
	go func() {
		_, err := workers.Adopt(adoption, town, Issue, run, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		adopted <- err
	}()
	waitFor(t, 10*time.Second, func() bool {
		_, err := os.Stat(initializeStarted)
		return err == nil
	})
	stop(errStopWorker)
	if err := os.WriteFile(initializeRelease, []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-adopted; !errors.Is(err, context.Canceled) {
		t.Fatalf("adopted stop should authenticate and report cancellation, got %v", err)
	}
	waitFor(t, 10*time.Second, func() bool { return !osrun.Alive(run.PID) })
	if _, err := os.Stat(filepath.Dir(run.Socket)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adopted stop did not clean the run directory: %v", err)
	}
}

func TestOnlyPlainCancellationDetaches(t *testing.T) {
	live := context.Background()
	if detachRequested(live) || stopRequested(live) {
		t.Fatal("a live context requests nothing")
	}
	shutdown, cancel := context.WithCancel(context.Background())
	cancel()
	if !detachRequested(shutdown) || stopRequested(shutdown) {
		t.Fatal("service shutdown must detach")
	}
	stopped, cancelCause := context.WithCancelCause(context.Background())
	cancelCause(errStopWorker)
	if detachRequested(stopped) || !stopRequested(stopped) {
		t.Fatal("an operator stop must kill")
	}
	expired, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if detachRequested(expired) || stopRequested(expired) {
		t.Fatal("a dispatch deadline must kill, not detach")
	}
	child, cancelChild := context.WithCancelCause(shutdown)
	defer cancelChild(nil)
	if !detachRequested(child) {
		t.Fatal("shutdown must propagate as detach through the dispatch context")
	}
}

func TestAdoptWithoutDetachWaitsForExitAndReportsUncertainOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workers, _, x, run, done, release := startFakeIssueRun(t, ctx, "hang")
	if run.Detachable {
		t.Fatal("v1 fixture must not advertise detach")
	}
	cancel()
	if outcome := <-done; !errors.Is(outcome.err, errWorkerDetached) {
		t.Fatalf("shutdown should detach a v1 worker too, got %v", outcome.err)
	}
	if !osrun.Alive(run.PID) {
		t.Fatal("shutdown killed the v1 bot process")
	}
	if err := os.WriteFile(release, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	observed := []Progress{}
	_, err := workers.Adopt(context.Background(), x, Issue, run, func(p Progress) { observed = append(observed, p) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var unknown *WorkerOutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected an uncertain outcome, got %v", err)
	}
	if !strings.Contains(err.Error(), "issue-bot 9.8.7") || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("uncertain outcome must name the bot: %v", err)
	}
	if len(observed) == 0 || observed[0].Phase != "waiting" {
		t.Fatalf("adoption should explain that it is waiting: %+v", observed)
	}
	waitFor(t, 10*time.Second, func() bool { return !osrun.Alive(run.PID) })
	if _, err := os.Stat(filepath.Dir(run.Socket)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adoption did not clean the run directory: %v", err)
	}
}

func TestAdoptReportsUncertainOutcomeWhenWorkerAlreadyExited(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t, false)
	x := addTown(t, store)
	workers := &BotWorkers{Root: dir, Store: store}
	runDir := filepath.Join(dir, "gone")
	if err := os.MkdirAll(runDir, 0700); err != nil {
		t.Fatal(err)
	}
	run := WorkerRun{Bot: "issue-bot", Version: "1.2.3", Command: "/bin/true", Hash: "x", PID: 1 << 30, Socket: filepath.Join(runDir, "worker.sock"), Output: filepath.Join(runDir, "worker.log"), Detachable: true, Issue: 7, Started: time.Now(), Deadline: time.Now().Add(time.Hour)}
	_, err := workers.Adopt(context.Background(), x, Issue, run, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var unknown *WorkerOutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected an uncertain outcome, got %v", err)
	}
	if _, err := os.Stat(runDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale run directory was not removed")
	}
}

func TestOpenKeepsRunHandlesForAdoption(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	x := addTown(t, s)
	handle := WorkerRun{Bot: "issue-bot", Version: "0.5.2", Command: "/usr/local/bin/npx", Args: []string{"--yes", "@brokkai/issue-bot@0.5.2"}, Hash: "abc", PID: 4242, Socket: "/tmp/bt-worker-1/worker.sock", Output: "/tmp/bt-worker-1/worker.log", Detachable: true, Seq: 3, Started: time.Now(), Deadline: time.Now().Add(time.Hour), Issue: 7}
	update(t, s, func(st *State) {
		w := st.Towns[x.ID].Workers[Issue]
		w.Enabled, w.Status, w.Run = true, "working", &handle
		b := st.Towns[x.ID].Workers[Bug]
		b.Enabled, b.Status = true, "working"
	})
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t2 := s.Snapshot().Towns[x.ID]
	w := t2.Workers[Issue]
	if w.Run == nil || w.Run.PID != 4242 || w.Run.Seq != 3 || w.Run.Socket != handle.Socket {
		t.Fatalf("run handle did not survive restart: %+v", w.Run)
	}
	if w.Status != "working" || w.Phase != "reconnecting" || !strings.Contains(w.Task, "issue-bot 0.5.2") {
		t.Fatalf("adoptable worker should show reconnecting: %+v", w)
	}
	if b := t2.Workers[Bug]; b.Status != "waiting" {
		t.Fatalf("worker without a handle should reset to waiting: %+v", b)
	}
}

// The repo house owns a bot process like any other, but its run names one
// repository observation: never an issue, a pull request or a revision.
func TestStateRejectsRepoRunHandleWithATarget(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	handle := func(run WorkerRun) error {
		return s.Update(func(st *State) error {
			st.Towns[x.ID].Workers[Repo].Run = &run
			return nil
		})
	}
	if err := handle(WorkerRun{Bot: "repo-bot", Version: "0.1.0", Command: "/x", PID: 1, Socket: "/s", Mode: "full"}); err != nil {
		t.Fatalf("a repository observation was rejected: %v", err)
	}
	if err := handle(WorkerRun{Bot: "repo-bot", Version: "0.1.0", Command: "/x", PID: 1, Socket: "/s", Mode: "full", PR: 7}); err == nil {
		t.Fatal("repo run handle accepted a pull request target")
	}
	if err := handle(WorkerRun{Bot: "repo-bot", Version: "0.1.0", Command: "/x", PID: 1, Socket: "/s"}); err == nil {
		t.Fatal("repo run handle accepted a run with no duty")
	}
}

// adoptingWorker records adoption calls and lets the test decide the outcome.
type adoptingWorker struct {
	workerFunc
	adopted     chan WorkerRun
	stops       chan bool
	outcome     error
	waitForStop bool
}

func (w *adoptingWorker) Adopt(ctx context.Context, _ *Town, _ Role, run WorkerRun, _ func(Progress), _ *slog.Logger) (RunResult, error) {
	w.adopted <- run
	if w.waitForStop {
		<-ctx.Done()
	}
	w.stops <- stopRequested(ctx)
	return RunResult{}, w.outcome
}

func TestSupervisorAdoptsPersistedRunsBeforeScheduling(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	handle := WorkerRun{Bot: "issue-bot", Version: "0.5.2", Command: "/usr/local/bin/npx", Hash: "abc", PID: 4242, Socket: "/tmp/bt-worker-2/worker.sock", Output: "/tmp/bt-worker-2/worker.log", Detachable: true, Started: time.Now(), Deadline: time.Now().Add(time.Hour), Issue: 7}
	update(t, s, func(st *State) {
		st.Towns[x.ID].Initialized = true
		w := st.Towns[x.ID].Workers[Issue]
		w.Enabled, w.Status, w.Run = true, "working", &handle
	})
	fresh := make(chan struct{}, 8)
	workers := &adoptingWorker{
		workerFunc: func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
			fresh <- struct{}{}
			return RunResult{}, nil
		},
		adopted: make(chan WorkerRun, 1), stops: make(chan bool, 1),
	}
	sup := NewSupervisor(s, nil, workers)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.adopt(ctx)
	select {
	case got := <-workers.adopted:
		if got.PID != 4242 || got.Issue != 7 {
			t.Fatalf("adopted the wrong handle: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not adopt the persisted run")
	}
	if <-workers.stops {
		t.Fatal("a live town's run must be resumed, not stopped")
	}
	waitFor(t, 5*time.Second, func() bool {
		w := s.Snapshot().Towns[x.ID].Workers[Issue]
		return w.Run == nil && w.Status == "waiting"
	})
	select {
	case <-fresh:
		t.Fatal("a fresh run was dispatched while a handle was outstanding")
	default:
	}
}

func TestSupervisorStopsPersistedReleaseRunUnderManualPolicy(t *testing.T) {
	store := testStore(t, false)
	town := addTown(t, store)
	handle := WorkerRun{Bot: "release-bot", Version: "0.5.1", Command: "/usr/local/bin/npx", Hash: "abc", PID: 4246, Socket: "/tmp/bt-worker-release/worker.sock", Output: "/tmp/bt-worker-release/worker.log", Detachable: true, Started: time.Now(), Deadline: time.Now().Add(time.Hour)}
	update(t, store, func(state *State) {
		current := state.Towns[town.ID]
		current.Config.MergePolicy = "manual"
		worker := current.Workers[Release]
		worker.Enabled, worker.Status, worker.Run = true, "working", &handle
		worker.Agent = &PublicBotAgentConfig{Harness: "codex", Model: "gpt-5"}
	})
	workers := &adoptingWorker{
		workerFunc: func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
			return RunResult{}, nil
		},
		adopted: make(chan WorkerRun, 1), stops: make(chan bool, 1),
	}
	supervisor := NewSupervisor(store, nil, workers)
	supervisor.adopt(context.Background())
	select {
	case got := <-workers.adopted:
		if got.Bot != "release-bot" {
			t.Fatalf("adopted wrong worker: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manual release run was left orphaned")
	}
	if !<-workers.stops {
		t.Fatal("persisted release run was resumed under manual policy")
	}
	supervisor.wg.Wait()
	worker := store.Snapshot().Towns[town.ID].Workers[Release]
	if worker.Run != nil || worker.Status != "paused" || worker.Agent != nil {
		t.Fatalf("stopped release worker retained active state: %+v", worker)
	}
}

func TestSupervisorRetainsPersistedRunWhenSafeStopFails(t *testing.T) {
	store := testStore(t, false)
	town := addTown(t, store)
	handle := WorkerRun{Bot: "release-bot", Version: "0.5.1", Command: "/usr/local/bin/npx", Hash: "abc", PID: 4247, Socket: "/tmp/bt-worker-release-failed/worker.sock", Output: "/tmp/bt-worker-release-failed/worker.log", Detachable: true, Started: time.Now(), Deadline: time.Now().Add(time.Hour)}
	update(t, store, func(state *State) {
		current := state.Towns[town.ID]
		current.Config.MergePolicy = "manual"
		worker := current.Workers[Release]
		worker.Enabled, worker.Status, worker.Run = true, "working", &handle
	})
	workers := &adoptingWorker{
		workerFunc: func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
			return RunResult{}, nil
		},
		adopted: make(chan WorkerRun, 1), stops: make(chan bool, 1), outcome: errors.New("identity probe failed"),
	}
	supervisor := NewSupervisor(store, nil, workers)
	supervisor.adopt(context.Background())
	supervisor.wg.Wait()
	worker := store.Snapshot().Towns[town.ID].Workers[Release]
	if worker.Run == nil || worker.Run.PID != handle.PID {
		t.Fatalf("failed safe stop discarded the durable run handle: %+v", worker)
	}
	if !strings.Contains(worker.Error, "Could not stop persisted release-bot safely") {
		t.Fatalf("failed safe stop was not exposed to the operator: %+v", worker)
	}
}

func TestSupervisorRetainsPersistedRunWhenPolicyChangesDuringAuthentication(t *testing.T) {
	store := testStore(t, false)
	town := addTown(t, store)
	handle := WorkerRun{Bot: "release-bot", Version: "0.5.1", Command: "/usr/local/bin/npx", Hash: "abc", PID: 4248, Socket: "/tmp/bt-worker-release-race/worker.sock", Output: "/tmp/bt-worker-release-race/worker.log", Detachable: true, Started: time.Now(), Deadline: time.Now().Add(time.Hour)}
	profile := &PublicBotAgentConfig{Harness: "codex", Model: "gpt-5"}
	update(t, store, func(state *State) {
		current := state.Towns[town.ID]
		current.Initialized = true
		current.Config.MergePolicy = "bot"
		worker := current.Workers[Release]
		worker.Enabled, worker.Status, worker.Run, worker.Agent = true, "working", &handle, profile
	})
	unknown := &WorkerOutcomeUnknownError{
		Bot: "release-bot", Version: "0.5.1", processRunning: true,
		Reason: "was still running when its stop could not be authenticated",
	}
	workers := &adoptingWorker{
		workerFunc: func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
			return RunResult{}, nil
		},
		adopted: make(chan WorkerRun, 1), stops: make(chan bool, 1), outcome: unknown, waitForStop: true,
	}
	supervisor := NewSupervisor(store, nil, workers)
	supervisor.adopt(context.Background())
	select {
	case <-workers.adopted:
	case <-time.After(5 * time.Second):
		t.Fatal("persisted release run did not begin authentication")
	}
	manual := "manual"
	if err := supervisor.SettingsForRoleAndPolicy(town.ID, "", AgentSettings{}, &manual, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !<-workers.stops {
		t.Fatal("manual policy did not request a stop during authentication")
	}
	supervisor.wg.Wait()
	worker := store.Snapshot().Towns[town.ID].Workers[Release]
	if worker.Run == nil || worker.Run.PID != handle.PID {
		t.Fatalf("unconfirmed stop discarded the durable run handle: %+v", worker)
	}
	if worker.Status != "paused" || worker.Agent == nil || !strings.Contains(worker.Error, "outcome is uncertain") {
		t.Fatalf("unconfirmed stop did not preserve visible uncertain state: %+v", worker)
	}
}

func TestSupervisorEndsOrphanOfDeletedTown(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	handle := WorkerRun{Bot: "bug-bot", Version: "0.3.1", Command: "/usr/local/bin/npx", Hash: "abc", PID: 4243, Socket: "/tmp/bt-worker-3/worker.sock", Output: "/tmp/bt-worker-3/worker.log", Started: time.Now(), Deadline: time.Now().Add(time.Hour)}
	update(t, s, func(st *State) {
		st.Towns[x.ID].Deleted = true
		w := st.Towns[x.ID].Workers[Bug]
		w.Enabled, w.Status, w.Run = false, "paused", &handle
	})
	workers := &adoptingWorker{adopted: make(chan WorkerRun, 1), stops: make(chan bool, 1)}
	sup := NewSupervisor(s, nil, workers)
	sup.adopt(context.Background())
	select {
	case <-workers.adopted:
	case <-time.After(5 * time.Second):
		t.Fatal("orphan of a deleted town was not handled")
	}
	if !<-workers.stops {
		t.Fatal("deleted town's orphan must be stopped")
	}
	waitFor(t, 5*time.Second, func() bool { return s.Snapshot().Towns[x.ID].Workers[Bug].Run == nil })
}

func TestSupervisorWithoutAdopterRecordsUncertainOutcome(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	handle := WorkerRun{Bot: "issue-bot", Version: "0.5.2", Command: "/usr/local/bin/npx", Hash: "abc", PID: 4244, Socket: "/tmp/bt-worker-4/worker.sock", Output: "/tmp/bt-worker-4/worker.log", Started: time.Now(), Deadline: time.Now().Add(time.Hour), Issue: 7}
	update(t, s, func(st *State) {
		st.Towns[x.ID].Initialized = true
		w := st.Towns[x.ID].Workers[Issue]
		w.Enabled, w.Status, w.Run = true, "working", &handle
	})
	sup := NewSupervisor(s, nil, workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		return RunResult{}, nil
	}))
	sup.adopt(context.Background())
	waitFor(t, 5*time.Second, func() bool {
		w := s.Snapshot().Towns[x.ID].Workers[Issue]
		return w.Run == nil && w.Status == "failed" && strings.Contains(w.Error, "uncertain")
	})
}

func TestShutdownDuringRunKeepsHandleAndMarksDetached(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		st.Towns[x.ID].Initialized = true
		st.Towns[x.ID].Workers[Issue].Enabled = true
	})
	started := make(chan struct{})
	workers := workerFunc(func(ctx context.Context, town *Town, role Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		// Mirror runWorker: commit the handle, then detach on a plain cancel.
		_ = s.Update(func(st *State) error {
			st.Towns[town.ID].Workers[role].Run = &WorkerRun{Bot: "issue-bot", Version: "0.5.2", Command: "/usr/local/bin/npx", Hash: "abc", PID: 4245, Socket: "/tmp/bt-worker-5/worker.sock", Output: "/tmp/bt-worker-5/worker.log", Detachable: true, Started: time.Now(), Deadline: time.Now().Add(time.Hour), Issue: 7}
			return nil
		})
		close(started)
		<-ctx.Done()
		if detachRequested(ctx) {
			return RunResult{}, errWorkerDetached
		}
		return RunResult{}, ctx.Err()
	})
	sup := NewSupervisor(s, nil, workers)
	ctx, cancel := context.WithCancel(context.Background())
	sup.mu.Lock()
	sup.dispatch(ctx, s.Snapshot().Towns[x.ID], Issue, x.ID+":issue", nil)
	sup.mu.Unlock()
	<-started
	cancel()
	sup.wg.Wait()
	w := s.Snapshot().Towns[x.ID].Workers[Issue]
	if w.Run == nil || w.Status != "detached" {
		t.Fatalf("shutdown must keep the handle and mark the worker detached: %+v", w)
	}
}

func TestOperatorStopCancelsWithStopCause(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		st.Towns[x.ID].Initialized = true
		st.Towns[x.ID].Workers[Bug].Enabled = true
	})
	started := make(chan struct{})
	causes := make(chan bool, 1)
	workers := workerFunc(func(ctx context.Context, _ *Town, _ Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		close(started)
		<-ctx.Done()
		causes <- stopRequested(ctx)
		return RunResult{}, ctx.Err()
	})
	sup := NewSupervisor(s, nil, workers)
	sup.mu.Lock()
	sup.dispatch(context.Background(), s.Snapshot().Towns[x.ID], Bug, x.ID+":bug", nil)
	sup.mu.Unlock()
	<-started
	if err := sup.Control(x.ID, Bug, "stop", ""); err != nil {
		t.Fatal(err)
	}
	if !<-causes {
		t.Fatal("operator stop must cancel with the stop cause so the process is killed")
	}
	sup.wg.Wait()
}

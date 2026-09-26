package town

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

func TestWorkerCancellationEventKeepsItsDetail(t *testing.T) {
	result, err := consumeWorkerEvents(strings.NewReader("{\"type\":\"canceled\",\"seq\":1,\"error\":\"fixture shutdown\"}\n"), 0, func(Progress) {})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "fixture shutdown") || result.terminal {
		t.Fatalf("lost cancellation evidence: err=%v terminal=%v", err, result.terminal)
	}
}

func TestWorkerCancellationCauseAndPhaseSurviveRestart(t *testing.T) {
	for _, scenario := range []struct {
		name, want string
		cause      error
	}{
		{"operator stop", "operator stop", nil},
		{"service signal", "terminated signal received", errors.New("terminated signal received")},
		{"unknown service cancellation", "cause was not provided", context.Canceled},
		{"worker canceled", "fixture worker shutdown", nil},
		{"redacted service cause", "[redacted]", errors.New("service failed with bearer abcdefghijklmnopqrstuvwxyz")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := testStore(t, false)
			x := addTown(t, s)
			update(t, s, func(st *State) {
				x := st.Towns[x.ID]
				x.Initialized = true
				x.Workers[Repo].Enabled = false
				x.Workers[Issue].Enabled = true
			})
			entered := make(chan struct{})
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			worker := workerFunc(func(ctx context.Context, _ *Town, _ Role, p func(Progress), _ *slog.Logger) (RunResult, error) {
				p(Progress{Phase: "inventorying", Task: "Reading fixture"})
				close(entered)
				if scenario.name == "worker canceled" {
					_, err := consumeWorkerEvents(strings.NewReader("{\"type\":\"canceled\",\"seq\":1,\"error\":\"fixture worker shutdown\"}\n"), 0, p)
					return RunResult{}, &WorkerInterruptedError{err}
				}
				<-ctx.Done()
				return RunResult{}, &WorkerInterruptedError{ctx.Err()}
			})
			sup := NewSupervisor(s, newGH(1), worker)
			sup.schedule(ctx)
			<-entered
			if scenario.name == "operator stop" {
				if err := sup.Control(x.ID, Issue, "stop", ""); err != nil {
					t.Fatal(err)
				}
			} else if scenario.cause != nil {
				cancel(scenario.cause)
			}
			sup.wg.Wait()
			check := func(st State) {
				t.Helper()
				x := st.Towns[x.ID]
				logs := x.Workers[Issue].Logs
				if len(logs) != 1 || !strings.Contains(logs[0].Text, scenario.want) || !strings.Contains(logs[0].Text, "inventorying") {
					t.Fatalf("cancellation log lost cause or phase: %+v", logs)
				}
				found := false
				for _, o := range x.Outcomes {
					if o.Kind == "worker_attempt" {
						found = true
						if o.Status != "abandoned" || !strings.Contains(o.Detail, scenario.want) || !strings.Contains(o.Detail, "inventorying") {
							t.Fatalf("cancellation outcome lost evidence: %+v", o)
						}
					}
				}
				if !found {
					t.Fatal("missing canceled attempt")
				}
				public, err := json.Marshal(st.Public())
				if err != nil || strings.Contains(string(public), "abcdefghijklmnopqrstuvwxyz") {
					t.Fatal("cancellation cause leaked credentials", err)
				}
			}
			check(s.Snapshot())
			dir := filepath.Dir(s.path)
			s.Close()
			reopened, err := Open(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			check(reopened.Snapshot())
		})
	}
}

func TestSupervisorFailureReachesCanceledWorker(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		x := st.Towns[x.ID]
		x.Initialized = true
		x.Workers[Repo].Enabled = false
		x.Workers[Issue].Enabled = true
	})
	entered := make(chan struct{})
	worker := workerFunc(func(ctx context.Context, _ *Town, _ Role, p func(Progress), _ *slog.Logger) (RunResult, error) {
		p(Progress{Phase: "checking"})
		close(entered)
		<-ctx.Done()
		return RunResult{}, ctx.Err()
	})
	sup := NewSupervisor(s, newGH(1), worker)
	sup.Mjolnir = mjolnir.New(t.TempDir(), true, mjolnir.Connection{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	<-entered
	failure := errors.New("fixture state write failed")
	sup.fail(failure)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatal("supervisor lost its failure", err)
	}
	w := s.Snapshot().Towns[x.ID].Workers[Issue]
	if len(w.Logs) != 1 || !strings.Contains(w.Logs[0].Text, failure.Error()) {
		t.Fatalf("supervisor failure discarded during worker cleanup: %+v", w.Logs)
	}
}

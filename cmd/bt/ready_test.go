package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startWithReadyPipe runs a stand-in child holding the notification pipe as
// descriptor 3, and closes the parent's write end as startBackground does.
func startWithReadyPipe(t *testing.T, script string) (*exec.Cmd, *os.File) {
	t.Helper()
	ready, notify, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ready.Close() })
	cmd := exec.Command("sh", "-c", script)
	cmd.ExtraFiles = []*os.File{notify}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	notify.Close()
	return cmd, ready
}

func TestAwaitReadyReturnsWhenTheChildSignals(t *testing.T) {
	cmd, ready := startWithReadyPipe(t, "echo ready >&3; exec 3>&-; sleep 5")
	defer cmd.Process.Kill()
	if err := awaitReady(context.Background(), cmd, ready); err != nil {
		t.Fatal(err)
	}
}

// A child that dies during startup is reported at once, not after a timeout.
func TestAwaitReadyReportsAFailedStartImmediately(t *testing.T) {
	cmd, ready := startWithReadyPipe(t, "exit 3")
	start := time.Now()
	err := awaitReady(context.Background(), cmd, ready)
	if err == nil || !strings.Contains(err.Error(), "exited during startup") || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("failed start took %s", time.Since(start))
	}
}

func TestAwaitReadyStopsTheChildOnCancel(t *testing.T) {
	cmd, ready := startWithReadyPipe(t, "sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := awaitReady(ctx, cmd, ready); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("child was not reaped")
	}
}

func TestTakeReadyPipeClearsTheEnvironment(t *testing.T) {
	t.Setenv(readyEnv, "not-a-descriptor")
	if f := takeReadyPipe(); f != nil {
		t.Fatal("accepted a malformed descriptor")
	}
	if _, ok := os.LookupEnv(readyEnv); ok {
		t.Fatal("left the descriptor in the environment for subprocesses")
	}
}

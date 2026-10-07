package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// readyChildEnv selects a stand-in child's behavior in TestReadyChild.
const readyChildEnv = "SCS_READY_TEST_CHILD"

// TestReadyChild is the stand-in child, run by startWithReadyPipe as this test
// binary. It takes the notification pipe as Town does.
func TestReadyChild(t *testing.T) {
	switch os.Getenv(readyChildEnv) {
	case "":
		t.Skip("stand-in child for the ready pipe tests")
	case "ready":
		readyPipe = takeReadyPipe()
		if readyPipe == nil {
			os.Exit(2)
		}
		signalReady()
		time.Sleep(5 * time.Second)
	case "fail":
		os.Exit(3)
	case "sleep":
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

// startWithReadyPipe runs a stand-in child holding the notification pipe as
// scs -d passes it, and closes the parent's write end as startBackground does.
func startWithReadyPipe(t *testing.T, mode string) (*exec.Cmd, *os.File) {
	t.Helper()
	ready, notify, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ready.Close() })
	cmd := exec.Command(os.Args[0], "-test.run=^TestReadyChild$")
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	cmd.Env = append(os.Environ(), readyChildEnv+"="+mode, readyEnv+"="+passReady(cmd, notify))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	notify.Close()
	return cmd, ready
}

func TestAwaitReadyReturnsWhenTheChildSignals(t *testing.T) {
	cmd, ready := startWithReadyPipe(t, "ready")
	defer cmd.Process.Kill()
	if err := awaitReady(context.Background(), cmd, ready); err != nil {
		t.Fatal(err)
	}
}

// A child that dies during startup is reported at once, not after a timeout.
func TestAwaitReadyReportsAFailedStartImmediately(t *testing.T) {
	cmd, ready := startWithReadyPipe(t, "fail")
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
	cmd, ready := startWithReadyPipe(t, "sleep")
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

// Only a pipe is taken as the notification descriptor.
func TestTakeReadyPipeAcceptsOnlyAPipe(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-pipe")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	t.Setenv(readyEnv, strconv.FormatUint(uint64(file.Fd()), 10))
	if f := takeReadyPipe(); f != nil {
		t.Fatal("took a regular file as the notification descriptor")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	// Production receives a raw inherited descriptor with no os.File owner.
	// Duplicate here: two os.File wrappers around w's same descriptor let an
	// unreachable wrapper's finalizer close an unrelated socket after fd reuse.
	value, release := duplicate(t, w)
	t.Setenv(readyEnv, value)
	f := takeReadyPipe()
	if f == nil {
		release()
		t.Fatal("refused a pipe")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("still owned"); err != nil {
		t.Fatalf("claim closed the original pipe: %v", err)
	}
	data := make([]byte, len("still owned"))
	if _, err := r.Read(data); err != nil || string(data) != "still owned" {
		t.Fatal(string(data), err)
	}
}

// A failed start reports only what it wrote, not errors earlier runs left in
// the appended log.
func TestLogTailShowsOnlyThisStart(t *testing.T) {
	dir := t.TempDir()
	_, stderr := logPaths(dir)
	if err := os.MkdirAll(filepath.Dir(stderr), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stderr, []byte("scs: an old failure\n"), 0600); err != nil {
		t.Fatal(err)
	}
	offset := logSize(dir)
	if got := logTail(dir, offset); got != "" {
		t.Fatalf("a start that wrote nothing reported %q", got)
	}
	f, err := os.OpenFile(stderr, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("scs: this failure\n")
	f.Close()
	got := logTail(dir, offset)
	if !strings.Contains(got, "this failure") || strings.Contains(got, "old failure") {
		t.Fatalf("tail = %q", got)
	}
}

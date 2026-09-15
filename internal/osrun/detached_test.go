package osrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetachedProcessSurvivesContextAndWritesToFile(t *testing.T) {
	dir := t.TempDir()
	output, err := os.Create(filepath.Join(dir, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	// A cancelled context must mean nothing to a detached worker.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx
	cmd := StartDetached("", []string{"python3", "-c", "import sys, time; print('detached ok', flush=True); sys.stderr.write('err ok\\n'); sys.stderr.flush(); time.sleep(30)"}, nil, output)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	output.Close()
	pid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = KillGroup(pid) })
	deadline := time.Now().Add(10 * time.Second)
	for {
		text, _ := TailFile(filepath.Join(dir, "worker.log"), 1<<10)
		if strings.Contains(text, "detached ok") && strings.Contains(text, "err ok") {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("detached output did not reach the file: %q", text)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !Alive(pid) {
		t.Fatal("detached process is not alive")
	}
	select {
	case err := <-done:
		t.Fatalf("detached process exited early: %v", err)
	default:
	}
	if err = KillGroup(pid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("kill did not end the detached process group")
	}
	if Alive(pid) {
		t.Fatal("process reported alive after it was reaped")
	}
	if !Alive(os.Getpid()) {
		t.Fatal("the current process must report alive")
	}
}

func TestTailFileBoundsAndMissing(t *testing.T) {
	if text, cut := TailFile(filepath.Join(t.TempDir(), "missing"), 16); text != "" || cut {
		t.Fatalf("missing file should read empty: %q %v", text, cut)
	}
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 40)+"tail"), 0600); err != nil {
		t.Fatal(err)
	}
	text, cut := TailFile(path, 8)
	if text != "aaaatail" || !cut {
		t.Fatalf("tail = %q cut=%v", text, cut)
	}
}

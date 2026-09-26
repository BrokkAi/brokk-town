package worker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func lifecycleClient(socket string) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, DisableKeepAlives: true}}
}
func lifecycleReady(t *testing.T, client *http.Client) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		r, e := client.Get("http://worker/v1/initialize")
		if e == nil {
			r.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not initialize")
}
func lifecycleInfo() Initialize {
	return Initialize{Protocol: 1, MinimumProtocol: 1, Bot: "test-bot", Version: "1.0.0"}
}

func TestWorkerReusesProcessRejectsOverlapAndCancelsActiveRun(t *testing.T) {
	dir, err := os.MkdirTemp("", "worker-life-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "w.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan int32, 3)
	release := make(chan struct{})
	var calls atomic.Int32
	run := func(ctx context.Context, _ Request, _ func(Progress)) (Result, error) {
		n := calls.Add(1)
		entered <- n
		if n == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		}
		if n == 3 {
			<-ctx.Done()
			return Result{}, ctx.Err()
		}
		return Result{}, nil
	}
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, socket, lifecycleInfo(), run, nil, nil) }()
	client := lifecycleClient(socket)
	defer client.CloseIdleConnections()
	lifecycleReady(t, client)
	post := func() int {
		r, e := client.Post("http://worker/v1/runs", "application/json", strings.NewReader(`{"protocol":1}`))
		if e != nil {
			return 0
		}
		defer r.Body.Close()
		io.Copy(io.Discard, r.Body)
		return r.StatusCode
	}
	result := make(chan int, 1)
	go func() { result <- post() }()
	if n := <-entered; n != 1 {
		t.Fatal(n)
	}
	if status := post(); status != http.StatusConflict {
		t.Fatalf("overlap status=%d", status)
	}
	close(release)
	if status := <-result; status != http.StatusOK {
		t.Fatal(status)
	}
	if status := post(); status != http.StatusOK {
		t.Fatal(status)
	}
	if n := <-entered; n != 2 {
		t.Fatal(n)
	}
	go func() { result <- post() }()
	if n := <-entered; n != 3 {
		t.Fatal(n)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not cancel active work")
	}
	<-result
}
func TestWorkerParentSocketChild(t *testing.T) {
	socket := os.Getenv("WORKER_LIFECYCLE_CHILD_SOCKET")
	if socket == "" {
		return
	}
	err := Serve(context.Background(), socket, lifecycleInfo(), func(context.Context, Request, func(Progress)) (Result, error) { return Result{}, nil }, nil, nil)
	if err != nil && err != context.Canceled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
func TestWorkerExitsWhenParentSocketCloses(t *testing.T) {
	dir, err := os.MkdirTemp("", "worker-parent-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	parentPath := filepath.Join(dir, "parent.sock")
	listener, err := net.Listen("unix", parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.(*net.UnixListener).SetDeadline(time.Now().Add(3 * time.Second))
	socket := filepath.Join(dir, "w.sock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerParentSocketChild$")
	cmd.Env = append(os.Environ(), "BROKK_TOWN_PARENT_SOCKET="+parentPath, "WORKER_LIFECYCLE_CHILD_SOCKET="+socket)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	client := lifecycleClient(socket)
	defer client.CloseIdleConnections()
	lifecycleReady(t, client)
	parent, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	parent.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker survived its parent")
	}
}

func TestWorkerExitsWhenParentPipeCloses(t *testing.T) {
	dir, err := os.MkdirTemp("", "worker-parent-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	socket := filepath.Join(dir, "w.sock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerParentSocketChild$")
	cmd.Env = append(os.Environ(), "BROKK_TOWN_PARENT_PIPE=1", "WORKER_LIFECYCLE_CHILD_SOCKET="+socket)
	cmd.ExtraFiles = []*os.File{r}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.Close()
	defer cmd.Process.Kill()
	client := lifecycleClient(socket)
	defer client.CloseIdleConnections()
	lifecycleReady(t, client)
	w.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker survived its parent")
	}
}

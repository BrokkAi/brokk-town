package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/BrokkAi/repo-bot/internal/worker"
)

// startWorker serves the worker on a socket under a short temporary directory:
// t.TempDir() embeds the test name and overflows the 104-byte macOS socket
// path limit.
func startWorker(t *testing.T, version string) (*http.Client, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "brp-worker-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "worker.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- workerCommand(ctx, []string{"--socket", socket}, version) }()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	stop := func() {
		client.CloseIdleConnections()
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not stop")
		}
		_ = os.RemoveAll(dir)
	}
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		select {
		case err := <-done:
			cancel()
			_ = os.RemoveAll(dir)
			t.Fatalf("worker exited before serving: %v", err)
		default:
		}
		response, err := client.Get("http://worker/v1/initialize")
		if err == nil {
			response.Body.Close()
			return client, stop
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	t.Fatal("worker not ready")
	return nil, nil
}

func initialize(t *testing.T, client *http.Client) worker.Initialize {
	t.Helper()
	response, err := client.Get("http://worker/v1/initialize")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var info worker.Initialize
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestWorkerCommandRequiresSocket(t *testing.T) {
	ctx := context.Background()
	if err := workerCommand(ctx, nil, "v1.0.0"); err == nil {
		t.Fatal("worker without --socket accepted")
	}
	if err := workerCommand(ctx, []string{"--socket", filepath.Join(t.TempDir(), "worker.sock"), "extra"}, "v1.0.0"); err == nil {
		t.Fatal("worker with a positional argument accepted")
	}
}

func TestWorkerInitializeReportsIdentity(t *testing.T) {
	client, stop := startWorker(t, "v1.0.0")
	defer stop()
	info := initialize(t, client)
	if info.Bot != "repo-bot" || info.Version != "v1.0.0" {
		t.Fatalf("identity %+v", info)
	}
	for _, want := range []string{"policy", "run", "progress", "repo-inventory", "branch-health"} {
		if !slices.Contains(info.Capabilities, want) {
			t.Fatalf("capabilities %v miss %q", info.Capabilities, want)
		}
	}
}

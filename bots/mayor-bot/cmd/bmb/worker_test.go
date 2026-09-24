package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/mayor-bot/internal/worker"
)

// startWorker serves the worker on a socket under a short temporary directory:
// t.TempDir() embeds the test name and overflows the 104-byte macOS socket
// path limit.
func startWorker(t *testing.T, version string) (*http.Client, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "bmb-worker-")
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
	if info.Bot != "mayor-bot" || info.Version != "v1.0.0" {
		t.Fatalf("identity %+v", info)
	}
	for _, want := range []string{"policy", "run", "progress", "mayor-judgment", "mayor-bulletin"} {
		if !slices.Contains(info.Capabilities, want) {
			t.Fatalf("capabilities %v miss %q", info.Capabilities, want)
		}
	}
}

func TestWorkerRejectsUnknownModeWithoutRunningAnAgent(t *testing.T) {
	client, stop := startWorker(t, "v1.0.0")
	defer stop()
	body, _ := json.Marshal(worker.Request{Protocol: worker.ProtocolVersion, Mode: "bogus"})
	response, err := client.Post("http://worker/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	failure := ""
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		var event worker.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "error" {
			failure = event.Error
		}
		if event.Result != nil {
			t.Fatal("unknown mode produced a result")
		}
	}
	if scanner.Err() != nil {
		t.Fatal(scanner.Err())
	}
	if !strings.Contains(failure, "judge or bulletin") {
		t.Fatalf("unknown mode error %q", failure)
	}
}

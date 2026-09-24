package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/BrokkAi/repo-bot/internal/worker"
)

func TestWorkerCommandRequiresSocket(t *testing.T) {
	ctx := context.Background()
	if err := workerCommand(ctx, nil, "v1.0.0"); err == nil {
		t.Fatal("worker without --socket accepted")
	}
	socket := filepath.Join(t.TempDir(), "worker.sock")
	if err := workerCommand(ctx, []string{"--socket", socket, "extra"}, "v1.0.0"); err == nil {
		t.Fatal("worker with a positional argument accepted")
	}
}

func TestWorkerInitializeReportsIdentity(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "worker.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- workerCommand(ctx, []string{"--socket", socket}, "v1.0.0") }()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer client.CloseIdleConnections()
	var info worker.Initialize
	ready := false
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		response, err := client.Get("http://worker/v1/initialize")
		if err == nil {
			decodeErr := json.NewDecoder(response.Body).Decode(&info)
			response.Body.Close()
			if decodeErr == nil {
				ready = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("worker not ready")
	}
	if info.Bot != "repo-bot" || info.Version != "v1.0.0" {
		t.Fatalf("identity %+v", info)
	}
	for _, want := range []string{"policy", "run", "progress", "repo-inventory", "branch-health"} {
		found := false
		for _, capability := range info.Capabilities {
			if capability == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("capabilities %v miss %q", info.Capabilities, want)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}

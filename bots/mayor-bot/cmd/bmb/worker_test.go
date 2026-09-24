package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/mayor-bot/internal/worker"
)

func startWorker(t *testing.T, version string) (*http.Client, func()) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "worker.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- workerCommand(ctx, []string{"--socket", socket}, version) }()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	ready := false
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		response, err := client.Get("http://worker/v1/initialize")
		if err == nil {
			response.Body.Close()
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("worker not ready")
	}
	return client, func() {
		client.CloseIdleConnections()
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not stop")
		}
	}
}

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
	client, stop := startWorker(t, "v1.0.0")
	defer stop()
	response, err := client.Get("http://worker/v1/initialize")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var info worker.Initialize
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Bot != "mayor-bot" || info.Version != "v1.0.0" {
		t.Fatalf("identity %+v", info)
	}
	for _, want := range []string{"policy", "run", "progress", "mayor-judgment", "mayor-bulletin"} {
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

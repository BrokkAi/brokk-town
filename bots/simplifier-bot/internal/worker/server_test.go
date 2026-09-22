package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeAssessmentRun(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "worker.sock")
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		close(ready)
		err := Serve(ctx, socket, Initialize{Protocol: 1, MinimumProtocol: 1, Bot: "simplifier-bot", Version: "dev"}, func(context.Context, Request, func(Progress)) (Result, error) {
			return Result{Simplification: &SimplificationResult{Mode: "suggest", Decision: "admit", Detail: "Focused change."}}, nil
		}, nil)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("serve: %v", err)
			}
		}
	}()
	<-ready
	var conn net.Conn
	for attempt := 0; attempt < 100; attempt++ {
		var err error
		conn, err = net.Dial("unix", socket)
		if err == nil {
			break
		}
	}
	if conn == nil {
		t.Fatal("worker socket never opened")
	}
	defer conn.Close()
	body := `{"protocol":1,"remote":"https://example.invalid/a.git","branch":"main","directory":"/tmp/a","state_directory":"/tmp/s","repo":"a/b","host":"example.invalid","agent":{"command":["fake"]},"issue":7,"mode":"suggest"}`
	request, _ := http.NewRequest(http.MethodPost, "http://worker/v1/runs", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil }, DisableKeepAlives: true}}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	var kinds []string
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, event.Type)
		if event.Type == "result" && (event.Result.Simplification == nil || event.Result.Simplification.Decision != "admit") {
			t.Fatalf("missing assessment: %+v", event)
		}
	}
	if strings.Join(kinds, ",") != "result,complete" {
		t.Fatalf("events = %v", kinds)
	}
}

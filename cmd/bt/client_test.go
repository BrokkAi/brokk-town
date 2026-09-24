package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeService advertises a stand-in Town in dir and records every request
// other than the liveness probe.
func fakeService(t *testing.T, state string) (string, chan string) {
	t.Helper()
	seen := make(chan string, 8)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" && r.Method == http.MethodGet {
			_, _ = w.Write([]byte(state))
			return
		}
		seen <- r.Method + " " + r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(h.Close)
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid(), Version: "test"})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, seen
}

func TestStatusAndShutdownWithoutTown(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := run(ctx, []string{"status", "--state-dir", dir}); err != nil {
		t.Fatalf("status of a stopped Town: %v", err)
	}
	if err := run(ctx, []string{"status", "--state-dir", dir, "--json"}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("status --json of a stopped Town: %v", err)
	}
	if err := run(ctx, []string{"shutdown", "--state-dir", dir}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("shutdown of a stopped Town: %v", err)
	}
}

func TestStatusSummarizesRunningTown(t *testing.T) {
	dir, seen := fakeService(t, `{"capacity":{"active":1,"limit":4},"towns":{"acme/app":{"workers":{"bug":{"enabled":true},"issue":{"enabled":false}}},"acme/gone":{"deleted":true}}}`)
	if err := run(context.Background(), []string{"status", "--state-dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"status", "--state-dir", dir, "--json"}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-seen:
		t.Fatalf("status made a request beyond the state read: %s", got)
	default:
	}
}

// The registry catalog is local data; listing it needs no running Town.
func TestHarnessesListWithoutTown(t *testing.T) {
	if err := run(context.Background(), []string{"harnesses", "--state-dir", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestRequestCheckPostsToCheckEndpoint(t *testing.T) {
	dir, seen := fakeService(t, `{}`)
	if err := run(context.Background(), []string{"request", "--state-dir", dir, "--repo", "Acme/App", "--check", "--request-id", "abc"}); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "POST /api/requests/check" {
		t.Fatalf("request --check sent %s", got)
	}
	for _, args := range [][]string{
		{"request", "--state-dir", dir, "--repo", "acme/app", "--check"},
		{"request", "--state-dir", dir, "--repo", "acme/app", "--check", "--request-id", "abc", "--title", "x"},
	} {
		if err := run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "--check") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestDeleteAlwaysTargetsTheWholeTown(t *testing.T) {
	var body map[string]string
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/control" {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"delete", "--state-dir", dir, "--repo", "acme/app"}); err != nil {
		t.Fatal(err)
	}
	if body["action"] != "delete" || body["role"] != "all" {
		t.Fatalf("delete sent %+v", body)
	}
}

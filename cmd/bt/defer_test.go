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
	"time"
)

func TestDeferUntilAcceptsTimestampsAndDelays(t *testing.T) {
	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	for value, want := range map[string]time.Time{
		"2026-09-24T08:30:00Z":      time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC),
		"2026-09-24T10:30:00+02:00": time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC),
		"90m":                       now.Add(90 * time.Minute),
		"4h30m":                     now.Add(4*time.Hour + 30*time.Minute),
		"2d":                        now.Add(48 * time.Hour),
	} {
		got, err := deferUntil(value, now)
		if err != nil || !got.Equal(want) {
			t.Fatalf("%s: got %s, %v; want %s", value, got, err, want)
		}
	}
	for _, value := range []string{"", "tomorrow", "-2h", "0s", "0d", "2026-09-24"} {
		if _, err := deferUntil(value, now); err == nil || !strings.Contains(err.Error(), "--until") {
			t.Fatalf("%q was accepted: %v", value, err)
		}
	}
}

func TestDeferCLIPostsTheSnoozeAndClearsIt(t *testing.T) {
	seen := make(chan map[string]string, 2)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" && r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/control" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		seen <- body
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if err := run(context.Background(), []string{"defer", "--state-dir", dir, "--repo", "Acme/Orchard", "--task", "issue:4", "--until", "2h", "--reason", "vendor fix"}); err != nil {
		t.Fatal(err)
	}
	got := <-seen
	until, err := time.Parse(time.RFC3339, got["until"])
	if err != nil || got["town"] != "acme/orchard" || got["action"] != "defer" || got["task"] != "issue:4" || got["reason"] != "vendor fix" {
		t.Fatalf("defer sent %v (%v)", got, err)
	}
	if until.Before(before.Add(2*time.Hour-time.Second)) || until.After(time.Now().Add(2*time.Hour)) {
		t.Fatalf("a 2h snooze resumes at %s", until)
	}
	if err := run(context.Background(), []string{"undefer", "--state-dir", dir, "--repo", "acme/orchard", "--task", "issue:4"}); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got["action"] != "undefer" || got["task"] != "issue:4" || got["until"] != "" {
		t.Fatalf("undefer sent %v", got)
	}
	for _, args := range [][]string{
		{"defer", "--state-dir", dir, "--repo", "acme/orchard", "--until", "2h"},
		{"defer", "--state-dir", dir, "--repo", "acme/orchard", "--task", "issue:4"},
		{"undefer", "--state-dir", dir, "--repo", "acme/orchard", "--task", "issue:4", "--until", "2h"},
	} {
		if err := run(context.Background(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

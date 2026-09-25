package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorUsesServiceSettingsAndPrintsUnknown(t *testing.T) {
	posts := 0
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authentication")
		}
		if r.URL.Path == "/api/state" {
			_, _ = w.Write([]byte(`{"towns":{}}`))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/diagnostics" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input) != 1 || input["town"] != "acme/app" {
			t.Error(input)
		}
		posts++
		_, _ = w.Write([]byte(`{"at":"2026-09-25T10:00:00Z","checks":[{"code":"agent_auth","role":"review","status":"unknown","detail":"Authentication not checked.","action":"Use the harness status command."}]}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "doctor")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()
	for _, jsonFlag := range []bool{false, true} {
		args := []string{"doctor", "--repo", "acme/app", "--state-dir", dir}
		if jsonFlag {
			args = append(args, "--json")
		}
		if err := run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"review / agent_auth [unknown]", "Next: Use the harness status command.", `"status": "unknown"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatal("missing result", want, string(raw))
		}
	}
	if posts != 2 {
		t.Fatal("wrong number of diagnostics", posts)
	}
}

func TestStatusPrintsSavedMergeBlockerAndRevision(t *testing.T) {
	dir, _ := fakeService(t, `{"towns":{"acme/app":{"workers":{},"tasks":{"pr:7":{"stage":"ready","merge_wait":{"at":"2026-09-25T10:00:00Z","head":"abc123","base":"def456","checks":[{"code":"checks","status":"blocked","detail":"Checks are failing.","action":"Fix the failing check.","url":"https://github.com/acme/app/pull/7/checks"}]}}}}}}`)
	output, err := os.CreateTemp(t.TempDir(), "status")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()
	if err := run(t.Context(), []string{"status", "--state-dir", dir}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(output.Name())
	for _, want := range []string{"pr:7", "abc123", "def456", "checks [blocked]", "Fix the failing check.", "https://github.com/acme/app/pull/7/checks"} {
		if !strings.Contains(string(raw), want) {
			t.Fatal("missing saved result", want, string(raw))
		}
	}
}

func TestStatusKeepsCurrentTaskExplanationWithOlderDiagnostics(t *testing.T) {
	dir, _ := fakeService(t, `{"towns":{"acme/app":{"workers":{},"tasks":{"pr:7":{"stage":"ready","detail":"Review Bot is paused; start it.","merge_wait":{"at":"2026-09-25T10:00:00Z","checks":[{"code":"checks","status":"blocked","detail":"Checks are failing.","action":"Fix the failing check."}]}},"pr:8":{"stage":"ready","blocked":true,"detail":"Targets release, but this town covers main."}}}}}`)
	output, err := os.CreateTemp(t.TempDir(), "status")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()
	if err := run(t.Context(), []string{"status", "--state-dir", dir}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pr:7", "Review Bot is paused; start it.", "Checks are failing.", "pr:8", "Targets release, but this town covers main."} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing current task detail %q: %s", want, raw)
		}
	}
}

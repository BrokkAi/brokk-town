package main

import (
	"os"
	"strings"
	"testing"
)

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

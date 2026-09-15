package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestCapacityCLIPostsRequiredBoundedLimit(t *testing.T) {
	seen := make(chan town.ServiceConfig, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" && r.Method == http.MethodGet {
			// The CLI probes a running service before every command.
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/capacity" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var cfg town.ServiceConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			t.Fatal(err)
		}
		seen <- cfg
		_, _ = w.Write([]byte(`{"active":1,"limit":7}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"capacity", "--state-dir", dir, "--max-workers", "7"}); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got.MaxWorkers != 7 {
		t.Fatalf("got %+v", got)
	}
	for _, args := range [][]string{
		{"capacity", "--state-dir", dir},
		{"capacity", "--state-dir", dir, "--max-workers", "0"},
		{"capacity", "--state-dir", dir, "--max-workers", "65"},
	} {
		if err := run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "--max-workers") {
			t.Fatalf("accepted invalid capacity args %v: %v", args, err)
		}
	}
}

func TestDecodeConfigFileSupportsCapacityObjectAndLegacyArray(t *testing.T) {
	configs, limit, err := decodeConfigFile([]byte(`{"max_workers":9,"towns":[{"repo":"acme/team","harness":"custom","agent":{"command":["fake"]},"merge_policy":"bot","poll_seconds":60,"report_seconds":60,"max_cycles":1}]}`))
	if err != nil || len(configs) != 1 || limit == nil || *limit != 9 {
		t.Fatalf("object config: configs=%d limit=%v err=%v", len(configs), limit, err)
	}
	configs, limit, err = decodeConfigFile([]byte(`[{"repo":"acme/team","harness":"custom","agent":{"command":["fake"]},"merge_policy":"bot","poll_seconds":60,"report_seconds":60,"max_cycles":1}]`))
	if err != nil || len(configs) != 1 || limit != nil {
		t.Fatalf("legacy config: configs=%d limit=%v err=%v", len(configs), limit, err)
	}
	for _, raw := range []string{
		`{"max_workers":null,"towns":[]}`,
		`{"max_workers":"4","towns":[]}`,
		`{"max_workers":{},"towns":[]}`,
		`{"max_workers":0,"towns":[]}`,
		`{"max_workers":65,"towns":[]}`,
	} {
		if _, _, err := decodeConfigFile([]byte(raw)); err == nil {
			t.Fatalf("accepted malformed capacity config %s", raw)
		}
	}
}

func TestServeConfigCapacityPrecedenceAndAtomicTownValidation(t *testing.T) {
	dir := t.TempDir()
	store, err := town.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *town.State) error {
		state.ServiceConfig.MaxWorkers = 9
		return nil
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	writeConfig := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "towns.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	serveConfig := func(t *testing.T, path string) error {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return serve(ctx, dir, "127.0.0.1:0", false, path, "")
	}
	readLimit := func(t *testing.T) int {
		t.Helper()
		current, err := town.Open(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		defer current.Close()
		return current.Snapshot().ServiceConfig.MaxWorkers
	}

	// An object config explicitly overrides the persisted service setting.
	if err := serveConfig(t, writeConfig(t, `{"max_workers":2,"towns":[]}`)); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("serve object config: %v", err)
	}
	if got := readLimit(t); got != 2 {
		t.Fatalf("object config did not override persisted limit: %d", got)
	}
	// The legacy array has no service setting and therefore preserves it.
	if err := serveConfig(t, writeConfig(t, `[]`)); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("serve legacy config: %v", err)
	}
	if got := readLimit(t); got != 2 {
		t.Fatalf("legacy config unexpectedly changed persisted limit: %d", got)
	}
	// Applying a bad town and a new capacity is one store transaction: neither
	// change may survive the validation error.
	bad := writeConfig(t, `{"max_workers":7,"towns":[{"repo":"invalid"}]}`)
	if err := serveConfig(t, bad); err == nil {
		t.Fatal("accepted invalid town config")
	}
	if got := readLimit(t); got != 2 {
		t.Fatalf("failed town config partially changed capacity: %d", got)
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestExecutionCLIUsesSharedCatalogAndSelectionAPI(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var selections []map[string]any
	var runtime map[string]any
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			w.Write([]byte(`{}`))
			return
		}
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing Town authentication")
		}
		if r.URL.Path == "/api/execution" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			selections = append(selections, body)
		}
		if r.URL.Path == "/api/execution-runtime" {
			if err := json.NewDecoder(r.Body).Decode(&runtime); err != nil {
				t.Error(err)
			}
		}
		w.Write([]byte(`{"configured":false}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	data, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{}, {"--refresh"}, {"--json"},
		{"--repo", "acme/app", "--target", "builder", "--profile", "coder"},
		{"--repo", "acme/app", "--role", "repo", "--local-execution"},
		{"--repo", "acme/app", "--role", "review", "--inherit-execution"},
		{"--repo", "acme/app", "--role", "review", "--runtime-session", "discovery"},
	} {
		if err := run(context.Background(), append([]string{"execution", "--state-dir", dir}, args...)); err != nil {
			t.Fatal(args, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 7 || paths[0] != "GET /api/execution-options" || paths[1] != "POST /api/execution-options/refresh" || len(selections) != 3 || paths[6] != "POST /api/execution-runtime" || runtime["session_id"] != "discovery" || runtime["role"] != "review" {
		t.Fatal(paths, selections)
	}
	if selections[0]["selection"].(map[string]any)["target_id"] != "builder" || selections[1]["role"] != "repo" || selections[2]["selection"] != nil {
		t.Fatal(selections)
	}
	for _, args := range [][]string{
		{"--repo", "acme/app", "--target", "builder"},
		{"--target", "builder", "--profile", "coder"},
		{"--repo", "acme/app", "--local-execution", "--inherit-execution", "--role", "review"},
		{"--repo", "acme/app", "--inherit-execution"},
		{"--repo", "acme/app", "--local-execution", "--role", "all"},
		{"--refresh", "--repo", "acme/app", "--local-execution"},
		{"--repo", "acme/app", "--runtime-session", ""},
		{"--repo", "acme/app", "--runtime-session", "discovery", "--local-execution"},
		{"--runtime-session", "discovery"},
	} {
		if err := run(context.Background(), append([]string{"execution", "--state-dir", dir}, args...)); err == nil {
			t.Fatal("accepted invalid selection", args)
		}
	}
	if len(paths) != 7 {
		t.Fatal("invalid request reached service", paths)
	}
}

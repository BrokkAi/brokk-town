package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestChoicesCommandUsesSharedProfileAPI(t *testing.T) {
	seen := make(chan map[string]any, 4)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/choices" {
			t.Error("unexpected request", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		seen <- body
		_, _ = w.Write([]byte(`{"models":[{"value":"remote-model","name":"Remote"}],"efforts":[]}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "fixture", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{}, {"--role", "review", "--model", "remote-model", "--json"}} {
		args := append([]string{"choices", "--state-dir", dir, "--repo", "acme/project"}, extra...)
		if err := run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
	bodies := []map[string]any{<-seen, <-seen}
	if bodies[0]["role"] != "" || bodies[1]["role"] != "review" || bodies[1]["town"] != "acme/project" {
		t.Fatal(bodies)
	}
	if agent := bodies[1]["agent"].(map[string]any); len(agent) != 1 || agent["model"] != "remote-model" {
		t.Fatal(agent)
	}
	for _, extra := range [][]string{{}, {"--repo", "bad"}, {"--repo", "acme/project", "--role", "all"}, {"--repo", "acme/project", "--harness", "custom"}} {
		if err := run(context.Background(), append([]string{"choices", "--state-dir", dir}, extra...)); err == nil {
			t.Fatal("accepted invalid choice request", extra)
		}
	}
	if len(seen) != 0 {
		t.Fatal("invalid command contacted choices API")
	}
}

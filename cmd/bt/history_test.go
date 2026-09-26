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

func TestHistoryCommandPagesAndInspectsWithoutMutation(t *testing.T) {
	var bodies []map[string]any
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			w.Write([]byte(`{}`))
			return
		}
		if r.URL.Path != "/api/history" || r.Method != "POST" {
			t.Error(r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		if body["task"] != nil {
			w.Write([]byte(`{"id":"issue:1","title":"Saved"}`))
		} else {
			w.Write([]byte(`{"town":"acme/project","items":[],"total":1,"retention_days":30}`))
		}
	}))
	defer h.Close()
	dir := t.TempDir()
	data, _ := json.Marshal(connection{URL: h.URL, Token: "fixture", PID: os.Getpid()})
	os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600)
	args := []string{"history", "--state-dir", dir, "--repo", "acme/project", "--json"}
	if err := run(context.Background(), append(args, "--after", "cursor", "--limit", "10")); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), append(args, "--task", "issue:1")); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0]["after"] != "cursor" || bodies[0]["limit"] != float64(10) || bodies[1]["task"] != "issue:1" || bodies[1]["limit"] != nil {
		t.Fatal(bodies)
	}
	for _, bad := range [][]string{{"history", "--state-dir", dir}, {"history", "--repo", "acme/project", "--limit", "101"}, {"history", "--repo", "acme/project", "--task", "issue:1", "--limit", "1"}} {
		if err := run(context.Background(), bad); err == nil {
			t.Fatal("invalid history arguments accepted")
		}
	}
	if len(bodies) != 2 {
		t.Fatal("invalid request contacted service")
	}
}

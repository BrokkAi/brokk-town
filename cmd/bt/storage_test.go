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

func TestStorageCommandSeparatesDryRunFromExplicitCleanup(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			w.Write([]byte(`{}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, body)
		if r.URL.Path == "/api/storage/cleanup" {
			w.Write([]byte(`[]`))
		} else {
			w.Write([]byte(`{"town":"acme/project","artifacts":[],"roles":{}}`))
		}
	}))
	defer h.Close()
	dir := t.TempDir()
	data, _ := json.Marshal(connection{URL: h.URL, Token: "fixture", PID: os.Getpid()})
	os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600)
	args := []string{"storage", "--state-dir", dir, "--repo", "acme/project", "--json"}
	if err := run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), append(args, "--older-than-hours", "12", "--cleanup", "abc,def")); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/api/storage" || paths[1] != "/api/storage/cleanup" || bodies[0]["minimum_age_hours"] != float64(168) || bodies[0]["ids"] != nil || bodies[1]["minimum_age_hours"] != float64(12) {
		t.Fatal(paths, bodies)
	}
	ids := bodies[1]["ids"].([]any)
	if len(ids) != 2 || ids[0] != "abc" || ids[1] != "def" {
		t.Fatal(ids)
	}
	for _, bad := range [][]string{{"storage", "--state-dir", dir}, {"storage", "--repo", "acme/project", "--older-than-hours", "-1"}} {
		if err := run(context.Background(), bad); err == nil {
			t.Fatal("accepted invalid storage arguments")
		}
	}
	if len(paths) != 2 {
		t.Fatal("invalid request contacted server")
	}
}

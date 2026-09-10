package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSettingsCLISendsSpecificProfileOrDefaults(t *testing.T) {
	type input struct {
		Town  string         `json:"town"`
		Role  string         `json:"role"`
		Agent map[string]any `json:"agent"`
	}
	received := make(chan input, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/settings" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("incorrect settings request")
		}
		var body input
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key"})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args  []string
		role  string
		agent map[string]any
	}{
		{[]string{"--model", "default-model"}, "", map[string]any{"model": "default-model"}},
		{[]string{"--role", "review", "--harness", "claude", "--model", "review-model", "--effort", "xhigh"}, "review", map[string]any{"harness": "claude", "model": "review-model", "effort": "xhigh"}},
		{[]string{"--role", "issue", "--model", "", "--effort", ""}, "issue", map[string]any{"model": "", "effort": ""}},
		{[]string{"--role", "review", "--inherit"}, "review", map[string]any{"inherit": true}},
	} {
		args := append([]string{"settings", "--state-dir", dir, "--repo", "Acme/Team"}, tc.args...)
		if err := run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		got := <-received
		if got.Town != "acme/team" || got.Role != tc.role || !reflect.DeepEqual(got.Agent, tc.agent) {
			t.Fatalf("got %+v for %v", got, tc.args)
		}
	}
	for _, flags := range [][]string{
		{"--role", "repo"}, {"--role", "all"}, {"--role", "invalid"},
		{"--inherit"}, {"--role", "review", "--inherit", "--model", "conflict"},
	} {
		args := append([]string{"settings", "--state-dir", dir, "--repo", "Acme/Team"}, flags...)
		if err := run(context.Background(), args); err == nil {
			t.Fatal("accepted invalid settings", flags)
		}
		select {
		case got := <-received:
			t.Fatal("invalid settings sent to service", got)
		default:
		}
	}
}

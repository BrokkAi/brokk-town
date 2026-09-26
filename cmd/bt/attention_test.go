package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestAttentionHookCLIAndConfig(t *testing.T) {
	seen := make(chan town.AttentionHookEdit, 2)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			_, _ = w.Write([]byte(`{"service_config":{"attention_hook":{"enabled":false,"configured":true}}}`))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/attention-hook" {
			t.Error("unexpected request")
			w.WriteHeader(500)
			return
		}
		var edit town.AttentionHookEdit
		if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
			t.Error(err)
		}
		seen <- edit
		_, _ = w.Write([]byte(`{"enabled":true,"configured":true}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "fixture", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "hook.json")
	if err := os.WriteFile(file, []byte(`["fixture","private-argument"]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"attention-hook", "--state-dir", dir, "--enable", "--command-file", file}); err != nil {
		t.Fatal(err)
	}
	edit := <-seen
	if edit.Enabled == nil || !*edit.Enabled || edit.Command == nil || (*edit.Command)[1] != "private-argument" {
		t.Fatal(edit)
	}
	if err := run(context.Background(), []string{"attention-hook", "--state-dir", dir, "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"attention-hook", "--state-dir", dir, "--enable", "--disable"}); err == nil {
		t.Fatal("accepted conflicting flags")
	}
	for _, raw := range []string{`{"attention_hook":null,"towns":[]}`, `{"attention_hook":{"enabled":true},"towns":[]}`, `{"attention_hook":{"enabled":false,"unknown":1},"towns":[]}`} {
		if _, _, err := decodeConfigFile([]byte(raw)); err == nil {
			t.Fatal("accepted invalid hook config", raw)
		}
	}
	_, service, err := decodeConfigFile([]byte(`{"attention_hook":{"enabled":true,"command":["fixture"]},"towns":[]}`))
	if err != nil || service.AttentionHook == nil || !service.AttentionHook.Enabled {
		t.Fatal(service, err)
	}
}

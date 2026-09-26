package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAttentionHookAPIValidatesPersistsAndKeepsCommandPrivate(t *testing.T) {
	s, h := fixture(t)
	r := call(t, h.URL, "POST", "/api/attention-hook", `{"enabled":true}`, "", "")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatal(r.Status)
	}
	for _, body := range []string{`{}`, `null`, `{"enabled":true}`, `{"enabled":true,"command":[]}`, `{"command":[""]}`, `{"command":["private\nargument"]}`, `{"command":["x"],"unknown":true}`, `{"enabled":"yes"}`} {
		r := call(t, h.URL, "POST", "/api/attention-hook", body, "test-key", "")
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Fatal("accepted invalid settings", body, r.Status)
		}
	}
	r = call(t, h.URL, "POST", "/api/attention-hook", `{"enabled":true,"command":["fixture","private-command-token"]}`, "test-key", "")
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || strings.Contains(string(data), "private-command-token") || !strings.Contains(string(data), `"configured":true`) {
		t.Fatal(r.Status, string(data))
	}
	if h := s.Store.Snapshot().ServiceConfig.AttentionHook; !h.Enabled || len(h.Command) != 2 {
		t.Fatal(h)
	}
	for _, req := range []struct{ method, path, body string }{{"GET", "/api/state", ""}, {"POST", "/api/quiet-hours", `{"windows":[]}`}, {"POST", "/api/attention-hook", `{"enabled":false}`}} {
		r := call(t, h.URL, req.method, req.path, req.body, "test-key", "")
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || strings.Contains(string(data), "private-command-token") || strings.Contains(string(data), "attention_pending") {
			t.Fatal("private settings exposed", req.path, r.Status, string(data))
		}
	}
	if h := s.Store.Snapshot().ServiceConfig.AttentionHook; h.Enabled || len(h.Command) != 2 {
		t.Fatal("disabling lost private command")
	}
}

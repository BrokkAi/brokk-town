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

func TestGuideTerminalSharesReadAskAndExactProposalCommands(t *testing.T) {
	var actions []map[string]any
	g := town.GuideConversation{Next: 1, Revision: 1, Turns: []town.GuideTurn{{ID: "saved-question", Sequence: 0, Question: "Pause issue", Answer: "Proposal ready", Status: "complete", Proposal: &town.GuideProposal{Action: "pause", Role: town.Issue, Digest: "exact-digest", Description: "Pause issue", Status: "proposed"}}}}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			w.Write([]byte(`{}`))
			return
		}
		if r.URL.Path != "/api/guide" {
			t.Error(r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		actions = append(actions, body)
		json.NewEncoder(w).Encode(g)
	}))
	defer h.Close()
	dir := t.TempDir()
	data, _ := json.Marshal(connection{URL: h.URL, Token: "fixture", PID: os.Getpid()})
	os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600)
	args := []string{"guide", "--state-dir", dir, "--repo", "acme/project", "--json"}
	if err := run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0]["action"] != "read" {
		t.Fatal(actions)
	}
	if err := run(context.Background(), append(args, "--confirm", "--request-id", "saved-question", "--proposal-digest", "wrong")); err == nil {
		t.Fatal("confirmed different digest")
	}
	before := len(actions)
	if err := run(context.Background(), append(args, "--confirm", "--request-id", "saved-question", "--proposal-digest", "exact-digest")); err != nil {
		t.Fatal(err)
	}
	if len(actions) != before+2 || actions[len(actions)-1]["action"] != "confirm" || actions[len(actions)-1]["digest"] != "exact-digest" {
		t.Fatal(actions)
	}
	if err := run(context.Background(), append(args, "--ask", "What needs attention?")); err != nil {
		t.Fatal(err)
	}
	ask := actions[len(actions)-1]
	if ask["action"] != "ask" || ask["sequence"] != float64(1) || ask["question"] != "What needs attention?" || ask["id"] == "" {
		t.Fatal(ask)
	}
	before = len(actions)
	for _, bad := range [][]string{{"guide"}, {"guide", "--repo", "acme/project", "--cancel"}, {"guide", "--repo", "acme/project", "--ask", "x", "--confirm"}} {
		if err := run(context.Background(), bad); err == nil {
			t.Fatal("bad flags accepted")
		}
	}
	if len(actions) != before {
		t.Fatal("invalid flags contacted service")
	}
}

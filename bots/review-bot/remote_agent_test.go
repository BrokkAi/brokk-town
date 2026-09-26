package reviewbot

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRemoteSnapshotUsesCompleteExactDiffWithoutEmbeddingIt(t *testing.T) {
	s := Snapshot{MergeBase: strings.Repeat("a", 40), Diff: strings.Repeat("large-diff", 100000), Focus: "all changed code", Discussion: []Discussion{{ID: "comment:9", Body: "Keep this complete discussion"}}}
	s.PR.Head.SHA = strings.Repeat("b", 40)
	text, err := remoteSnapshotContext(s)
	if err != nil || len(text) > 65536 || strings.Contains(text, "large-diff") || !strings.Contains(text, s.MergeBase+" "+s.PR.Head.SHA) || !strings.Contains(text, s.Discussion[0].Body) || !strings.Contains(text, s.Focus) {
		t.Fatalf("remote snapshot lost exact diff or metadata: %v", err)
	}
	if !strings.HasPrefix(s.Diff, "large-diff") {
		t.Fatal("remote context changed the local snapshot")
	}
}

func TestRemotePromptLimitRefusesBeforeContactingParent(t *testing.T) {
	for _, prompt := range []string{strings.Repeat("x", 65537), strings.Repeat("界", 65537), " \n", string([]byte{0xff})} {
		_, err := executeRemote(t.Context(), Config{RemoteHead: strings.Repeat("a", 40), RemoteAgent: "/does/not/exist"}, prompt)
		if err == nil || !strings.Contains(err.Error(), "no session was requested") {
			t.Fatalf("invalid prompt reached remote transport: %v", err)
		}
	}
}

func remoteFixture(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	// Keep the socket below Unix's platform-specific pathname limit.
	dir, err := os.MkdirTemp("", "brv-remote-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = os.RemoveAll(dir) })
	return path
}

func TestRemoteReviewKeepsIndependentVerificationAndPublicationGates(t *testing.T) {
	for _, scenario := range []string{"success", "dry run", "missing evidence", "wrong revision", "invalid receipt", "refused"} {
		t.Run(scenario, func(t *testing.T) {
			e, state, source, agent, _ := fixture(t)
			var mu sync.Mutex
			calls := 0
			e.config.Agent.Command = []string{"must-never-launch-a-local-agent"}
			e.config.DryRun = scenario == "dry run"
			e.config.RemoteAgent = remoteFixture(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				var request struct {
					Protocol     int
					Head, Prompt string
				}
				if r.Method != "POST" || r.URL.Path != "/v1/agent" || json.NewDecoder(r.Body).Decode(&request) != nil || request.Protocol != 1 || request.Head != source.prs[0].Head.SHA {
					t.Error("lost exact revision at the remote boundary")
					w.WriteHeader(400)
					return
				}
				if !strings.Contains(request.Prompt, "inline data") || strings.Contains(request.Prompt, e.config.StateDirectory) || strings.Contains(request.Prompt, e.config.Directory) {
					t.Error("remote prompt depends on a controller-local snapshot")
				}
				if scenario == "refused" {
					http.Error(w, "private-parent-diagnostic", 409)
					return
				}
				cfg := e.config
				cfg.Directory = filepath.Join(cfg.Directory, request.Head, string(rune('a'+calls)))
				text, err := agent.factory(cfg).Execute(r.Context(), request.Prompt)
				if err != nil {
					t.Error(err)
				}
				head, evidence := request.Head, "run-proof"
				if scenario == "wrong revision" {
					head = strings.Repeat("f", 40)
				}
				if scenario == "missing evidence" {
					evidence = ""
				}
				if scenario == "invalid receipt" {
					text = "No new comments."
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"protocol": 1, "head": head, "text": text, "evidence": evidence})
			})
			e.agent = func(c Config) Agent { return agentProcess{c, e.log} }
			err := e.step(t.Context(), state)
			mu.Lock()
			defer mu.Unlock()
			if scenario == "success" || scenario == "dry run" {
				if err != nil || calls != 2 || agent.investigations != 1 || agent.verifications != 1 {
					t.Fatalf("remote phases were not independently verified: calls=%d error=%v", calls, err)
				}
				wantCreates, wantStatus := 1, "submitted"
				if scenario == "dry run" {
					wantCreates, wantStatus = 0, "dry_run"
				}
				if source.creates != wantCreates || state.Jobs[0].Status != wantStatus || len(state.Jobs[0].Payload.Comments) != 1 {
					t.Fatal("remote review changed publication or dry-run semantics")
				}
			} else if source.creates != 0 || calls != 1 || len(state.Jobs) != 1 || state.Jobs[0].Status == "submitted" {
				t.Fatal("refused remote evidence reached the publisher")
			}
			if err != nil && strings.Contains(err.Error(), "private-parent-diagnostic") {
				t.Fatal("private parent diagnostics leaked")
			}
		})
	}
}

func TestRemoteAgentCancellationAndStrictResponse(t *testing.T) {
	for _, scenario := range []string{"cancel", "redirect", "trailing", "overflow"} {
		t.Run(scenario, func(t *testing.T) {
			entered := make(chan struct{})
			path := remoteFixture(t, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				switch scenario {
				case "cancel":
					<-r.Context().Done()
				case "redirect":
					http.Redirect(w, r, "http://elsewhere.invalid/", 302)
				case "trailing":
					json.NewEncoder(w).Encode(map[string]any{"protocol": 1, "head": strings.Repeat("a", 40), "text": reviewReceipt, "evidence": "proof"})
					w.Write([]byte(`{}`))
				case "overflow":
					w.Write([]byte(strings.Repeat("x", (2<<20)+1)))
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if scenario == "cancel" {
				go func() { <-entered; cancel() }()
			}
			_, err := executeRemote(ctx, Config{RemoteAgent: path, RemoteHead: strings.Repeat("a", 40)}, "review")
			if err == nil {
				t.Fatal("accepted an unconfirmed remote result")
			}
		})
	}
}

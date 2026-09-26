package mjolnir

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const managedAnswer = `REVIEW_RESULT {"summary":"Checked the exact revision","findings":[]}`

func TestManagedACPRejectsInvalidPromptBeforeProvisioning(t *testing.T) {
	for _, prompt := range []string{strings.Repeat("x", 65537), strings.Repeat("界", 65537), " \n\t", "!shell", string([]byte{0xff})} {
		e, plan, record := executorFixture(t, "success")
		answer, err := e.Execute(t.Context(), plan, prompt, false)
		if err == nil || !strings.Contains(err.Error(), "no session was created") || answer.Run != nil {
			t.Fatalf("invalid prompt was not rejected before creation: %v", err)
		}
		if _, err := os.Stat(record); !os.IsNotExist(err) {
			t.Fatal("invalid prompt invoked the adapter")
		}
	}
}

type acpFixtureConfig struct {
	URL, Token, Directory, Record, Scenario string
}

func executorFixture(t *testing.T, scenario string) (Executor, RunPlan, string) {
	t.Helper()
	dir := t.TempDir()
	plan := runPlan(t)
	plan.Model, plan.Effort = "careful", "high"
	record := filepath.Join(dir, "calls")
	var mu sync.Mutex
	model, effort, destroyed := "fast", "low", false
	if scenario == "stale configuration" {
		effort = "high" // Changing model resets this; a stale GET must not skip restoring it.
	}
	staleModel, staleEffort, lagReads, completionReads := "", "", 0, 0
	patches := map[string]int{}
	c, _ := catalogFixture(t, func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		artifactHeaders(w, "application/json")
		if req.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("missing private API authentication")
		}
		fields := func() map[string]any {
			f := launchFields()
			f["config_options"] = []any{
				map[string]any{"key": "model", "current": model, "choices": []any{map[string]string{"value": "fast"}, map[string]string{"value": "careful"}}},
				map[string]any{"key": "effort", "current": effort, "choices": []any{map[string]string{"value": "low"}, map[string]string{"value": "high"}}},
			}
			if scenario == "runtime drift" {
				f["runtime"].(map[string]any)["id"] = "replacement"
			}
			return f
		}
		switch req.URL.Path {
		case "/api/v1/options":
			fmt.Fprint(w, `{"revision":1,"profiles":[{"id":"profile","harness":"codex"}],"targets":[{"id":"target","kind":"docker","availability":"ready"}],"bundles":[{"id":"bundle","primary_repository":"project","repositories":[{"id":"project","github":"acme/app","destination":"app"}]}]}`)
		case "/api/v1/workspaces":
			if req.Method != "GET" {
				t.Error("did not reuse workspace")
			}
			json.NewEncoder(w).Encode(map[string]any{"workspaces": []Workspace{plan.Placement.Workspace}})
		case "/api/v1/sessions/session":
			if destroyed {
				w.WriteHeader(404)
				return
			}
			f := fields()
			if lagReads > 0 {
				actualModel, actualEffort := model, effort
				model, effort = staleModel, staleEffort
				f = fields()
				model, effort = actualModel, actualEffort
				lagReads--
			}
			calls, _ := os.ReadFile(record)
			if strings.Contains(string(calls), "session/prompt") {
				if scenario == "stale completion" && completionReads < 2 {
					f["is_idle"], f["chat_phase"] = false, "running"
					completionReads++
				}
				if scenario == "completion drift" {
					f["runtime"].(map[string]any)["id"] = "replacement"
				}
			}
			json.NewEncoder(w).Encode(f)
		case "/api/v1/sessions/session/config":
			var setting struct{ Key, Value string }
			if req.Method != "PATCH" || json.NewDecoder(req.Body).Decode(&setting) != nil {
				t.Error("invalid configuration request")
			}
			patches[setting.Key]++
			if patches[setting.Key] > 1 {
				t.Error("replayed a configuration mutation")
			}
			if scenario == "lost setting" {
				w.WriteHeader(500)
				fmt.Fprint(w, "private-diagnostic")
				return
			}
			staleModel, staleEffort = model, effort
			if setting.Key == "model" {
				model, effort = setting.Value, "low"
			} else if setting.Key == "effort" {
				effort = setting.Value
			}
			if scenario == "stale configuration" {
				lagReads = 2
			}
			json.NewEncoder(w).Encode(fields())
		case "/api/v1/sessions/session/diff":
			json.NewEncoder(w).Encode(map[string]any{"base": req.URL.Query().Get("base"), "head": evidenceBase, "diff": "", "head_descends_from_base": true})
		case "/api/v1/sessions/session/transcript":
			if scenario == "missing evidence" {
				w.WriteHeader(404)
				return
			}
			if model != "careful" || effort != "high" {
				t.Error("prompt did not use the selected target settings")
			}
			items := []any{}
			if req.URL.Query().Get("after_seq") == "0" {
				items = append(items, map[string]any{"stable_id": "answer", "seq": 2, "position": 1, "role": "agent", "text": managedAnswer})
			}
			json.NewEncoder(w).Encode(map[string]any{"session_id": "session", "latest_seq": 2, "next_after_seq": 2, "items": items})
		case "/api/v1/sessions/session/destroy":
			data, err := os.ReadFile(filepath.Join(dir, plan.ID, "run.json"))
			var saved RunRecord
			if err != nil || json.Unmarshal(data, &saved) != nil || saved.State != "destroying" || saved.Evidence == "" {
				t.Error("cleanup before durable evidence and cleanup intent")
			}
			destroyed = true
			w.WriteHeader(202)
		default:
			t.Errorf("unexpected HTTP operation %s %s", req.Method, req.URL.Path)
			w.WriteHeader(400)
		}
	})
	config := acpFixtureConfig{c.connection.URL, c.connection.TokenFile, dir, record, scenario}
	data, _ := json.Marshal(config)
	path := filepath.Join(dir, "fixture.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOWN_MJ_ACP_FIXTURE", path)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Executor{Runs: &Runs{Catalog: c, Directory: dir}, Command: []string{exe, "-test.run=^TestMjACPHelper$", "--"}}, plan, record
}

func TestManagedACPDispatchGuardsAndDurableOutcomes(t *testing.T) {
	for _, scenario := range []string{"success", "stale configuration", "stale completion", "completion drift", "wrong daemon", "lost create", "runtime drift", "lost setting", "missing evidence", "wrong session", "incomplete turn"} {
		t.Run(scenario, func(t *testing.T) {
			e, plan, record := executorFixture(t, scenario)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			answer, err := e.Execute(ctx, plan, "review this exact revision", false)
			calls, _ := os.ReadFile(record)
			if scenario == "success" || scenario == "stale configuration" || scenario == "stale completion" {
				if err != nil || answer.Text != managedAnswer || answer.Run.Record().State != "evidence" || answer.Artifacts.Head != plan.Checkout.Commit {
					t.Fatalf("managed answer not backed by evidence: %v", err)
				}
				if err := answer.Run.Destroy(ctx); err != nil {
					t.Fatal(err)
				}
				if answer.Run.Record().State != "destroyed" {
					t.Fatal("cleanup was not confirmed")
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "private-") {
					t.Fatalf("unsafe or missing refusal: %v", err)
				}
				if scenario == "wrong daemon" {
					if len(calls) != 0 || answer.Run != nil {
						t.Fatal("created session on a different daemon")
					}
					return
				}
				if answer.Run == nil || answer.Run.Destroy(ctx) == nil {
					t.Fatal("uncertain evidence was discarded")
				}
				if scenario == "missing evidence" || scenario == "completion drift" {
					data, err := os.ReadFile(filepath.Join(answer.Run.directory, "completed-answer.json"))
					var completion struct {
						Session string `json:"session_id"`
						Answer  string `json:"answer"`
					}
					if err != nil || json.Unmarshal(data, &completion) != nil || completion.Session != "session" || completion.Answer != managedAnswer {
						t.Fatal("lost the confirmed ACP answer after refusing its artifacts")
					}
				}
				before, _ := os.ReadFile(record)
				if _, retryErr := e.Execute(ctx, plan, "retry", false); retryErr == nil {
					t.Fatal("repeated an uncertain submission")
				}
				after, _ := os.ReadFile(record)
				if strings.Count(string(after), "session/new") != strings.Count(string(before), "session/new") {
					t.Fatal("sent another creation request")
				}
				if (scenario == "lost create" || scenario == "runtime drift" || scenario == "lost setting") && strings.Contains(string(calls), "session/prompt") {
					t.Fatal("prompted without a confirmed launch/configuration")
				}
			}
			records, err := e.Runs.Records()
			if err != nil || len(records) != 1 {
				t.Fatalf("lost durable run: %v", err)
			}
		})
	}
}

func TestUnpublishedConfigurationStopsWithoutMutation(t *testing.T) {
	e, _, record := executorFixture(t, "success")
	r := &Run{owner: e.Runs, record: RunRecord{Session: "session"}}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := r.awaitConfiguration(ctx, [][2]string{{"model", "careful"}}); err == nil || ctx.Err() == nil {
		t.Fatal("unconfirmed configuration did not stop at the deadline", err)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("configuration confirmation submitted an ACP request")
	}
}

func TestManagedACPCancellationInterruptsAndRetainsSession(t *testing.T) {
	e, plan, record := executorFixture(t, "hang")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := e.Execute(ctx, plan, "wait for cancellation", false); done <- err }()
	deadline := time.After(5 * time.Second)
	for {
		data, _ := os.ReadFile(record)
		if strings.Contains(string(data), "session/prompt") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("fake adapter never reached prompt")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("adapter did not stop promptly")
	}
	data, _ := os.ReadFile(record)
	if !strings.Contains(string(data), "session/cancel") {
		t.Fatal("adapter was killed without requesting target cancellation")
	}
	records, err := e.Runs.Records()
	if err != nil || len(records) != 1 || records[0].State != "prompting" || records[0].Session != "session" {
		t.Fatal("cancelled run was not retained")
	}
}

// Credential-free stand-in for the mj executable, including its API-info
// command and ACP adapter. It checks the saved intent before acknowledging a
// session, so these tests exercise the real process/protocol boundary.
func TestMjACPHelper(t *testing.T) {
	path := os.Getenv("TOWN_MJ_ACP_FIXTURE")
	if path == "" {
		return
	}
	var fixture acpFixtureConfig
	data, _ := os.ReadFile(path)
	if json.Unmarshal(data, &fixture) != nil {
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	if slices.Contains(os.Args, "api-info") {
		if fixture.Scenario == "wrong daemon" {
			fixture.URL = "http://127.0.0.1:1/api/v1"
		}
		enc.Encode(map[string]string{"base_url": fixture.URL, "token_path": fixture.Token})
		os.Exit(0)
	}
	args := strings.Join(os.Args, " ")
	for _, required := range []string{"--checkout-commit " + evidenceBase, "--checkout-branch town/run-123", "--expected-runtime-identity " + selectedRuntime, "--on-exit keep", "--target target", "--profile profile", "--bundle bundle"} {
		if !strings.Contains(args, required) {
			os.Exit(3)
		}
	}
	record, err := os.OpenFile(fixture.Record, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(4)
	}
	send := func(value any) {
		if enc.Encode(value) != nil {
			os.Exit(5)
		}
	}
	reply := func(id json.RawMessage, value any) { send(map[string]any{"jsonrpc": "2.0", "id": id, "result": value}) }
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 5<<20)
	for scanner.Scan() {
		var message struct {
			ID     json.RawMessage
			Method string
			Params map[string]any
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			os.Exit(6)
		}
		fmt.Fprintln(record, message.Method)
		switch message.Method {
		case "initialize":
			reply(message.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}, "authMethods": []any{}})
		case "session/new":
			var intent RunRecord
			data, err := os.ReadFile(filepath.Join(fixture.Directory, "run-123", "run.json"))
			if err != nil || json.Unmarshal(data, &intent) != nil || intent.State != "creating" || intent.Plan.Checkout.Commit != evidenceBase || message.Params["cwd"] != "/" {
				os.Exit(7)
			}
			if fixture.Scenario == "lost create" {
				os.Exit(8)
			}
			reply(message.ID, map[string]string{"sessionId": "session"})
		case "session/prompt":
			var intent RunRecord
			data, _ := os.ReadFile(filepath.Join(fixture.Directory, "run-123", "run.json"))
			if json.Unmarshal(data, &intent) != nil || intent.State != "prompting" {
				os.Exit(9)
			}
			if fixture.Scenario == "hang" {
				continue
			}
			id := "session"
			if fixture.Scenario == "wrong session" {
				id = "other"
			}
			for _, chunk := range []string{managedAnswer[:10], managedAnswer[10:]} {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": id, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": chunk}}}})
			}
			stop := "end_turn"
			if fixture.Scenario == "incomplete turn" {
				stop = "max_tokens"
			}
			reply(message.ID, map[string]string{"stopReason": stop})
		case "session/cancel":
			os.Exit(0)
		}
	}
	os.Exit(0)
}

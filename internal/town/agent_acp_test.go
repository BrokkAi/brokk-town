package town

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/runner"
)

// The rest of this package substitutes executeAgent, so nothing else drives
// the real acp-go runner over the wire: an acp-go upgrade could change the
// handshake and every other test would still pass. TestACPAgentHelper is a
// credential-free simulated ACP agent run from the test binary itself.

const reviewReceipt = `TOWN_REVIEW {"verdict":"clean","complete":true,"summary":"all covered","checks":["go test ./..."],"findings":[]}`

type acpRun struct {
	text       string
	err        error
	calls      []string
	state      string
	transcript string
}

// runSimulatedACP may run off the test goroutine, so it reports setup
// failures with t.Error and returns them rather than calling t.Fatal.
func runSimulatedACP(t *testing.T, ctx context.Context, dir, script string, agent runner.AgentConfig) acpRun {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Error(err)
		return acpRun{err: err}
	}
	workspace, state := filepath.Join(dir, "work"), filepath.Join(dir, "state")
	for _, d := range []string{workspace, state} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Error(err)
			return acpRun{err: err}
		}
	}
	record := filepath.Join(dir, "calls.jsonl")
	agent.Command = []string{exe, "-test.run=^TestACPAgentHelper$"}
	agent.Environment = map[string]string{"BROKK_ACP_HELPER": script, "BROKK_ACP_RECORD": record}
	town := &Town{ID: "acme/orchard", Config: Config{Agent: agent}}
	tree := sessionTree{dir: workspace, repository: filepath.Join(state, "repository.git")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var out acpRun
	out.text, out.err = (&BotWorkers{}).runAgent(ctx, town, tree, "review", log, "review the change")
	if body, err := os.ReadFile(record); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			var call struct {
				Method string `json:"method"`
			}
			if json.Unmarshal([]byte(line), &call) == nil {
				out.calls = append(out.calls, call.Method)
			}
		}
		out.state = string(body)
	}
	sessions, _ := filepath.Glob(filepath.Join(state, "sessions", "session-*.jsonl"))
	if len(sessions) == 1 {
		body, _ := os.ReadFile(sessions[0])
		out.transcript = string(body)
	}
	return out
}

func TestACPRunnerSelectsModelAndEffortBeforeThePrompt(t *testing.T) {
	run := runSimulatedACP(t, context.Background(), t.TempDir(), "commentary", runner.AgentConfig{Model: "careful", Effort: "xhigh"})
	if run.err != nil {
		t.Fatalf("the real runner could not complete a session: %v", run.err)
	}
	want := []string{"initialize", "session/new", "session/set_config_option", "session/set_config_option", "session/prompt"}
	if strings.Join(run.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", run.calls, want)
	}
	if !strings.Contains(run.state, `"careful"`) || !strings.Contains(run.state, `"xhigh"`) {
		t.Fatalf("selections did not reach the agent:\n%s", run.state)
	}
	// The runner advertises session config options; Town's choice probe
	// advertises the same so both see the same selectors.
	if !strings.Contains(run.state, `"configOptions"`) {
		t.Fatalf("initialize did not advertise session config options:\n%s", run.state)
	}
}

func TestACPReceiptSurvivesASeparateCommentaryMessage(t *testing.T) {
	// The agent ends a commentary message without a newline, then sends the
	// receipt as a new message. acp-go v0.1.0 welded the two into one line.
	run := runSimulatedACP(t, context.Background(), t.TempDir(), "commentary", runner.AgentConfig{})
	if run.err != nil {
		t.Fatal(run.err)
	}
	var result struct {
		Verdict  string    `json:"verdict"`
		Complete bool      `json:"complete"`
		Summary  string    `json:"summary"`
		Checks   []string  `json:"checks"`
		Findings []Finding `json:"findings"`
	}
	if err := receipt(run.text, "TOWN_REVIEW", &result); err != nil {
		t.Fatalf("a valid receipt after commentary was rejected: %v\n%q", err, run.text)
	}
	if result.Verdict != "clean" || !result.Complete {
		t.Fatalf("receipt = %+v", result)
	}
}

func TestACPRunnerWritesATranscriptAndApprovesPermissions(t *testing.T) {
	run := runSimulatedACP(t, context.Background(), t.TempDir(), "permission", runner.AgentConfig{})
	if run.err != nil {
		t.Fatal(run.err)
	}
	// The agent only sends its receipt once the client picked allow_once.
	if !strings.HasSuffix(strings.TrimSpace(run.text), reviewReceipt) {
		t.Fatalf("permission was not approved: %q", run.text)
	}
	for _, want := range []string{"review the change", "permission_request", `"selected":"allow"`, "agent_message_chunk", "session_end"} {
		if !strings.Contains(run.transcript, want) {
			t.Fatalf("transcript is missing %q:\n%s", want, run.transcript)
		}
	}
}

func TestACPUnavailableModelIsASetupError(t *testing.T) {
	run := runSimulatedACP(t, context.Background(), t.TempDir(), "commentary", runner.AgentConfig{Model: "nonexistent-model"})
	var setup *runner.SetupError
	if !errors.As(run.err, &setup) || !strings.Contains(run.err.Error(), "nonexistent-model") {
		t.Fatalf("an unavailable model was not a setup error naming it: %v", run.err)
	}
	for _, call := range run.calls {
		if call == "session/prompt" {
			t.Fatal("prompt was sent after the model was rejected")
		}
	}
}

func TestACPCancellationStopsAPromptInFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan acpRun, 1)
	dir := t.TempDir()
	go func() { done <- runSimulatedACP(t, ctx, dir, "hang", runner.AgentConfig{}) }()
	// Cancel only once the prompt is in flight, so this is not a setup failure.
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		body, _ := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
		if strings.Contains(string(body), `"session/prompt"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prompt never reached the agent")
		}
	}
	cancel()
	select {
	case run := <-done:
		if run.err == nil {
			t.Fatal("a canceled prompt produced an answer")
		}
		var setup *runner.SetupError
		if errors.As(run.err, &setup) {
			t.Fatalf("a canceled prompt was reported as a setup failure: %v", run.err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("cancellation did not stop the session")
	}
}

func TestLegacyThoughtLevelEffortIsListedAndSelected(t *testing.T) {
	// acp-go v0.8.1 SetEffort dropped v0.1.0's fallback to an uncategorized
	// thought_level option. Town restores it, and the probe and a run agree.
	run := runSimulatedACP(t, context.Background(), t.TempDir(), "legacy-effort", runner.AgentConfig{Effort: "xhigh"})
	if run.err != nil {
		t.Fatalf("a legacy thought_level effort was not selectable: %v", run.err)
	}
	if !strings.Contains(run.state, `"configId":"thought_level","sessionId":"fixture","value":"xhigh"`) {
		t.Fatalf("effort was not sent to the thought_level option:\n%s", run.state)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig("acme/orchard")
	c.Agent = runner.AgentConfig{Command: []string{exe, "-test.run=^TestACPAgentHelper$"},
		Environment: map[string]string{"BROKK_ACP_HELPER": "legacy-effort", "BROKK_ACP_RECORD": filepath.Join(t.TempDir(), "calls.jsonl")}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listed, err := ProbeAgent(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Efforts) != 2 || listed.Efforts[1].Value != "xhigh" {
		t.Fatalf("probe listed %+v, not the thought_level values a run selects", listed.Efforts)
	}
}

func TestChoicesMatchTheSelectorsARunUses(t *testing.T) {
	var session acp.Session
	body := `{"sessionId":"s","configOptions":[
		{"id":"legacy_model","name":"Legacy","type":"select","currentValue":"x","options":[{"value":"x","name":"X"}]},
		{"id":"model","name":"Model","category":"model","type":"select","currentValue":"a","options":[
			{"group":"fast","name":"Fast","options":[{"value":"a","name":"A"}]},
			{"group":"deep","name":"Deep","options":[{"value":"b","name":"B","description":"slow"}]}]},
		{"id":"reasoning_effort","name":"Effort","type":"select","currentValue":"low","options":[{"value":"low","name":"Low"},{"value":"high","name":"High"}]},
		{"id":"effort_hint","name":"Hint","category":"reasoning_effort","type":"select","currentValue":"h","options":[{"value":"h","name":"H"}]},
		{"id":"verbose","name":"Verbose","type":"boolean","currentValue":true}]}`
	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatal(err)
	}
	got, err := choices(session)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	// v0.1.0 order: a reasoning_effort category outranks the bare ID.
	want := `{"models":[{"value":"a","name":"A"},{"value":"b","name":"B"}],"efforts":[{"value":"h","name":"H"}]}`
	if string(encoded) != want {
		t.Fatalf("choices = %s, want %s", encoded, want)
	}
	empty, err := choices(acp.Session{})
	if encoded, _ = json.Marshal(empty); err != nil || string(encoded) != `{"models":[],"efforts":[]}` {
		t.Fatalf("empty choices = %s (%v)", encoded, err)
	}
}

// TestACPAgentHelper speaks ACP over stdio when BROKK_ACP_HELPER names a
// script. It records every request it receives, before answering it.
func TestACPAgentHelper(t *testing.T) {
	script := os.Getenv("BROKK_ACP_HELPER")
	if script == "" {
		return
	}
	record, err := os.OpenFile(os.Getenv("BROKK_ACP_RECORD"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(3)
	}
	enc := json.NewEncoder(os.Stdout)
	send := func(v any) {
		if enc.Encode(v) != nil {
			os.Exit(7)
		}
	}
	reply := func(id json.RawMessage, result any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	chunk := func(messageID, text string) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
			"sessionId": "fixture", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": messageID,
				"content": map[string]string{"type": "text", "text": text}}}})
	}
	current := map[string]string{"model": "fast", "reasoning_effort": "low", "thought_level": "low"}
	value := func(v, name string) map[string]string { return map[string]string{"value": v, "name": name} }
	selectors := func() []any {
		model := map[string]any{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": current["model"],
			"options": []any{value("fast", "Fast"), value("careful", "Careful")}}
		if script == "legacy-effort" {
			// Uncategorized selectors only: acp-go v0.1.0 chose thought_level
			// over reasoning_effort; v0.8.1 SetEffort sees only the latter.
			return []any{model,
				map[string]any{"id": "reasoning_effort", "name": "Codex effort", "type": "select", "currentValue": current["reasoning_effort"],
					"options": []any{value("low", "Low"), value("high", "High")}},
				map[string]any{"id": "thought_level", "name": "Thought level", "type": "select", "currentValue": current["thought_level"],
					"options": []any{value("low", "Low"), value("xhigh", "Extra high")}},
			}
		}
		return []any{model,
			map[string]any{"id": "reasoning_effort", "name": "Effort", "category": "thought_level", "type": "select", "currentValue": current["reasoning_effort"],
				"options": []any{value("low", "Low"), value("xhigh", "Extra high")}},
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var prompt json.RawMessage
	for scanner.Scan() {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
			Result struct {
				Outcome struct {
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			} `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			os.Exit(4)
		}
		if message.Method == "" {
			// The client's answer to our permission request.
			if prompt != nil && message.Result.Outcome.OptionID == "allow" {
				chunk("m1", "Approved.")
				chunk("m2", reviewReceipt)
				reply(prompt, map[string]string{"stopReason": "end_turn"})
			}
			continue
		}
		if _, err := record.Write(append(append([]byte{}, scanner.Bytes()...), '\n')); err != nil {
			os.Exit(5)
		}
		switch message.Method {
		case "initialize":
			reply(message.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}, "authMethods": []any{}})
		case "session/new":
			reply(message.ID, map[string]any{"sessionId": "fixture", "configOptions": selectors()})
		case "session/set_config_option":
			id, _ := message.Params["configId"].(string)
			current[id], _ = message.Params["value"].(string)
			reply(message.ID, map[string]any{"configOptions": selectors()})
		case "session/prompt":
			prompt = message.ID
			switch script {
			case "commentary", "legacy-effort":
				chunk("m1", "The review is complete.")
				chunk("m2", reviewReceipt)
				reply(message.ID, map[string]string{"stopReason": "end_turn"})
			case "permission":
				send(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{
					"sessionId": "fixture",
					"toolCall":  map[string]any{"toolCallId": "call-1", "title": "Edit a file"},
					"options": []any{
						map[string]string{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
						map[string]string{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
					}}})
			case "hang":
				// Never answer: only cancellation or a killed process ends it.
			}
		case "session/cancel":
			if prompt != nil {
				reply(prompt, map[string]string{"stopReason": "cancelled"})
			}
		default:
			if message.ID != nil {
				reply(message.ID, map[string]any{})
			}
		}
	}
	os.Exit(0)
}

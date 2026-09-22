package issuebot

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrokkAi/acp-go/runner"
)

// simulatedACPAgent is a complete ACP agent over stdio. It exists because the
// rest of this suite substitutes the Agent interface, so nothing else drives
// the real runner: an acp-go upgrade could change the wire handshake and every
// other test would still pass. It needs no credentials and starts no network.
//
// SCRIPT selects what the agent streams back, so one fixture covers both a
// single answer and the two-message case that receipt parsing has to survive.
const simulatedACPAgent = `#!/usr/bin/env python3
import json, os, sys

SCRIPT = os.environ.get("ACP_FIXTURE_SCRIPT", "single")
RECORD = os.environ.get("ACP_FIXTURE_RECORD", "")

def record(entry):
    if RECORD:
        with open(RECORD, "a") as f:
            f.write(json.dumps(entry) + "\n")

def send(payload):
    sys.stdout.write(json.dumps(payload) + "\n")
    sys.stdout.flush()

def reply(request_id, result):
    send({"jsonrpc": "2.0", "id": request_id, "result": result})

def notify(session, update):
    send({"jsonrpc": "2.0", "method": "session/update",
          "params": {"sessionId": session, "update": update}})

SELECTORS = [
    {"id": "model", "name": "Model", "category": "model", "type": "select",
     "currentValue": "fast", "options": [
         {"name": "Fast", "value": "fast"},
         {"name": "Careful", "value": "careful"}]},
    {"id": "reasoning_effort", "name": "Effort", "category": "thought_level", "type": "select",
     "currentValue": "low", "options": [
         {"name": "Low", "value": "low"},
         {"name": "Extra high", "value": "xhigh"}]},
]

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    message = json.loads(line)
    method, request_id = message.get("method"), message.get("id")
    if method is None:
        continue
    record({"method": method, "params": message.get("params")})
    if method == "initialize":
        reply(request_id, {"protocolVersion": 1,
                           "agentCapabilities": {"loadSession": False},
                           "authMethods": []})
    elif method == "session/new":
        reply(request_id, {"sessionId": "fixture-session", "configOptions": SELECTORS})
    elif method == "session/set_config_option":
        chosen = message["params"]["configId"]
        value = message["params"]["value"]
        for option in SELECTORS:
            if option["id"] == chosen:
                option["currentValue"] = value
        reply(request_id, {"configOptions": SELECTORS})
    elif method == "session/prompt":
        if SCRIPT == "two-messages":
            # A commentary message that does not end in a newline, then a
            # separate final message carrying the receipt.
            notify("fixture-session", {"sessionUpdate": "agent_message_chunk", "messageId": "m1",
                                       "content": {"type": "text", "text": "The work is complete."}})
            notify("fixture-session", {"sessionUpdate": "agent_message_chunk", "messageId": "m2",
                                       "content": {"type": "text", "text": 'ISSUE_RESULT {"status":"solved","title":"Fix the parser","detail":"Rewrote the receipt scan","tests":["go test ./..."]}'}})
        else:
            notify("fixture-session", {"sessionUpdate": "agent_thought_chunk",
                                       "content": {"type": "text", "text": "considering"}})
            notify("fixture-session", {"sessionUpdate": "agent_message_chunk", "messageId": "m1",
                                       "content": {"type": "text", "text": 'ISSUE_RESULT {"status":"blocked","detail":"The credentials are missing"}'}})
        reply(request_id, {"stopReason": "end_turn"})
    elif request_id is not None:
        reply(request_id, {})
`

func writeSimulatedAgent(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "acp-fixture-agent")
	if err := os.WriteFile(path, []byte(simulatedACPAgent), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// runSimulatedAgent drives agentProcess, the production path, against the
// fixture and returns the parsed receipt.
func runSimulatedAgent(t *testing.T, script, record string, model, effort string) (Result, error) {
	t.Helper()
	dir := t.TempDir()
	agent := writeSimulatedAgent(t, dir)
	workspace := filepath.Join(dir, "workspace")
	state := filepath.Join(dir, "state")
	for _, d := range []string{workspace, state} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	environment := map[string]string{"ACP_FIXTURE_SCRIPT": script}
	if record != "" {
		environment["ACP_FIXTURE_RECORD"] = record
	}
	cfg := Config{Directory: workspace, StateDirectory: state, Agent: runner.AgentConfig{
		Command: []string{agent}, Environment: environment, Model: model, Effort: effort,
	}}
	process := agentProcess{config: cfg, log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}
	return process.Execute(context.Background(), "implement the issue")
}

func TestACPSessionStartupSelectionAndReceipt(t *testing.T) {
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	result, err := runSimulatedAgent(t, "single", record, "careful", "xhigh")
	if err != nil {
		t.Fatalf("the real runner could not complete a session: %v", err)
	}
	if result.Status != "blocked" || result.Detail != "The credentials are missing" {
		t.Fatalf("receipt = %+v", result)
	}
	calls, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	transcript := string(calls)
	for _, want := range []string{`"initialize"`, `"session/new"`, `"session/prompt"`, `"careful"`, `"xhigh"`} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the session never sent %s:\n%s", want, transcript)
		}
	}
	// Selection is a request the agent answers, not a hint: both choices must
	// reach it before the prompt does.
	if strings.Index(transcript, `"careful"`) > strings.Index(transcript, `"session/prompt"`) {
		t.Fatalf("model was selected after the prompt:\n%s", transcript)
	}
}

func TestACPReceiptSurvivesASeparateCommentaryMessage(t *testing.T) {
	// BrokkAi/issue-bot#2: a valid final receipt was rejected when the agent
	// first emitted commentary with no trailing newline, because the two
	// messages were concatenated into one line.
	result, err := runSimulatedAgent(t, "two-messages", "", "", "")
	if err != nil {
		t.Fatalf("a valid receipt after commentary was rejected: %v", err)
	}
	if result.Status != "solved" || result.Title != "Fix the parser" || len(result.Tests) != 1 {
		t.Fatalf("receipt = %+v", result)
	}
}

func TestACPSessionWritesATranscript(t *testing.T) {
	dir := t.TempDir()
	agent := writeSimulatedAgent(t, dir)
	workspace, state := filepath.Join(dir, "workspace"), filepath.Join(dir, "state")
	for _, d := range []string{workspace, state} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{Directory: workspace, StateDirectory: state, Agent: runner.AgentConfig{
		Command: []string{agent}, Environment: map[string]string{"ACP_FIXTURE_SCRIPT": "single"},
	}}
	process := agentProcess{config: cfg, log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}
	if _, err := process.Execute(context.Background(), "implement the issue"); err != nil {
		t.Fatal(err)
	}
	sessions, err := filepath.Glob(filepath.Join(state, "sessions", "session-*.jsonl"))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %v (%v)", sessions, err)
	}
	body, err := os.ReadFile(sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"implement the issue", "agent_message_chunk", "session_end"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("transcript is missing %q", want)
		}
	}
}

func TestACPCancellationStopsTheSession(t *testing.T) {
	dir := t.TempDir()
	agent := writeSimulatedAgent(t, dir)
	workspace, state := filepath.Join(dir, "workspace"), filepath.Join(dir, "state")
	for _, d := range []string{workspace, state} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{Directory: workspace, StateDirectory: state, Agent: runner.AgentConfig{
		Command: []string{agent}, Environment: map[string]string{"ACP_FIXTURE_SCRIPT": "single"},
	}}
	process := agentProcess{config: cfg, log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := process.Execute(ctx, "implement the issue"); err == nil {
		t.Fatal("a canceled context still produced a receipt")
	}
}

func TestACPUnavailableModelIsReportedNotIgnored(t *testing.T) {
	// A model the agent does not advertise must fail the run rather than
	// silently fall back to the default.
	_, err := runSimulatedAgent(t, "single", "", "nonexistent-model", "")
	if err == nil {
		t.Fatal("an unavailable model was accepted")
	}
	if !strings.Contains(err.Error(), "nonexistent-model") {
		t.Fatalf("error does not name the rejected model: %v", err)
	}
	var setup *runner.SetupError
	if !errors.As(err, &setup) {
		t.Fatalf("a failure before the prompt is not a setup error: %v", err)
	}
}

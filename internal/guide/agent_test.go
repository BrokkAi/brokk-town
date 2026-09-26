package guide

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

func TestGuideACPNoToolsPermissionsAndPrivateDiagnostics(t *testing.T) {
	t.Setenv("GH_TOKEN", "private-gh-token")
	for _, script := range []string{"answer", "unsupported", "overflow", "hang"} {
		t.Run(script, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			record := filepath.Join(dir, "calls.jsonl")
			config := runner.AgentConfig{Command: []string{exe, "-test.run=^TestGuideAgentHelper$"}, Environment: map[string]string{"GUIDE_ACP_SCRIPT": script, "GUIDE_ACP_RECORD": record}, Mode: "bypassPermissions", Model: "careful", Effort: "high"}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if script == "hang" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
			}
			defer cancel()
			answer := ""
			err = Run(ctx, config, dir, "Explain only", func(chunk string) error { answer += chunk; return nil })
			raw, _ := os.ReadFile(record)
			if strings.Contains(string(raw), "private-gh-token") || strings.Contains(string(raw), "bypassPermissions") {
				t.Fatal("unsafe credentials/mode inherited", string(raw))
			}
			if script == "answer" {
				if err != nil || answer != "Permissions denied; no tools executed." {
					t.Fatal(answer, err, string(raw))
				}
				for _, want := range []string{`"modeId":"plan"`, `"value":"careful"`, `"value":"high"`, `"outcome":"cancelled"`, `"code":-32601`} {
					if !strings.Contains(string(raw), want) {
						t.Fatal("missing", want, string(raw))
					}
				}
				if strings.Contains(string(raw), `"terminal":true`) || strings.Contains(string(raw), `"writeTextFile":true`) {
					t.Fatal("advertised tools")
				}
			} else if err == nil {
				t.Fatal("unsafe or incomplete guide result accepted", script)
			}
			if script == "unsupported" && strings.Contains(string(raw), `"method":"session/prompt"`) {
				t.Fatal("prompted without read-only mode")
			}
			if len(answer) > MaxAnswer {
				t.Fatal("unbounded output")
			}
		})
	}
}

// A credential-free executable fixture speaks the real ACP wire protocol.
func TestGuideAgentHelper(t *testing.T) {
	script := os.Getenv("GUIDE_ACP_SCRIPT")
	if script == "" {
		return
	}
	f, err := os.OpenFile(os.Getenv("GUIDE_ACP_RECORD"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	defer f.Close()
	if token := os.Getenv("GH_TOKEN"); token != "" {
		f.WriteString(token)
	}
	enc := json.NewEncoder(os.Stdout)
	send := func(value any) {
		if enc.Encode(value) != nil {
			os.Exit(3)
		}
	}
	reply := func(id json.RawMessage, result any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	chunk := func(text string) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "guide-fixture", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
	}
	current := map[string]string{"model": "fast", "effort": "low"}
	selectors := func() []any {
		return []any{
			map[string]any{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": current["model"], "options": []any{map[string]string{"value": "fast", "name": "Fast"}, map[string]string{"value": "careful", "name": "Careful"}}},
			map[string]any{"id": "effort", "name": "Effort", "category": "thought_level", "type": "select", "currentValue": current["effort"], "options": []any{map[string]string{"value": "low", "name": "Low"}, map[string]string{"value": "high", "name": "High"}}},
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var prompt json.RawMessage
	currentMode := "bypassPermissions"
	for scanner.Scan() {
		f.Write(append(append([]byte(nil), scanner.Bytes()...), '\n'))
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(4)
		}
		switch m.Method {
		case "initialize":
			reply(m.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}, "authMethods": []any{}})
		case "session/new":
			result := map[string]any{"sessionId": "guide-fixture", "configOptions": selectors()}
			if script != "unsupported" {
				result["modes"] = map[string]any{"currentModeId": "bypassPermissions", "availableModes": []any{map[string]string{"id": "plan", "name": "Plan"}, map[string]string{"id": "bypassPermissions", "name": "Bypass"}}}
			}
			reply(m.ID, result)
		case "session/set_mode":
			currentMode, _ = m.Params["modeId"].(string)
			reply(m.ID, map[string]any{})
		case "session/set_config_option":
			currentMode = "bypassPermissions" // A changed model may reset the session mode.
			key := m.Params["configId"].(string)
			current[key] = m.Params["value"].(string)
			reply(m.ID, map[string]any{"configOptions": selectors()})
		case "session/prompt":
			prompt = m.ID
			if script == "answer" && currentMode != "plan" {
				chunk("Unsafe mode after model selection")
				reply(prompt, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if script == "hang" {
				continue
			}
			if script == "overflow" {
				chunk(strings.Repeat("x", MaxAnswer+1))
				reply(prompt, map[string]string{"stopReason": "end_turn"})
				continue
			}
			send(map[string]any{"jsonrpc": "2.0", "id": "permission", "method": "session/request_permission", "params": map[string]any{"sessionId": "guide-fixture", "toolCall": map[string]string{"toolCallId": "edit", "title": "Write a file"}, "options": []any{map[string]string{"optionId": "allow", "name": "Allow", "kind": "allow_always"}}}})
		case "":
			if string(m.ID) == `"permission"` {
				send(map[string]any{"jsonrpc": "2.0", "id": "file", "method": "fs/write_text_file", "params": map[string]string{"sessionId": "guide-fixture", "path": "/tmp/guide-must-not-write", "content": "bad"}})
			}
			if string(m.ID) == `"file"` {
				chunk("Permissions denied; no tools executed.")
				reply(prompt, map[string]string{"stopReason": "end_turn"})
			}
		default:
			reply(m.ID, map[string]any{})
		}
	}
	os.Exit(0)
}

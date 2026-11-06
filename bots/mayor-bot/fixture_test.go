package mayorbot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each bot owns its fixtures. These subprocesses speak the real gh/ACP
// boundaries but cannot contact GitHub or launch a coding harness.
type fixtureResponse struct {
	Body string
	Exit int
}
type fixtureGitHub struct {
	Routes map[string]fixtureResponse
}
type fixtureCall struct {
	Method, Endpoint string
	Args             []string
}
type fixturePrompt struct{ Directory, Text string }

func TestFixtureProcess(t *testing.T) {
	mode := os.Getenv("BOT_TEST_PROCESS")
	if mode == "" {
		return
	}
	if mode == "gh" {
		args := os.Args
		for len(args) > 0 && args[0] != "--" {
			args = args[1:]
		}
		args = args[1:]
		if len(args) < 4 || args[0] != "api" || args[1] != "--hostname" || args[2] != "github.com" {
			os.Exit(91)
		}
		endpoint := args[3]
		method := "GET"
		for i, a := range args {
			if a == "--method" && i+1 < len(args) {
				method = args[i+1]
			}
		}
		root := os.Getenv("BOT_TEST_GITHUB")
		appendFixture(filepath.Join(root, "calls"), fixtureCall{method, endpoint, args})
		var fixture fixtureGitHub
		readFixture(filepath.Join(root, "fixture.json"), &fixture)
		u, err := url.Parse(endpoint)
		if err != nil {
			os.Exit(92)
		}
		if method != "GET" {
			os.Exit(95)
		}
		response, ok := fixture.Routes[endpoint]
		if !ok {
			response, ok = fixture.Routes[u.Path]
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "unexpected endpoint", endpoint)
			os.Exit(96)
		}
		fmt.Print(response.Body)
		os.Exit(response.Exit)
	}
	if mode != "acp" {
		os.Exit(97)
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var message struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		if decoder.Decode(&message) != nil {
			os.Exit(0)
		}
		var result any = map[string]any{}
		switch message.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}
		case "session/new":
			result = map[string]any{"sessionId": "fixture"}
		case "session/prompt":
			var params struct{ Prompt []struct{ Text string } }
			if json.Unmarshal(message.Params, &params) != nil || len(params.Prompt) != 1 {
				os.Exit(98)
			}
			dir, _ := os.Getwd()
			appendFixture(os.Getenv("BOT_TEST_PROMPTS"), fixturePrompt{dir, params.Prompt[0].Text})
			switch os.Getenv("BOT_TEST_ACTION") {
			case "edit":
				if os.WriteFile("README.md", []byte("unauthorized edit\n"), 0600) != nil {
					os.Exit(99)
				}
			case "commit":
				cmd := exec.Command("git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "unauthorized commit")
				if cmd.Run() != nil {
					os.Exit(100)
				}
			case "wait":
				for {
					time.Sleep(time.Second)
				}
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": "fixture", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": os.Getenv("BOT_TEST_REPLY")}}}})
			result = map[string]any{"stopReason": "end_turn"}
		case "session/cancel":
			continue
		default:
			os.Exit(101)
		}
		if len(message.ID) > 0 {
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
		}
	}
}
func readFixture(path string, value any) {
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, value) != nil {
		os.Exit(102)
	}
}
func appendFixture(path string, value any) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(104)
	}
	if json.NewEncoder(f).Encode(value) != nil {
		os.Exit(105)
	}
	_ = f.Close()
}
func fixtureLines[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var result []T
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var v T
		if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		result = append(result, v)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func saveGitHubFixture(t *testing.T, root string, f fixtureGitHub) {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
func installFixture(t *testing.T, cfg *Config, reply string, f fixtureGitHub) string {
	t.Helper()
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport BOT_TEST_PROCESS=gh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestFixtureProcess$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BOT_TEST_GITHUB", root)
	// Even a broken recovery test must never fetch a real repository.
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "private-state"))
	saveGitHubFixture(t, root, f)
	cfg.GitHub.Repo = "o/r"
	cfg.Agent = AgentConfig{Command: []string{exe, "-test.run=^TestFixtureProcess$"}, Environment: map[string]string{
		"BOT_TEST_PROCESS": "acp", "BOT_TEST_REPLY": reply, "BOT_TEST_PROMPTS": filepath.Join(root, "prompts"),
	}}
	return root
}

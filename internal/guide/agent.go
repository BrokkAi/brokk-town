// Package guide runs a conversational ACP client without workspace tools or
// permission grants. Its caller supplies only a bounded, sanitized snapshot.
package guide

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/acp-go/schema"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

const MaxAnswer = 16 << 10

// Run never exposes filesystem, terminal or mutation capabilities to the agent.
// The configured executable remains trusted code; ACP is not an OS sandbox.
func Run(ctx context.Context, config runner.AgentConfig, directory, prompt string, emit func(string) error) error {
	if len(config.Command) == 0 {
		return errors.New("guide harness is not configured")
	}
	cmd := osrun.StartCommand(ctx, directory, config.Command, config.Environment)
	// GitHub credentials belong to execution bots, not this conversation. Keep
	// harness login and provider variables, but never inherit Town/GitHub tokens.
	env := []string{}
	for _, entry := range cmd.Env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GH_") || strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "BROKK_TOWN_") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = env
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer out.Close()
	if err = cmd.Start(); err != nil {
		return errors.New("could not launch guide harness")
	}
	defer func() { _ = osrun.Kill(cmd); _ = cmd.Wait() }()
	var mu sync.Mutex
	total := 0
	var sessionID acp.SessionID
	prompting := false
	connection := acp.Connect(out, in, deny, acp.SessionUpdates(func(update acp.Update) error {
		chunk := update.Update.AgentMessageChunk
		if chunk == nil || chunk.Content.Text == nil {
			return nil
		}
		text := chunk.Content.Text.Text
		mu.Lock()
		defer mu.Unlock()
		if !prompting || update.SessionID != sessionID {
			return errors.New("guide received an unexpected session answer")
		}
		total += len(text)
		if total > MaxAnswer {
			return errors.New("guide answer exceeded its limit")
		}
		return emit(text)
	}))
	defer connection.Close()
	caps := acp.Capabilities{}
	caps.Session = acp.ConfigOptionsClientCapabilities(true)
	init, err := connection.InitializeWithInfo(ctx, caps, acp.ClientInfo{Name: "brokk-town-guide", Version: "1"})
	if err != nil {
		return err
	}
	if config.AuthMethod != "" {
		if err = connection.Authenticate(ctx, init, config.AuthMethod); err != nil {
			return err
		}
	}
	session, err := connection.NewSession(ctx, directory)
	if err != nil {
		return err
	}
	if config.Model != "" {
		if err = connection.SetModel(ctx, &session, config.Model); err != nil {
			return err
		}
	}
	if config.Effort != "" {
		if err = connection.SetEffort(ctx, &session, config.Effort); err != nil {
			return err
		}
	}
	// A configured execution mode might bypass approvals. A guide must select an
	// advertised read-only mode itself and fail before prompting if unavailable.
	// Select it after model/effort: changing those can reset session modes.
	mode := ""
	if session.Modes != nil {
		for _, candidate := range session.Modes.AvailableModes {
			switch string(candidate.ID) {
			case "read-only", "readonly", "plan":
				mode = string(candidate.ID)
			}
			if mode != "" {
				break
			}
		}
	}
	if mode == "" {
		return errors.New("guide harness must advertise a read-only or plan mode")
	}
	if err = connection.SetMode(ctx, &session, mode); err != nil {
		return err
	}
	mu.Lock()
	sessionID = session.SessionID
	prompting = true
	mu.Unlock()
	reason, err := connection.Prompt(ctx, session, prompt)
	if err != nil {
		return err
	}
	if reason != schema.StopReasonEndTurn {
		return errors.New("guide answer did not finish")
	}
	return nil
}

func deny(_ context.Context, method string, _ json.RawMessage) (any, error) {
	if method == "session/request_permission" {
		return map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}, nil
	}
	return nil, &acp.RPCError{Code: -32601, Message: "Town Guide has no tools or write capabilities"}
}

// Workspace contains no repository, private state or credentials.
func Workspace(root string) (string, error) { return os.MkdirTemp(root, "guide-context-") }

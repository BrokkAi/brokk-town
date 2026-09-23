package town

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	acp "github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/clienthost"
	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/acp-go/schema"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// executeACP follows acp-go v0.8.1 runner/runner.go (Apache-2.0, Copyright
// 2026 Brokk.ai and contributors; originally part of BrokkAi/release-bot), as
// feature-bot's agentProcess does. runner.Execute has no hook between model
// and effort selection, and its SetEffort dropped v0.1.0's fallback to an
// uncategorized thought_level option; setEffort restores it so a run selects
// the same effort option the choice probe lists. Keep the lifecycle aligned
// with the upstream runner otherwise.
func executeACP(ctx context.Context, cfg runner.Config, log *slog.Logger, prompt string) (result string, runErr error) {
	if log == nil {
		log = slog.Default()
	}
	if len(cfg.Agent.Command) == 0 || cfg.Agent.Command[0] == "" {
		return "", &runner.SetupError{Err: errors.New("agent command is required")}
	}
	promptStarted := false
	defer func() {
		if runErr != nil && !promptStarted {
			runErr = &runner.SetupError{Err: runErr}
		}
	}()
	dir := filepath.Join(cfg.StateDirectory, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return result, err
	}
	transcript, err := os.CreateTemp(dir, "session-*.jsonl")
	if err != nil {
		return result, err
	}
	defer transcript.Close()
	host, err := clienthost.Open(ctx, clienthost.Config{Directory: cfg.Directory, Transcript: transcript, Logger: log})
	if err != nil {
		return result, err
	}
	defer host.Close()
	host.SetAutoApprove(cfg.AutoApprove)
	log.Info("Starting agent", "command", strings.Join(cfg.Agent.Command, " "), "transcript", transcript.Name())
	cmd := osrun.StartCommand(context.Background(), cfg.Directory, cfg.Agent.Command, cfg.Agent.Environment)
	diagnostics := &osrun.Tail{Capacity: 64 << 10}
	cmd.Stderr = io.MultiWriter(diagnostics, host.ProcessWriter("Agent stderr", ""))
	in, err := cmd.StdinPipe()
	if err != nil {
		return result, err
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return result, err
	}
	defer out.Close()
	if err := cmd.Start(); err != nil {
		return result, fmt.Errorf("launch ACP agent: %w", err)
	}
	defer func() {
		_ = osrun.Kill(cmd)
		_ = cmd.Wait()
		text, _ := diagnostics.Text()
		_ = host.Record(map[string]string{"stderr": text})
		if runErr != nil && text != "" {
			runErr = fmt.Errorf("%w\nAgent diagnostics: %s", runErr, text)
		}
	}()
	connection := acp.Connect(out, in, host.Request, host.Notification)
	phase := "initialize"
	started := time.Now()
	defer func() {
		record := map[string]any{"event": "session_end", "phase": phase, "elapsed": time.Since(started).String()}
		if runErr != nil {
			record["error"] = runErr.Error()
		}
		if cause := context.Cause(ctx); cause != nil {
			record["context_cause"] = cause.Error()
		}
		if err := connection.Err(); err != nil {
			record["transport_error"] = err.Error()
		}
		_ = host.Record(record)
		_ = connection.Close()
	}()
	capabilities := acp.WorkspaceCapabilities(true, true, true)
	capabilities.Session = acp.ConfigOptionsClientCapabilities(true)
	init, err := connection.InitializeWithInfo(ctx, capabilities, cfg.ClientInfo)
	if err != nil {
		return result, err
	}
	if cfg.Agent.AuthMethod != "" {
		phase = "authenticate"
		if err := connection.Authenticate(ctx, init, cfg.Agent.AuthMethod); err != nil {
			return result, err
		}
	}
	phase = "session/new"
	session, err := connection.NewSession(ctx, cfg.Directory)
	if err != nil {
		return result, fmt.Errorf("create ACP session (check agent login): %w", err)
	}
	host.SetSession(session.SessionID)
	if cfg.Agent.Mode != "" {
		phase = "select mode"
		if err := connection.SetMode(ctx, &session, cfg.Agent.Mode); err != nil {
			return result, err
		}
	}
	if cfg.Agent.Model != "" {
		phase = "select model"
		if err := connection.SetModel(ctx, &session, cfg.Agent.Model); err != nil {
			return result, err
		}
		log.Info("Using model", "model", cfg.Agent.Model)
	}
	if cfg.Agent.Effort != "" {
		phase = "select effort"
		if err := setEffort(ctx, connection, &session, cfg.Agent.Effort); err != nil {
			return result, err
		}
		log.Info("Using reasoning effort", "effort", cfg.Agent.Effort)
	}
	log.Info("agent session", "id", session.SessionID, "transcript", transcript.Name())
	if err := host.Record(map[string]string{"prompt": prompt}); err != nil {
		return result, err
	}
	phase = "session/prompt"
	promptStarted = true
	reason, err := connection.Prompt(ctx, session, prompt)
	if err != nil {
		return result, err
	}
	if reason != schema.StopReasonEndTurn {
		return result, fmt.Errorf("agent stopped with %s", reason)
	}
	if logErr := host.LogErr(); logErr != nil {
		return result, logErr
	}
	text, truncated := host.Answer()
	if truncated {
		return "", errors.New("agent answer exceeded 2 MiB")
	}
	return text, nil
}

// selectOption finds a select option the way acp-go v0.1.0 did: the first
// with the category, else the first uncategorized one with that ID.
func selectOption(options []schema.SessionConfigOption, name string) *schema.SessionConfigOption {
	for i := range options {
		if o := &options[i]; o.Select != nil && o.Category != nil && string(*o.Category) == name {
			return o
		}
	}
	for i := range options {
		if o := &options[i]; o.Select != nil && o.Category == nil && string(o.ID) == name {
			return o
		}
	}
	return nil
}

func modelOption(options []schema.SessionConfigOption) *schema.SessionConfigOption {
	return selectOption(options, "model")
}

// effortOption keeps acp-go v0.1.0's order: thought_level, then Codex's
// reasoning_effort. acp-go v0.8.1 SetEffort only honors the thought_level
// category and an uncategorized reasoning_effort ID.
func effortOption(options []schema.SessionConfigOption) *schema.SessionConfigOption {
	if o := selectOption(options, "thought_level"); o != nil {
		return o
	}
	return selectOption(options, "reasoning_effort")
}

// setEffort selects effort on the option effortOption finds. acp-go still
// validates the value and requires the agent to acknowledge it under the
// option's wire ID; only a copy is tagged, so advertised data is unchanged.
func setEffort(ctx context.Context, connection *acp.Connection, session *acp.Session, effort string) error {
	option := effortOption(session.ConfigOptions)
	if option == nil || (option.Category != nil && *option.Category == schema.SessionConfigOptionCategoryThoughtLevel) {
		return connection.SetEffort(ctx, session, effort)
	}
	compatible := *session
	compatible.ConfigOptions = append([]schema.SessionConfigOption(nil), session.ConfigOptions...)
	for i := range compatible.ConfigOptions {
		if compatible.ConfigOptions[i].ID == option.ID {
			category := schema.SessionConfigOptionCategoryThoughtLevel
			compatible.ConfigOptions[i].Category = &category
			break
		}
	}
	err := connection.SetEffort(ctx, &compatible, effort)
	if err == nil {
		// The agent's acknowledged options replaced the tagged copy.
		session.ConfigOptions = compatible.ConfigOptions
	}
	return err
}

// selectValues flattens a select option's values exactly as acp-go does when
// selecting one: the first entry decides between flat and grouped values.
func selectValues(option *schema.SessionConfigOption) ([]ChoiceValue, error) {
	out := []ChoiceValue{}
	if option == nil || option.Select.Options == nil {
		return out, nil
	}
	encoded, err := json.Marshal(option.Select.Options)
	if err != nil {
		return nil, err
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &items); err != nil || len(items) == 0 {
		return out, err
	}
	var flat []schema.SessionConfigSelectOption
	if _, grouped := items[0]["group"]; grouped || json.Unmarshal(encoded, &flat) != nil {
		var groups []schema.SessionConfigSelectGroup
		if err := json.Unmarshal(encoded, &groups); err != nil {
			return nil, fmt.Errorf("unsupported %s options: %w", option.Name, err)
		}
		flat = nil
		for _, group := range groups {
			flat = append(flat, group.Options...)
		}
	}
	for _, value := range flat {
		out = append(out, ChoiceValue{Value: string(value.Value), Name: value.Name})
	}
	return out, nil
}

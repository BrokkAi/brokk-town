package town

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	acp "github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// Pointer fields distinguish preserving a setting from resetting it to default.
type AgentSettings struct {
	Harness *string   `json:"harness,omitempty"`
	Model   *string   `json:"model,omitempty"`
	Effort  *string   `json:"effort,omitempty"`
	Command *[]string `json:"command,omitempty"`
}

func (c Config) harness() string {
	if c.Harness != "" {
		return c.Harness
	}
	if len(c.Agent.Command) > 0 {
		return "custom"
	}
	return "codex"
}

func validateAgent(c Config) error {
	switch c.harness() {
	case "codex", "claude", "gemini":
	case "custom":
		if len(c.Agent.Command) == 0 || strings.TrimSpace(c.Agent.Command[0]) == "" {
			return errors.New("custom harness requires an ACP command")
		}
	default:
		return errors.New("harness must be codex, claude, gemini, or custom")
	}
	for _, v := range append([]string{c.Agent.Model, c.Agent.Effort}, c.Agent.Command...) {
		if len(v) > 4096 || strings.ContainsAny(v, "\x00\r\n") {
			return errors.New("invalid agent setting")
		}
	}
	return nil
}

func (a AgentSettings) Apply(c Config) (Config, error) {
	if a.Harness != nil {
		if *a.Harness != c.harness() {
			// Authentication and mode belong to the selected harness.
			c.Agent = runner.AgentConfig{}
		}
		c.Harness = *a.Harness
	}
	if a.Command != nil {
		if c.harness() != "custom" {
			return c, errors.New("command is only supported for a custom harness")
		}
		c.Agent.Command = append([]string{}, (*a.Command)...)
	}
	if a.Model != nil {
		c.Agent.Model = strings.TrimSpace(*a.Model)
	}
	if a.Effort != nil {
		c.Agent.Effort = strings.TrimSpace(*a.Effort)
	}
	return c, c.Validate()
}

func agentConfig(c Config) runner.AgentConfig {
	a := c.Agent
	if len(a.Command) != 0 {
		return a
	}
	var binary, pkg string
	var args []string
	switch c.harness() {
	case "claude":
		binary, pkg = "claude-agent-acp", "@agentclientprotocol/claude-agent-acp"
	case "gemini":
		binary, pkg, args = "gemini", "@google/gemini-cli", []string{"--acp"}
	default:
		binary, pkg = "codex-acp", "@agentclientprotocol/codex-acp"
	}
	if p, err := exec.LookPath(binary); err == nil {
		a.Command = append([]string{p}, args...)
	} else {
		a.Command = append([]string{"npx", "--yes", pkg}, args...)
	}
	return a
}

func (s *Supervisor) Add(c Config) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strings.ToLower(c.Repo)
	for key := range s.running {
		if strings.HasPrefix(key, id+":") {
			return "", errors.New("town workers are still stopping; try again shortly")
		}
	}
	err := s.Store.Update(func(st *State) error {
		if st.Demo {
			return errors.New("use a live service to add real repositories")
		}
		t, err := st.Add(c)
		if err != nil {
			return err
		}
		st.Event(t.ID, "town", "operator", "repo", "", "Town established; reporter is checking the repository", s.now())
		return nil
	})
	return id, err
}

func (s *Supervisor) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil {
			return errors.New("unknown town")
		}
		t.Deleted = true
		for _, w := range t.Workers {
			w.Enabled, w.Status = false, "paused"
		}
		for _, r := range t.Requests {
			if r.Status == "queued" {
				r.Status = "canceled"
				r.Detail = "Submission canceled when the town was deleted."
			}
		}
		st.Event(id, "town", "operator", "hall", "", "Town deleted; recovery records retained", s.now())
		return nil
	}); err != nil {
		return err
	}
	for key, cancel := range s.running {
		if strings.HasPrefix(key, id+":") {
			cancel()
		}
	}
	return nil
}

func (s *Supervisor) Settings(id string, settings AgentSettings) error {
	return s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		cfg, err := settings.Apply(t.Config)
		if err != nil {
			return err
		}
		t.Config = cfg
		st.Event(id, "settings", "operator", "hall", "", "Agent settings saved for the next worker run", s.now())
		return nil
	})
}

type AgentChoices struct {
	Models  []acp.ConfigValue `json:"models"`
	Efforts []acp.ConfigValue `json:"efforts"`
}

func choices(session acp.Session) AgentChoices {
	out := AgentChoices{Models: []acp.ConfigValue{}, Efforts: []acp.ConfigValue{}}
	for _, o := range session.ConfigOptions {
		if o.Type != "select" {
			continue
		}
		category := o.Category
		if category == "" {
			category = o.ID
		}
		switch category {
		case "model":
			out.Models = append(out.Models, o.Options...)
		case "thought_level", "reasoning_effort":
			out.Efforts = append(out.Efforts, o.Options...)
		}
	}
	return out
}

// A discovery session never sends a prompt, exposes client tools or uses a
// repository worktree. It only asks the harness for its session selectors.
func ProbeAgent(ctx context.Context, cfg Config) (AgentChoices, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "brokk-town-choices-*")
	if err != nil {
		return AgentChoices{}, err
	}
	defer os.RemoveAll(dir)
	a := agentConfig(cfg)
	cmd := osrun.StartCommand(ctx, dir, a.Command, a.Environment)
	cmd.Stderr = &osrun.Tail{Capacity: 16 << 10}
	in, err := cmd.StdoutPipe()
	if err != nil {
		return AgentChoices{}, err
	}
	out, err := cmd.StdinPipe()
	if err != nil {
		in.Close()
		return AgentChoices{}, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return AgentChoices{}, errors.New("could not start harness; check its installation")
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	c := acp.Connect(in, out, nil, nil)
	defer c.Close()
	init, err := c.InitializeWithInfo(ctx, acp.Capabilities{}, acp.ClientInfo{Name: "brokk-town", Version: "dev"})
	if err != nil {
		return AgentChoices{}, errors.New("harness initialization failed; check its installation and login")
	}
	if a.AuthMethod != "" {
		if err = c.Authenticate(ctx, init, a.AuthMethod); err != nil {
			return AgentChoices{}, errors.New("harness authentication failed; complete login in your terminal")
		}
	}
	session, err := c.NewSession(ctx, dir)
	if err != nil {
		return AgentChoices{}, errors.New("could not open a harness session; complete login in your terminal")
	}
	if a.Mode != "" {
		if err = c.SetMode(ctx, &session, a.Mode); err != nil {
			return AgentChoices{}, errors.New("harness rejected its configured mode")
		}
	}
	if a.Model != "" {
		if err = c.SetModel(ctx, &session, a.Model); err != nil {
			return AgentChoices{}, fmt.Errorf("model selection: %w", err)
		}
	}
	return choices(session), nil
}

func (s *Supervisor) Choices(ctx context.Context, id string, settings AgentSettings) (AgentChoices, error) {
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return AgentChoices{}, ctx.Err()
	}
	t := s.Store.Snapshot().Towns[id]
	if t == nil || t.Deleted {
		s.mu.Unlock()
		return AgentChoices{}, errors.New("unknown town")
	}
	cfg, err := settings.Apply(t.Config)
	if err != nil {
		s.mu.Unlock()
		return AgentChoices{}, err
	}
	if s.Store.Snapshot().Demo {
		s.mu.Unlock()
		return AgentChoices{Models: []acp.ConfigValue{{Value: "demo-model", Name: "Demo model"}}, Efforts: []acp.ConfigValue{{Value: "low", Name: "Low"}, {Value: "high", Name: "High"}}}, nil
	}
	key := id + ":choices"
	if s.running[key] != nil {
		s.mu.Unlock()
		return AgentChoices{}, errors.New("already loading harness choices")
	}
	ctx, cancel := context.WithCancel(ctx)
	s.running[key] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	defer func() { cancel(); s.mu.Lock(); delete(s.running, key); s.mu.Unlock() }()
	return ProbeAgent(ctx, cfg)
}

package town

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

const executionPending = "Mjolnir execution is not available yet; remote checkout and evidence support are still required. Select direct local execution to run this bot here."

// ExecutionForRole resolves placement independently of the agent profile.
// A missing override inherits; an explicit empty selection runs directly here.
func (c Config) ExecutionForRole(role Role) mjolnir.Selection {
	if s, ok := c.BotExecution[role]; ok {
		return s
	}
	if c.Execution != nil {
		return *c.Execution
	}
	return mjolnir.Selection{}
}

func (c Config) validateExecution() error {
	if c.Execution != nil {
		if err := c.Execution.Validate(); err != nil {
			return err
		}
	}
	for r, s := range c.BotExecution {
		if !ValidAgentRole(r) {
			return errors.New("invalid execution role")
		}
		if err := s.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// SetExecution saves only catalog references. A nil selection resets the town
// to direct local execution, or removes a bot override. Stale options can be
// selected: availability is advisory, never authority to launch work.
func (s *Supervisor) SetExecution(id string, role Role, selection *mjolnir.Selection) error {
	id = strings.ToLower(id)
	if role != "" && !ValidAgentRole(role) {
		return errors.New("invalid execution role")
	}
	if selection != nil {
		if err := selection.Validate(); err != nil {
			return err
		}
		selection = clone(selection)
	}
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		if selection != nil && selection.Managed() && *selection != t.Config.ExecutionForRole(role) && !s.Mjolnir.Contains(*selection) {
			return errors.New("select a target and profile from Mjolnir's cached launch options; refresh the catalog if they are missing")
		}
		if role == "" {
			t.Config.Execution = selection
		} else if selection == nil {
			delete(t.Config.BotExecution, role)
		} else {
			if t.Config.BotExecution == nil {
				t.Config.BotExecution = map[Role]mjolnir.Selection{}
			}
			t.Config.BotExecution[role] = *selection
		}
		for _, r := range AgentRoles {
			w := t.Workers[r]
			if w.Phase == "execution" && !t.Config.ExecutionForRole(r).Managed() {
				w.Phase, w.Task = "", "Ready when you are"
				w.Next = time.Time{}
			}
		}
		target := string(role)
		if target == "" {
			target = "hall"
		}
		st.Event(id, "settings", "operator", target, "", "Execution selection saved for future work", s.now())
		return nil
	})
	if err == nil {
		s.notifyScheduler()
	}
	return err
}

// Until the remote workspace/evidence contract is implemented, a selected
// Mjolnir target must never fall back to a local harness, writes, or a merge.
func requireLocalExecution(c Config) error {
	if c.Execution != nil && c.Execution.Managed() {
		return fmt.Errorf("target %s: %s", c.Execution.Target, executionPending)
	}
	return nil
}

package town

import (
	"context"
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
	for key, pin := range c.ExecutionRuntimes {
		if pin.Validate() != nil || key != runtimeSelectionKey(pin.Source.Selection) {
			return errors.New("invalid saved Mjolnir runtime selection")
		}
	}
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

func runtimeSelectionKey(selection mjolnir.Selection) string {
	return Key(selection.Target + "\x00" + selection.Profile)
}

func (c Config) ExecutionRuntimeForRole(role Role) *mjolnir.RuntimePin {
	selection := c.ExecutionForRole(role)
	if !selection.Managed() {
		return nil
	}
	pin, ok := c.ExecutionRuntimes[runtimeSelectionKey(selection)]
	if !ok || pin.Source.Selection != selection {
		return nil
	}
	return clone(&pin)
}

// SelectExecutionRuntime is an explicit operator update, never a background
// catalog refresh. The network read happens outside the store/render lock;
// compare the selection again before saving so a slow read cannot pin a new
// placement accidentally. Existing dispatches keep their copied receipts.
func (s *Supervisor) SelectExecutionRuntime(ctx context.Context, id string, role Role, session string) error {
	id = strings.ToLower(id)
	if role != "" && !ValidAgentRole(role) {
		return errors.New("invalid execution role")
	}
	snapshot := s.Store.Snapshot()
	if snapshot.Demo {
		return errors.New("Mjolnir runtime selection is offline in demo mode")
	}
	t := snapshot.Towns[id]
	if t == nil || t.Deleted {
		return errors.New("unknown town")
	}
	selection := t.Config.ExecutionForRole(role)
	pin, err := s.Mjolnir.DiscoverRuntime(ctx, selection, session)
	if err != nil {
		return err
	}
	err = s.Store.Update(func(st *State) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := st.Towns[id]
		if current == nil || current.Deleted || current.Config.ExecutionForRole(role) != selection {
			return errors.New("execution selection changed while reading runtime; select it again")
		}
		if current.Config.ExecutionRuntimes == nil {
			current.Config.ExecutionRuntimes = map[string]mjolnir.RuntimePin{}
		}
		current.Config.ExecutionRuntimes[runtimeSelectionKey(selection)] = pin
		st.Event(id, "settings", "operator", "hall", "", "Mjolnir runtime selected for future work", s.now())
		return nil
	})
	if err == nil {
		s.notifyScheduler()
	}
	return err
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

package featurebot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/runner"
)

type Agent interface {
	Execute(context.Context, string) (string, error)
}
type agentProcess struct {
	config Config
	log    *slog.Logger
	// stage is "discovery" or "review"; it names the setting to fix when the
	// adapter rejects a model or effort selection.
	stage string
}

// Execute runs one prompt through acp-go's runner and names the setting behind
// a failed model or effort selection.
func (a agentProcess) Execute(ctx context.Context, prompt string) (string, error) {
	answer, err := runner.Runner{Config: runner.Config{
		Directory:      a.config.Directory,
		StateDirectory: a.config.StateDirectory,
		Agent:          a.config.Agent,
		ClientInfo:     acp.ClientInfo{Name: "feature-bot", Version: "0.1.0"},
		AutoApprove:    true,
	}, Log: a.log}.Execute(ctx, prompt)
	return answer, a.selectionError(err)
}

// selectionError names the setting behind a failed model or effort selection.
// Only a value missing from the adapter's advertised choices
// (acp.UnknownSelectionError) is described as not accepted; unsupported or
// unconfirmed selection, transport and cancellation failures are reported as
// failures to select. Errors from other phases are returned unchanged.
func (a agentProcess) selectionError(err error) error {
	var setup *runner.SetupError
	if !errors.As(err, &setup) {
		return err
	}
	var kind, value string
	switch setup.Phase {
	case runner.PhaseSelectModel:
		kind, value = "model", a.config.Agent.Model
	case runner.PhaseSelectEffort:
		kind, value = "effort", a.config.Agent.Effort
	default:
		return err
	}
	setting := map[string]string{"model": "--model or agent.model", "effort": "--effort or agent.effort"}[kind]
	if a.stage == "review" {
		setting = map[string]string{"model": "--review-model or review_model", "effort": "--review-effort or review_effort"}[kind]
	}
	stage := a.stage
	if stage == "" {
		stage = "agent"
	}
	var unknown *acp.UnknownSelectionError
	if errors.As(setup.Err, &unknown) {
		return &runner.SetupError{Phase: setup.Phase, Err: fmt.Errorf("%s %s %q was not accepted by the ACP adapter; choose a value it offers with %s: %w", stage, kind, value, setting, setup.Err)}
	}
	return &runner.SetupError{Phase: setup.Phase, Err: fmt.Errorf("failed to select %s %s %q (set with %s): %w", stage, kind, value, setting, setup.Err)}
}

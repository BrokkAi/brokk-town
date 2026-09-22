package mayorbot

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"time"

	"github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/runner"
)

type Agent interface {
	Execute(context.Context, string) (string, error)
}
type agentProcess struct {
	config Config
	log    *slog.Logger
}

func (a agentProcess) Execute(ctx context.Context, prompt string) (string, error) {
	r := runner.Runner{Config: runner.Config{
		Directory: a.config.Directory, StateDirectory: a.config.StateDirectory, Agent: a.config.Agent,
		AutoApprove: true, ClientInfo: acp.ClientInfo{Name: "mayor-bot", Version: "0.1.0"},
	}, Log: a.log}
	return r.Execute(ctx, prompt)
}

var startupRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second}

// execute runs one agent session, retrying only the agent's own startup
// failures. A missing agent command is reported at once.
func execute(ctx context.Context, a Agent, prompt string, log *slog.Logger, sleep func(context.Context, time.Duration) error) (string, error) {
	for i := 0; ; i++ {
		text, err := a.Execute(ctx, prompt)
		var setup *runner.SetupError
		if err == nil || !errors.As(err, &setup) || errors.Is(err, exec.ErrNotFound) || i >= len(startupRetryDelays) || ctx.Err() != nil {
			return text, err
		}
		delay := startupRetryDelays[i]
		log.Warn("Agent failed to start; retrying", "error", err, "delay", delay)
		if sleepErr := sleep(ctx, delay); sleepErr != nil {
			return "", errors.Join(err, sleepErr)
		}
	}
}
func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

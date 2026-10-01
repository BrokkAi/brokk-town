package town

import (
	"context"
	"errors"
)

// errStopWorker cancels an in-process bot dispatch without treating the run as
// a failure.
var errStopWorker = errors.New("worker stop requested")

func stopRequested(ctx context.Context) bool { return errors.Is(context.Cause(ctx), errStopWorker) }

// WorkerInterruptedError means a bot run ended without a trustworthy outcome,
// so Town must reconcile its saved result before dispatching it again.
type WorkerInterruptedError struct{ Err error }

func (e *WorkerInterruptedError) Error() string {
	return "worker outcome is uncertain: " + e.Err.Error()
}
func (e *WorkerInterruptedError) Unwrap() error { return e.Err }

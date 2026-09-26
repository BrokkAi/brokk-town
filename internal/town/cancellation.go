package town

import (
	"context"
	"errors"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// Keep the worker's explanation without changing cancellation's error identity
// or treating it as proof that an interrupted write did not land.
type workerCanceledError struct{ detail string }

func (e *workerCanceledError) Error() string { return "worker reported cancellation: " + e.detail }
func (e *workerCanceledError) Unwrap() error { return context.Canceled }

func cancellationDetail(ctx context.Context, err error, phase string) string {
	var detail string
	cause := context.Cause(ctx)
	var reported *workerCanceledError
	switch {
	case stopRequested(ctx):
		detail = "Worker attempt canceled by operator stop"
	case cause != nil && cause != context.Canceled:
		detail = "Worker attempt canceled because Town's service context ended: " + cancellationText(cause.Error(), 2048)
	case cause != nil:
		detail = "Worker attempt canceled because Town's service context ended; cause was not provided"
	case errors.As(err, &reported):
		detail = "Worker reported cancellation while Town's dispatch context remained active: " + cancellationText(reported.detail, 2048)
	default:
		detail = "Worker attempt returned cancellation while Town's dispatch context remained active; cause was not provided"
	}
	if phase = strings.TrimSpace(phase); phase != "" {
		detail += "; last phase: " + cancellationText(phase, 128)
	} else {
		detail += "; no worker phase was reported"
	}
	return detail
}

func cancellationText(text string, limit int) string {
	return osrun.Clip(credentialText.ReplaceAllString(text, "[redacted]"), limit)
}

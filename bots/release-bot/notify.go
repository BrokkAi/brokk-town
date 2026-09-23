package releasebot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"time"

	"github.com/BrokkAi/release-bot/internal/osrun"
)

// NotificationVersion identifies the NotificationEvent schema.
const NotificationVersion = 1

// NotificationEvent is the JSON document written to the notify command's
// stdin. It carries identifiers only: no transcripts, environment values,
// configuration, local paths or failure output. Job is stable for a pending
// release across restarts, so consumers can deduplicate repeated events.
type NotificationEvent struct {
	Version int    `json:"version"`
	Event   string `json:"event"` // "verified" or "exhausted"
	// Time is when the daemon emitted the event, in UTC.
	Time time.Time `json:"time"`
	// Repository is the GitHub owner/repository, when known.
	Repository string `json:"repository,omitempty"`
	Branch     string `json:"branch"`
	Job        string `json:"job"`
	// Release identifies a verified release; it is empty for exhausted jobs.
	Release string `json:"release,omitempty"`
	// Target is the watched-branch commit the job was started for.
	Target string `json:"target"`
	// Commit and Tag are the verified release, or the prepared plan when known.
	Commit      string `json:"commit,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"max_attempts"`
}

// Bounded diagnostics from the notify command; stdout is discarded.
const notifyOutputLimit = 4 << 10

func (e *engine) notificationFor(event string, j *Job) NotificationEvent {
	n := NotificationEvent{Version: NotificationVersion, Event: event, Time: e.now().UTC(),
		Repository: e.config.GitHubRepo(), Branch: e.config.Branch, Job: jobID(j),
		Target: j.Target, Attempts: j.Tries, MaxAttempts: e.config.Attempts}
	if j.Plan != nil {
		n.Commit, n.Tag = j.Plan.Commit, j.Plan.Tag
	}
	return n
}

// jobID matches the pending release identifier used by progress displays.
func jobID(j *Job) string { return "job:" + j.WorkBranch + ":" + j.Target }

// notify runs the configured command best effort. Failures are logged and
// returned for tests; callers never let them change the release outcome.
func (e *engine) notify(ctx context.Context, event NotificationEvent) error {
	if len(e.config.Notify) == 0 {
		return nil
	}
	err := e.runNotify(ctx, event)
	if err != nil {
		e.log.Warn("Notification failed; release outcome unchanged", "event", event.Event, "job", event.Job, "error", err)
	} else {
		e.log.Info("Notification delivered", "event", event.Event, "job", event.Job)
	}
	return err
}

var (
	errNotifyMissing = errors.New("notify command not found")
	errNotifyTimeout = errors.New("notify command timed out")
	errNotifyExit    = errors.New("notify command exited unsuccessfully")
)

func (e *engine) runNotify(ctx context.Context, event NotificationEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.config.NotifyTimeout))
	defer cancel()
	// Literal arguments, outside the managed checkout.
	cmd := osrun.StartCommand(ctx, e.config.StateDirectory, e.config.Notify, nil)
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	cmd.Stdout = io.Discard
	stderr := &osrun.Tail{Capacity: notifyOutputLimit}
	cmd.Stderr = stderr
	err = cmd.Run()
	detail, _ := stderr.Text()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: %s", errNotifyMissing, e.config.Notify[0])
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%w after %s", errNotifyTimeout, time.Duration(e.config.NotifyTimeout))
	case ctx.Err() != nil:
		return fmt.Errorf("notify command cancelled: %w", ctx.Err())
	case errors.As(err, &exit):
		return fmt.Errorf("%w (status %d): %s", errNotifyExit, exit.ExitCode(), detail)
	default:
		return fmt.Errorf("notify command: %w", err)
	}
}

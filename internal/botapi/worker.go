package botapi

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/acp-go/runner"

	"github.com/BrokkAi/brokk-town/internal/artifact"
	"github.com/BrokkAi/brokk-town/internal/harness"
)

// Job is one request as a bot's handler receives it: the workspace and agent
// already resolved in the bot's own environment.
type Job struct {
	Request
	Directory      string
	StateDirectory string
	// Launch is the agent to start, resolved from Request.Agent. It is empty
	// when the request selected no agent.
	Launch runner.AgentConfig
	// Log carries the bot's diagnostics to the service as Log events.
	Log *slog.Logger
	// Progress reports the bot's phase and task to the service.
	Progress func(phase, task string)
}

// Handler performs one job for one bot.
type Handler func(context.Context, Job) (Result, error)

// Worker is the side of the contract a bot runs. It owns everything that
// depends on where the bot runs, so a handler only maps a job to its bot's
// configuration and back.
type Worker struct {
	// Name is the bot's name, such as "bug-bot"; Role is the role key it
	// serves, such as "bug".
	Name, Role string
	// Root is the bot's data root. Workspaces live under it (see Dirs) and
	// harnesses are installed into it.
	Root   string
	Handle Handler
}

func (w Worker) Run(ctx context.Context, request Request, emit func(Event)) (Result, error) {
	if request.Role != w.Role {
		return Result{}, fmt.Errorf("%s cannot run the %q unit", w.Name, request.Role)
	}
	dir, state, err := Dirs(w.Root, request.Town, request.Role)
	if err != nil {
		return Result{}, err
	}
	launch, err := ResolveAgent(ctx, w.Root, request.Agent)
	if err != nil {
		return Result{}, err
	}
	stream := &stream{emit: emit}
	defer stream.close()
	before := sessionFiles(state)
	result, err := w.Handle(ctx, Job{
		Request: request, Directory: dir, StateDirectory: state, Launch: launch,
		Log:      slog.New(&logHandler{stream: stream, attrs: []slog.Attr{slog.String("bot", w.Name)}}),
		Progress: stream.progress,
	})
	// A run that was stopped still reports what it wrote, so the digest gets
	// its own short budget rather than the run's context.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result.Transcripts = newTranscripts(recordCtx, filepath.Dir(dir), state, before)
	return result, err
}

// ResolveAgent turns an agent selection into a launch in this process's
// environment. An explicit command is used as given; a harness is installed
// under root and started from there.
func ResolveAgent(ctx context.Context, root string, a Agent) (runner.AgentConfig, error) {
	launch := runner.AgentConfig{Command: a.Command, Environment: a.Environment, AuthMethod: a.AuthMethod, Mode: a.Mode, Model: a.Model, Effort: a.Effort}
	if len(a.Command) > 0 || a.Harness == nil {
		return launch, nil
	}
	command, env, err := harness.Resolve(ctx, root, a.HarnessName, *a.Harness, a.Environment)
	if err != nil {
		return runner.AgentConfig{}, err
	}
	launch.Command, launch.Environment = command, env
	return launch, nil
}

// stream delivers events in order and drops any a straggling goroutine sends
// after the run has returned.
type stream struct {
	mu     sync.Mutex
	emit   func(Event)
	closed bool
}

func (s *stream) send(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed && s.emit != nil {
		s.emit(e)
	}
}

func (s *stream) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *stream) progress(phase, task string) {
	phase, task = strings.TrimSpace(phase), strings.TrimSpace(task)
	if phase == "" {
		phase = "running"
	}
	s.send(Event{Progress: &Progress{Phase: phase, Task: Truncate(task, 4096)}})
}

// Truncate bounds a text field at limit bytes without splitting a rune.
func Truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && cut < len(value) && value[cut]&0xC0 == 0x80 {
		cut--
	}
	return value[:cut] + "…[truncated]"
}

// logHandler turns a bot's slog records into Log events.
type logHandler struct {
	stream *stream
	attrs  []slog.Attr
	group  string
}

func (h *logHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *logHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	for _, a := range h.attrs {
		flatten(attrs, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		flatten(attrs, h.group, a)
		return true
	})
	h.stream.send(Event{Log: &Log{Time: r.Time, Level: r.Level.String(), Message: r.Message, Attrs: attrs}})
	return nil
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append([]slog.Attr{}, h.attrs...)
	for _, a := range attrs {
		if h.group != "" {
			a.Key = h.group + "." + a.Key
		}
		next.attrs = append(next.attrs, a)
	}
	return &next
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	next := *h
	if next.group != "" {
		name = next.group + "." + name
	}
	next.group = name
	return &next
}

func flatten(into map[string]string, prefix string, a slog.Attr) {
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	value := a.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		for _, child := range value.Group() {
			flatten(into, key, child)
		}
		return
	}
	into[key] = value.String()
}

// sessionFiles lists the transcripts already in a state directory.
func sessionFiles(state string) map[string]bool {
	files := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(state, "sessions"))
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
			files[entry.Name()] = true
		}
	}
	return files
}

// newTranscripts reports the transcripts a run added, relative to base.
func newTranscripts(ctx context.Context, base, state string, before map[string]bool) []Transcript {
	var out []Transcript
	for name := range sessionFiles(state) {
		if before[name] {
			continue
		}
		path := filepath.Join(state, "sessions", name)
		if !artifact.PathSafe(base, path) {
			continue
		}
		digest, size, err := artifact.Digest(ctx, path)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(base, path)
		out = append(out, Transcript{Path: filepath.ToSlash(rel), Digest: digest, Bytes: size})
	}
	return out
}

// AppendLabels adds the service's label filter to a bot's own default without
// duplicating an entry the configuration already carries.
func AppendLabels(existing, extra []string) []string {
	if len(extra) == 0 {
		return existing
	}
	seen := map[string]bool{}
	for _, l := range existing {
		seen[strings.ToLower(strings.TrimSpace(l))] = true
	}
	for _, l := range extra {
		key := strings.ToLower(strings.TrimSpace(l))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		existing = append(existing, strings.TrimSpace(l))
	}
	return existing
}

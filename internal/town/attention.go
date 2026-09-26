package town

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// AttentionHook is private service configuration. Only Public is sent to clients.
type AttentionHook struct {
	Enabled bool     `json:"enabled"`
	Command []string `json:"command,omitempty"`
}

func (h AttentionHook) Validate() error {
	if h.Enabled && len(h.Command) == 0 {
		return errors.New("an enabled attention hook requires a command")
	}
	size := 0
	for i, arg := range h.Command {
		size += len(arg)
		if len(arg) > 4096 || strings.ContainsFunc(arg, unicode.IsControl) || (i == 0 && strings.TrimSpace(arg) == "") {
			return errors.New("invalid attention hook command")
		}
	}
	if len(h.Command) > 64 || size > 16<<10 {
		return errors.New("attention hook command exceeds its argument limit")
	}
	return nil
}
func (h AttentionHook) Public() map[string]bool {
	return map[string]bool{"enabled": h.Enabled, "configured": len(h.Command) > 0}
}

func (c ServiceConfig) Public() map[string]any {
	return map[string]any{"max_workers": c.MaxWorkers, "quiet_hours": c.QuietHours, "attention_hook": c.AttentionHook.Public()}
}

type AttentionHookEdit struct {
	Enabled *bool     `json:"enabled,omitempty"`
	Command *[]string `json:"command,omitempty"`
}

func (s *Supervisor) SetAttentionHook(edit AttentionHookEdit) error {
	if edit.Enabled == nil && edit.Command == nil {
		return errors.New("supply enabled or command")
	}
	return s.Store.Update(func(st *State) error {
		h := &st.ServiceConfig.AttentionHook
		if edit.Enabled != nil {
			h.Enabled = *edit.Enabled
		}
		if edit.Command != nil {
			h.Command = slices.Clone(*edit.Command)
		}
		return h.Validate()
	})
}

// AttentionNotice contains public identities and a fixed reason code only.
// Error text, task titles, commands, credentials and repository content are excluded.
type AttentionNotice struct {
	Town   string `json:"town"`
	Task   string `json:"task"`
	Role   Role   `json:"role"`
	Reason string `json:"reason"`
	Seq    uint64 `json:"seq"`
}

func (n AttentionNotice) key() string { return n.Town + "\x00" + n.Task + "\x00" + n.Reason }
func (n AttentionNotice) valid() bool {
	if !ValidRepo(n.Town) || !ValidRole(n.Role) {
		return false
	}
	switch n.Reason {
	case "blocked", "failed", "inconclusive", "uncertain_write", "worker_failed", "worker_recovery":
	default:
		return false
	}
	if strings.HasPrefix(n.Task, "worker:") {
		return n.Task == "worker:"+string(n.Role)
	}
	kind, number, ok := strings.Cut(n.Task, ":")
	if kind == "commit" {
		return SHA(number)
	}
	if kind == "source" {
		return len(number) == 24 && strings.Trim(number, "0123456789abcdef") == ""
	}
	return ok && (kind == "pr" || kind == "issue") && validAttentionNumber(number)
}
func validAttentionNumber(number string) bool {
	if number == "" || number[0] == '0' {
		return false
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(number) <= 20
}

// attentionSet mirrors the attention states in the shared browser projection.
// It is pure: no process, network, or additional disk I/O occurs in Store.Update.
func attentionSet(st State, now time.Time) map[string]AttentionNotice {
	out := map[string]AttentionNotice{}
	for _, t := range st.Towns {
		if t.Deleted {
			continue
		}
		for _, task := range t.Tasks {
			if task.MayoralDecision == "pending" && !task.Blocked {
				continue
			}
			reason := ""
			switch task.Stage {
			case "complete", "merged", "closed", "implemented", "declined", "shipped":
				continue
			}
			intent := t.Intents[task.Number]
			running := false
			if w := t.Workers[task.House]; w != nil && w.Run != nil && (w.Status == "working" || w.Status == "pausing" || w.Agent != nil) {
				running = (task.Kind == "issue" && w.Run.Issue == task.Number) || (task.Kind == "pr" && w.Run.PR == task.Number)
			}
			switch {
			case task.Stage == "uncertain" || task.Stage == "uncertain_write" || (task.Kind == "pr" && intent != nil && intent.Status == "uncertain"):
				reason = "uncertain_write"
			case task.Stage == "inconclusive" || (task.Audit != nil && task.Audit.Verdict == "inconclusive"):
				reason = "inconclusive"
			case task.Deferred(now) && !running:
				continue
			case task.Blocked || task.Stage == "blocked":
				reason = "blocked"
			case task.Stage == "failed":
				reason = "failed"
			}
			if reason != "" {
				n := AttentionNotice{Town: t.ID, Task: task.ID, Role: task.House, Reason: reason, Seq: st.Seq}
				out[n.key()] = n
			}
		}
		for role, worker := range t.Workers {
			reason := ""
			if worker.Recovery != nil {
				reason = "worker_recovery"
			} else if worker.Status == "failed" {
				reason = "worker_failed"
			}
			if reason != "" {
				n := AttentionNotice{Town: t.ID, Task: "worker:" + string(role), Role: role, Reason: reason, Seq: st.Seq}
				out[n.key()] = n
			}
		}
	}
	return out
}

// Capture transitions in the committing transaction. A coalesced Watch signal
// cannot lose a short-lived blocked state while another hook is still running.
// Repeated transitions for an identity already queued coalesce into one notice.
func queueAttention(previous State, next *State, now time.Time) {
	h := next.ServiceConfig.AttentionHook
	if next.Demo || !h.Enabled {
		next.AttentionPending = nil
		next.AttentionSeen = nil
		return
	}
	next.AttentionSeen = map[string]bool{}
	for key, n := range attentionSet(*next, now) {
		next.AttentionSeen[key] = true
		if previous.AttentionSeen[key] {
			continue
		}
		if next.AttentionPending == nil {
			next.AttentionPending = map[string]AttentionNotice{}
		}
		if _, queued := next.AttentionPending[key]; !queued {
			next.AttentionPending[key] = n
		}
	}
}

const attentionHookTimeout = 10 * time.Second

// The idle watcher must not clone every town and its history on each commit.
func (s *Store) attentionReady() (active, pending bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.AttentionActive != nil, !s.state.Demo && s.state.ServiceConfig.AttentionHook.Enabled && len(s.state.AttentionPending) > 0
}

func (s *Supervisor) watchAttention(ctx context.Context) {
	// A saved claim may have run before the process stopped. Never replay it.
	if active, _ := s.Store.attentionReady(); active {
		slog.Warn("Attention hook interrupted; delivery is uncertain and will not be retried")
		if err := s.Store.Update(func(st *State) error { st.AttentionActive = nil; return nil }); err != nil {
			slog.Warn("Could not reconcile attention hook claim")
			return
		}
	}
	for ctx.Err() == nil {
		changed := s.Store.Watch()
		if _, pending := s.Store.attentionReady(); pending {
			var notice *AttentionNotice
			var command []string
			err := s.Store.Update(func(st *State) error {
				if st.Demo || !st.ServiceConfig.AttentionHook.Enabled {
					return nil
				}
				keys := make([]string, 0, len(st.AttentionPending))
				for key := range st.AttentionPending {
					keys = append(keys, key)
				}
				sort.Slice(keys, func(i, j int) bool {
					a, b := st.AttentionPending[keys[i]], st.AttentionPending[keys[j]]
					return a.Seq < b.Seq || (a.Seq == b.Seq && keys[i] < keys[j])
				})
				if len(keys) == 0 {
					return nil
				}
				n := st.AttentionPending[keys[0]]
				delete(st.AttentionPending, keys[0])
				st.AttentionActive = &n
				notice = &n
				command = slices.Clone(st.ServiceConfig.AttentionHook.Command)
				return nil
			})
			if err != nil {
				slog.Warn("Could not save attention hook claim; hook was not started")
				return
			}
			if notice != nil {
				outcome := runAttentionHook(ctx, filepath.Dir(s.Store.path), command, *notice, attentionHookTimeout)
				if outcome != "delivered" {
					slog.Warn("Attention hook did not complete", "town", notice.Town, "task", notice.Task, "outcome", outcome)
				}
				if err := s.Store.Update(func(st *State) error { st.AttentionActive = nil; return nil }); err != nil {
					slog.Warn("Could not finish attention hook claim; delivery will not be retried")
					return
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}
func runAttentionHook(ctx context.Context, dir string, command []string, notice AttentionNotice, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	data, _ := json.Marshal(notice)
	cmd := osrun.StartCommand(ctx, dir, command, nil)
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	// No command output enters logs or snapshots. Operators can redirect it in
	// their hook if needed; draining it consumes no growing output buffer.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	defer func() { _ = osrun.Kill(cmd) }()
	err := cmd.Run()
	if ctx.Err() != nil {
		return "interrupted_or_timed_out"
	}
	if err != nil {
		return "failed"
	}
	return "delivered"
}

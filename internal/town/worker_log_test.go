package town

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func drainLogs(ch chan Log) []Log {
	var out []Log
	for {
		select {
		case l := <-ch:
			out = append(out, l)
		default:
			return out
		}
	}
}

type credentials struct{ token string }

func (c credentials) LogValue() slog.Value {
	return slog.GroupValue(slog.String("user", "bot"), slog.String("token", c.token))
}

// Worker log lines are persisted and pushed to every client, so attributes
// that can carry credentials or a full command line never reach them, however
// the caller nests them.
func TestWorkerLogRedactsSensitiveAttributes(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	out := make(chan Log, 16)
	log := slog.New(&workerLog{role: Issue, out: out, now: func() time.Time { return at }})
	log.With("GH_TOKEN", "ghp_with", "repo", "o/r").Warn("started",
		"access_token", "ghp_attr",
		"Client_Secret", "shh-attr",
		"environment", "HOME=/x SECRET=env",
		"command", "gh auth --with-token ghp_cmd",
		"pr", 7,
		slog.Group("auth", slog.String("token", "ghp_group"), slog.String("kind", "app")),
		slog.Any("credentials", credentials{"ghp_valuer"}),
	)
	log.WithGroup("request").Info("grouped", "token", "ghp_withgroup", "id", 3)
	logs := drainLogs(out)
	if len(logs) != 2 {
		t.Fatalf("got %d log lines", len(logs))
	}
	first := logs[0]
	if first.Level != "WARN" || !first.At.Equal(at) {
		t.Fatalf("line metadata = %+v", first)
	}
	for _, leaked := range []string{"ghp_", "shh-attr", "SECRET=env", "gh auth"} {
		for _, l := range logs {
			if strings.Contains(l.Text, leaked) {
				t.Fatalf("log line leaked %q: %q", leaked, l.Text)
			}
		}
	}
	for _, kept := range []string{"started", "repo=o/r", "pr=7", "kind=app", "user=bot"} {
		if !strings.Contains(first.Text, kept) {
			t.Fatalf("log line lost %q: %q", kept, first.Text)
		}
	}
	if !strings.Contains(logs[1].Text, "id=3") {
		t.Fatalf("grouped line lost its attribute: %q", logs[1].Text)
	}
}

type agentSettings struct {
	Model  string
	Remote string
}

// Values passed with slog.Any print whole, so their field names are never
// inspected; credential-shaped text inside them is scrubbed instead.
func TestWorkerLogScrubsCredentialsInsideAnyValues(t *testing.T) {
	out := make(chan Log, 8)
	log := slog.New(&workerLog{role: Issue, out: out, now: time.Now})
	pat := "github_pat_" + strings.Repeat("A1", 20)
	log.Info("config", "agent", agentSettings{Model: "opus", Remote: "https://x-access-token:s3cr3tvalue@github.com/o/r.git"})
	log.Info("env", "vars", map[string]string{"GH": "ghp_" + strings.Repeat("a", 36), "MODE": "fast"})
	log.Info("failed", "error", errors.New("push refused for "+pat))
	log.Info("argv", "args", []string{"curl", "-H", "Authorization: Bearer abcdefghijklmnopqrstuvwxyz", "sk-ant-api03-" + strings.Repeat("z", 20)})
	log.Info("leaked in message ghu_" + strings.Repeat("b", 36))
	logs := drainLogs(out)
	if len(logs) != 5 {
		t.Fatalf("got %d log lines", len(logs))
	}
	for _, l := range logs {
		for _, leaked := range []string{"s3cr3tvalue", "ghp_", "github_pat_", "abcdefghijklmnop", "sk-ant-", "ghu_"} {
			if strings.Contains(l.Text, leaked) {
				t.Fatalf("log line leaked %q: %q", leaked, l.Text)
			}
		}
		if !strings.Contains(l.Text, "[redacted]") {
			t.Fatalf("nothing redacted in %q", l.Text)
		}
	}
	for i, kept := range []string{"{opus ", "MODE:fast", "push refused for", "curl"} {
		if !strings.Contains(logs[i].Text, kept) {
			t.Fatalf("scrubbing removed %q: %q", kept, logs[i].Text)
		}
	}
}

// Log lines are cut on a character boundary, so the persisted state and the
// JSON pushed to clients never carry a split character.
func TestWorkerLogBoundsLinesOnRuneBoundaries(t *testing.T) {
	out := make(chan Log, 1)
	log := slog.New(&workerLog{role: Issue, out: out, now: time.Now})
	log.Info(strings.Repeat("€", 3000))
	l := <-out
	if !utf8.ValidString(l.Text) {
		t.Fatal("bounded log line split a character")
	}
	if !strings.HasSuffix(l.Text, "…") || len(l.Text) > 4000+len("…") {
		t.Fatalf("log line not bounded: %d bytes", len(l.Text))
	}
}

// A full buffer drops the oldest pending line, never the newest, and never
// blocks the agent callback that logged it.
func TestWorkerLogKeepsTheNewestLineWhenFull(t *testing.T) {
	out := make(chan Log, 2)
	log := slog.New(&workerLog{role: Issue, out: out, now: time.Now})
	done := make(chan struct{})
	go func() {
		for _, m := range []string{"one", "two", "three"} {
			log.Info(m)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("logging blocked on a full buffer")
	}
	logs := drainLogs(out)
	if len(logs) != 2 || logs[0].Text != "two" || logs[1].Text != "three" {
		t.Fatalf("kept %+v", logs)
	}
}

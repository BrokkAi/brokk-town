package botserver

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

func worker(root string) botapi.Worker {
	return botapi.Worker{Name: "review-bot", Role: "review", Root: root}
}

// A bot refuses to serve without the settings that keep it safe.
func TestABotNeedsATokenAndARoot(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name, token string
		args        []string
		want        string
	}{
		{"no token", "", []string{"--root", root, "--listen", "127.0.0.1:0"}, "token is required"},
		{"short token", "short", []string{"--root", root, "--listen", "127.0.0.1:0"}, "at least 16"},
		{"no root", "0123456789abcdef", []string{"--listen", "127.0.0.1:0"}, "data root"},
		{"half TLS", "0123456789abcdef", []string{"--root", root, "--listen", "127.0.0.1:0", "--tls-cert", "x"}, "both"},
		{"stray", "0123456789abcdef", []string{"--root", root, "extra"}, "unexpected"},
		{"demo mode is gone", "0123456789abcdef", []string{"--root", root, "--listen", "127.0.0.1:0", "--demo"}, "not defined"},
		{"no parent to exit with", "0123456789abcdef", []string{"--root", root, "--listen", "127.0.0.1:0", "--exit-with-parent"}, "not defined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvToken, tc.token)
			t.Setenv(EnvRoot, "")
			err := Run(context.Background(), tc.args, "v1.0.0", worker, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A bot prints where it listens, then stops when its context ends, as it does
// on SIGTERM when Kubernetes stops its pod.
func TestABotStopsWhenItsContextEnds(t *testing.T) {
	t.Setenv(EnvToken, "0123456789abcdef")
	var out strings.Builder
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, []string{"--root", t.TempDir(), "--listen", "127.0.0.1:0"}, "v1.0.0", worker, &out, io.Discard)
	if err != nil || !strings.Contains(out.String(), `"address":"127.0.0.1:`) || !strings.Contains(out.String(), `"version":"v1.0.0"`) {
		t.Fatalf("err %v, out %q", err, out.String())
	}
}

// A log record whose text grows when encoded still fits in one frame.
func TestAnOversizedLogRecordFitsItsFrame(t *testing.T) {
	for _, text := range []string{strings.Repeat("\x1b", 2*botapi.MaxEventBytes), strings.Repeat("\xff", 2*botapi.MaxEventBytes), strings.Repeat("界", botapi.MaxEventBytes)} {
		e := boundEvent(botapi.Event{Log: &botapi.Log{Message: text, Attrs: map[string]string{"k": text}}})
		data, err := marshalLine(botapi.Frame{Seq: 99999, Event: e})
		if err != nil || len(data) > botapi.MaxEventBytes {
			t.Fatalf("frame is %d bytes (%v)", len(data), err)
		}
	}
}

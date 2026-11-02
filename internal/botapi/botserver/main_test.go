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
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvToken, tc.token)
			t.Setenv(EnvRoot, "")
			err := Run(context.Background(), tc.args, "v1.0.0", worker, strings.NewReader(""), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A bot started by a parent exits when the parent's end of standard input
// closes, after printing where it listens.
func TestABotExitsWithItsParent(t *testing.T) {
	t.Setenv(EnvToken, "0123456789abcdef")
	var out strings.Builder
	err := Run(context.Background(), []string{"--root", t.TempDir(), "--listen", "127.0.0.1:0", "--exit-with-parent"}, "v1.0.0", worker, strings.NewReader(""), &out, io.Discard)
	if err != nil || !strings.Contains(out.String(), `"address":"127.0.0.1:`) || !strings.Contains(out.String(), `"version":"v1.0.0"`) {
		t.Fatalf("err %v, out %q", err, out.String())
	}
}

// Every role's demo result is the kind its service accepts.
func TestDemoResultsMatchTheirRole(t *testing.T) {
	r := demoResult(botapi.Request{Role: "review", HeadSHA: "h"})
	if r.Review == nil || !r.Review.Complete || r.Review.ExactHead != "h" {
		t.Fatalf("review %+v", r)
	}
	if demoResult(botapi.Request{Role: "issue"}).Issue == nil || demoResult(botapi.Request{Role: "hall", Mode: "judge"}).Judgment == nil {
		t.Fatal("issue or hall result missing")
	}
	if inv := demoResult(botapi.Request{Role: "repo", Repo: "o/r"}).Inventory; inv == nil || len(inv.Head) != 40 {
		t.Fatal("repo inventory missing")
	}
}

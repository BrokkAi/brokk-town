package botlaunch

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

func reviewBot(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a bot binary")
	}
	exe := filepath.Join(t.TempDir(), "review-bot")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, "../../cmd/review-bot").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return exe
}

// A bot that dies is started again on a new port with a new token, and a
// call made meanwhile waits for it. Stop leaves no bot running.
func TestALaunchedBotIsRestartedAndStopped(t *testing.T) {
	p := Start(Spec{Name: "review-bot", Role: "review", Executable: reviewBot(t), Root: t.TempDir(), Demo: true}, nil)
	stopped := false
	defer func() {
		if !stopped {
			p.Stop()
		}
	}()
	ctx := context.Background()
	identity, err := p.Identity(ctx)
	if err != nil || identity.Role != "review" || !identity.Demo {
		t.Fatalf("identity %+v, %v", identity, err)
	}
	p.mu.Lock()
	first, firstClient := p.pid, p.client
	p.mu.Unlock()
	process, _ := os.FindProcess(first)
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		p.mu.Lock()
		pid, client := p.pid, p.client
		p.mu.Unlock()
		if pid != first && client != nil {
			if client.URL == firstClient.URL && client.Token == firstClient.Token {
				t.Fatal("the restarted bot reused the old address and token")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bot was not restarted")
		}
		time.Sleep(50 * time.Millisecond)
	}
	result, err := p.Run(ctx, botapi.Request{Town: "0123abcd", Role: "review", PR: 1, HeadSHA: "h"}, nil)
	if err != nil || result.Review == nil || result.Review.ExactHead != "h" {
		t.Fatalf("run after restart: %+v, %v", result, err)
	}
	p.Stop()
	stopped = true
	if _, err := p.Identity(ctx); !errors.Is(err, botapi.ErrUnavailable) {
		t.Fatalf("stopped bot answered: %v", err)
	}
	if _, err := firstClient.Identity(ctx); err == nil {
		t.Fatal("the first bot still answers")
	}
}

// A missing binary is reported, not retried silently forever.
func TestAMissingBinaryIsUnavailable(t *testing.T) {
	p := Start(Spec{Name: "review-bot", Role: "review", Executable: filepath.Join(t.TempDir(), "absent"), Root: t.TempDir()}, nil)
	defer p.Stop()
	p.ReadyTimeout = 2 * time.Second
	_, err := p.Run(context.Background(), botapi.Request{Role: "review"}, nil)
	if !errors.Is(err, botapi.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// A launched bot never inherits the tokens the service holds for other bots
// or for its callback.
func TestALaunchedBotGetsNoOtherBotsCredentials(t *testing.T) {
	t.Setenv("BROKK_TOWN_BOT_REVIEW_TOKEN", "review-secret")
	t.Setenv("BROKK_TOWN_BOT_REVIEW_URL", "http://review")
	t.Setenv("BROKK_TOWN_CALLBACK_TLS_KEY", "/key")
	t.Setenv("BROKK_BOT_TOKEN", "stale")
	t.Setenv("BROKK_TOWN_MUSE_PERMISSIONS", "keep")
	t.Setenv("GH_TOKEN", "gh")
	env := strings.Join(childEnvironment(), "\n")
	for _, leaked := range []string{"review-secret", "http://review", "/key", "stale"} {
		if strings.Contains(env, leaked) {
			t.Fatalf("environment carries %q", leaked)
		}
	}
	if !strings.Contains(env, "GH_TOKEN=gh") || !strings.Contains(env, "BROKK_TOWN_MUSE_PERMISSIONS=keep") {
		t.Fatal("the bot lost settings it needs")
	}
}

// Every line a bot writes reaches the log, including the last ones a failing
// bot writes as it exits, and the first line is the readiness report.
func TestLineLogKeepsEveryLine(t *testing.T) {
	var buf bytes.Buffer
	var first string
	l := &lineLog{log: slog.New(slog.NewTextHandler(&buf, nil)), source: "review-bot", first: func(s string) { first = s }}
	_, _ = l.Write([]byte("{\"address\":\"x\"}\npanic: boo"))
	_, _ = l.Write([]byte("m\r\ngoroutine 1\n"))
	if first != `{"address":"x"}` || !strings.Contains(buf.String(), "panic: boom") || !strings.Contains(buf.String(), "goroutine 1") {
		t.Fatalf("first %q, log %q", first, buf.String())
	}
}

// After a launch fails, a call is told why at once instead of waiting out the
// ready timeout.
func TestAFailedLaunchFailsCallsAtOnce(t *testing.T) {
	p := Start(Spec{Name: "review-bot", Role: "review", Executable: filepath.Join(t.TempDir(), "absent"), Root: t.TempDir()}, nil)
	defer p.Stop()
	begun := time.Now()
	_, err := p.Identity(context.Background())
	if !errors.Is(err, botapi.ErrUnavailable) || !strings.Contains(err.Error(), "cannot find") || time.Since(begun) > 5*time.Second {
		t.Fatalf("err %v after %s", err, time.Since(begun))
	}
}

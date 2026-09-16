package osrun

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFailedCommandNeverReturnsTruncatedResponse(t *testing.T) {
	text, err := Run(context.Background(), "", nil, "python3", "-c", "import sys; sys.stdout.write('x' * ((8 << 20) + 1)); sys.exit(1)")
	var exit *exec.ExitError
	if text != "" || err == nil || !strings.Contains(err.Error(), "exceeded 8 MiB") || !errors.As(err, &exit) {
		t.Fatalf("truncated failure exposed as a complete response: bytes=%d, err=%v", len(text), err)
	}
}

func TestTailIsBoundedAndUTF8(t *testing.T) {
	tail := &Tail{Capacity: 4}
	_, _ = tail.Write([]byte("prefix€€"))
	text, truncated := tail.Text()
	if !truncated || !utf8.ValidString(text) || text != "€" {
		t.Fatalf("bad tail: %q %v", text, truncated)
	}
	empty := &Tail{Capacity: 0}
	_, _ = empty.Write([]byte("drop"))
	text, truncated = empty.Text()
	if text != "" || !truncated {
		t.Fatal("zero output limit ignored")
	}
}
func TestProcessTreeCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Run(ctx, "", nil, "sh", "-c", "sleep 30 & wait"); err == nil {
		t.Fatal("cancelled shell succeeded")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("descendant held output pipes open")
	}
}

// A failing command's message is persisted and pushed to every connected
// client, so it must not carry the whole captured output.
func TestFailureMessageCarriesBoundedOutput(t *testing.T) {
	script := "import sys; sys.stdout.write('o' * 200000); sys.stderr.write('e' * 200000); sys.exit(3)"
	text, err := Run(context.Background(), "", nil, "python3", "-c", script)
	if err == nil {
		t.Fatal("failing command reported success")
	}
	if len(text) != 200000 {
		t.Fatalf("caller lost the complete output: %d bytes", len(text))
	}
	message := err.Error()
	if len(message) > 4*ErrorTextLimit {
		t.Fatalf("error message was not bounded: %d bytes", len(message))
	}
	if !strings.Contains(message, "earlier output omitted") || !strings.Contains(message, "ooo") || !strings.Contains(message, "eee") {
		t.Fatalf("bounded message lost its explanation: %q", message[:min(len(message), 200)])
	}
}

func TestClipKeepsTheTailOnRuneBoundaries(t *testing.T) {
	if got := Clip("short", ErrorTextLimit); got != "short" {
		t.Fatalf("clipped a value within the limit: %q", got)
	}
	got := Clip(strings.Repeat("€", 10), 5)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "€") || strings.Count(got, "€") != 1 {
		t.Fatalf("clip broke a rune or kept the wrong tail: %q", got)
	}
}

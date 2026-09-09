package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
	"github.com/rivo/uniseg"
)

func TestTerminalFramesFitAndNeutralizeControls(t *testing.T) {
	s := town.NewState(true)
	x, _ := s.Add(town.DefaultConfig("acme/orchard"))
	_, _ = s.Add(town.DefaultConfig("acme/paper-trail"))
	x.Workers[town.Bug].Task = "危険\x1b[2J 👩‍💻 é"
	x.Report("New arrivals", "10 issues arrived", time.Now())
	for _, size := range [][2]int{{120, 35}, {80, 24}, {25, 10}, {10, 3}, {1, 1}} {
		for _, role := range []int{-1, 0, 3} {
			frame := renderTUI(s, 0, role, size[0], size[1], "status")
			lines := strings.Split(frame, "\n")
			if len(lines) > size[1] {
				t.Fatalf("height exceeded %v", size)
			}
			for _, line := range lines {
				if uniseg.StringWidth(line) > size[0] {
					t.Fatalf("width exceeded %v: %q", size, line)
				}
				if strings.ContainsRune(line, '\x1b') {
					t.Fatal("untrusted terminal escape")
				}
			}
		}
	}
	overview := renderTUI(s, 0, -1, 100, 30, "")
	if !strings.Contains(overview, "acme/orchard") || !strings.Contains(overview, "acme/paper-trail") {
		t.Fatal("overview omitted a town")
	}
}
func TestPastedCommandsNeverOperateTown(t *testing.T) {
	var d keyDecoder
	out := d.feed("\x1b[200~aspq\x1b[201~")
	if len(out) != 0 {
		t.Fatal("paste became controls", out)
	}
	out = d.feed("q")
	if len(out) != 1 || out[0] != "q" {
		t.Fatal(out)
	}
}
func TestRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer h.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var result any
		done <- request(ctx, connection{URL: h.URL, Token: "key"}, "GET", "/api/state", nil, &result)
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("request blocked cancellation")
	}
}
func TestRejectPublicBindAndUnexpectedArguments(t *testing.T) {
	if e := serve(context.Background(), t.TempDir(), "0.0.0.0:8099", false, "", ""); e == nil {
		t.Fatal("public bind accepted")
	}
	if e := run(context.Background(), []string{"status", "unexpected"}); e == nil {
		t.Fatal("extra argument ignored")
	}
}

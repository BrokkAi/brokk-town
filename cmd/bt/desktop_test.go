package main

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
)

// buildBots compiles every bot binary into one directory, the way a release
// archive installs them next to bt.
func buildBots(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the bot binaries")
	}
	dir := t.TempDir()
	for _, role := range town.AgentRoles {
		name := town.BotName(role)
		out, err := exec.Command("go", "build", "-o", filepath.Join(dir, name+exeSuffix()), "../"+name).CombinedOutput()
		if err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, out)
		}
	}
	return dir
}

// The desktop app's deployment: bt starts each bot binary on a loopback port
// with a token of its own and reaches it over the network contract. In demo
// mode every bot answers a dispatch, which Activity records; when bt stops,
// so do the bots.
func TestDesktopLaunchesTheBotBinariesAndDispatchesOverLoopback(t *testing.T) {
	t.Setenv("BROKK_TOWN_BOT_DIR", buildBots(t))
	for _, role := range town.AgentRoles {
		t.Setenv(botEnv(role, "URL"), "")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, dir, "127.0.0.1:0", true, "", "") }()
	defer func() {
		cancel()
		select {
		case err := <-served:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(90 * time.Second):
			t.Error("bt did not stop its bots")
		}
	}()
	answered := map[string]bool{}
	deadline := time.Now().Add(90 * time.Second)
	for len(answered) < len(town.AgentRoles) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d bots answered over loopback: %v", len(answered), len(town.AgentRoles), answered)
		}
		time.Sleep(500 * time.Millisecond)
		conn, err := readConnection(dir)
		if err != nil {
			continue
		}
		var state struct {
			Events []struct {
				Kind  string `json:"kind"`
				From  string `json:"from"`
				Title string `json:"title"`
			} `json:"events"`
		}
		if err := request(ctx, conn, "GET", "/api/state", nil, &state); err != nil {
			continue
		}
		for _, e := range state.Events {
			if strings.Contains(e.Title, "could not be reached") || strings.Contains(e.Title, "answered with an error") {
				t.Fatalf("a bot failed: %s", e.Title)
			}
			if e.Kind == "activity" && strings.Contains(e.Title, "answered a demo dispatch over the network") {
				answered[e.From] = true
			}
		}
	}
}

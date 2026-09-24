package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bot "github.com/BrokkAi/simplifier-bot"
)

func cliFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"remote":"https://github.com/o/r.git","agent":{"command":["sh"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return path
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestCLIOverridesAndInterspersedFlags(t *testing.T) {
	path := cliFixture(t)
	called := false
	run := func(_ context.Context, c bot.Config, _ *slog.Logger, once bool) error {
		called = true
		if c.Poll != bot.Duration(2*time.Minute) || c.Timeout != bot.Duration(time.Hour) || !once || c.MaxProposals != 7 || !c.DryRun || c.Agent.Model != "fixture" || c.Agent.Effort != "low" || len(c.Labels) != 2 || strings.Join(c.Agent.Command, " ") != "sh -x" {
			t.Fatalf("wrong settings %+v", c)
		}
		return nil
	}
	err := executeWith(context.Background(), []string{"once", "--config", path, "--max-proposals", "7", "--poll", "2m", "--timeout", "1h", "--dry-run", "--model", "fixture", "--effort", "low", "--label", "simplify", "--label", "ready", "--agent-arg", "-x"}, quiet(), run, nil, io.Discard)
	if err != nil || !called {
		t.Fatalf("CLI %v %v", called, err)
	}
}

func TestAssessCLI(t *testing.T) {
	path := cliFixture(t)
	var got []any
	assess := func(_ context.Context, _ bot.Config, mode string, issue, pr int, _ *slog.Logger) (bot.Assessment, error) {
		got = []any{mode, issue, pr}
		return bot.Assessment{Decision: "admit", Summary: "fine"}, nil
	}
	var out strings.Builder
	if err := executeWith(context.Background(), []string{"assess", "--config", path, "--pr", "12", "--mode", "auto"}, quiet(), nil, assess, &out); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "auto" || got[1] != 0 || got[2] != 12 || !strings.Contains(out.String(), `"decision": "admit"`) {
		t.Fatalf("assess %v %q", got, out.String())
	}
	for _, args := range [][]string{{"assess", "--config", path}, {"assess", "--config", path, "--issue", "1", "--pr", "2"}} {
		if err := executeWith(context.Background(), args, quiet(), nil, assess, io.Discard); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestPlainAndJSONConflict(t *testing.T) {
	path := cliFixture(t)
	if err := executeWith(context.Background(), []string{"--config", path, "--plain", "--json"}, quiet(), nil, nil, io.Discard); err == nil {
		t.Fatal("--plain with --json accepted")
	}
}

func TestVersionCommand(t *testing.T) {
	original := version
	version = "v1.2.3"
	t.Cleanup(func() { version = original })
	var output strings.Builder
	if err := versionCommand(nil, &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "v1.2.3\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
	if err := versionCommand([]string{"extra"}, &output); err == nil {
		t.Fatal("version accepted an argument")
	}
}

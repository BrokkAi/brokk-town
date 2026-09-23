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

	bot "github.com/BrokkAi/repo-bot"
)

func cliFixture(t *testing.T, agent string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"remote":"https://github.com/o/r.git","agent":{"command":[`+agent+`]}}`), 0600); err != nil {
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
	path := cliFixture(t, `"sh"`)
	called := false
	run := func(_ context.Context, c bot.Config, _ *slog.Logger, once bool) error {
		called = true
		if c.Poll != bot.Duration(2*time.Minute) || c.Timeout != bot.Duration(time.Hour) || !once || c.MaxRepairs != 5 || !c.DryRun || c.Agent.Model != "fixture" || c.Agent.Effort != "low" || strings.Join(c.Agent.Command, " ") != "sh -x" {
			t.Fatalf("wrong settings %+v", c)
		}
		return nil
	}
	err := executeWith(context.Background(), []string{"once", "--config", path, "--max-repairs", "5", "--poll", "2m", "--timeout", "1h", "--dry-run", "--model", "fixture", "--effort", "low", "--agent-arg", "-x"}, quiet(), run, io.Discard)
	if err != nil || !called {
		t.Fatalf("CLI %v %v", called, err)
	}
}

func TestObservationWithoutAnAgent(t *testing.T) {
	path := cliFixture(t, `"missing-agent"`)
	called := false
	run := func(_ context.Context, c bot.Config, _ *slog.Logger, _ bool) error {
		called = true
		if !c.InventoryOnly() {
			t.Fatalf("agent kept %v", c.Agent.Command)
		}
		return nil
	}
	if err := executeWith(context.Background(), []string{"--config", path, "--agent", ""}, quiet(), run, io.Discard); err != nil || !called {
		t.Fatalf("inventory-only run %v %v", called, err)
	}
	if err := executeWith(context.Background(), []string{"--config", path, "--agent", "", "--agent-arg", "-x"}, quiet(), run, io.Discard); err == nil {
		t.Fatal("agent argument without an agent accepted")
	}
	if err := executeWith(context.Background(), []string{"--config", path}, quiet(), run, io.Discard); err == nil {
		t.Fatal("missing agent accepted")
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

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

	bot "github.com/BrokkAi/mayor-bot"
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
	watch := func(_ context.Context, c bot.Config, _ *slog.Logger, once bool) error {
		called = true
		if c.Poll != bot.Duration(2*time.Hour) || c.Timeout != bot.Duration(time.Hour) || !once || c.MaxItems != 9 || c.Agent.Model != "fixture" || c.Agent.Effort != "low" || strings.Join(c.Agent.Command, " ") != "sh -x" {
			t.Fatalf("wrong settings %+v", c)
		}
		return nil
	}
	err := executeWith(context.Background(), []string{"once", "--config", path, "--max-items", "9", "--poll", "2h", "--timeout", "1h", "--model", "fixture", "--effort", "low", "--agent-arg", "-x"}, quiet(), commands{watch: watch}, io.Discard)
	if err != nil || !called {
		t.Fatalf("CLI %v %v", called, err)
	}
}

func TestJudgeCLI(t *testing.T) {
	path := cliFixture(t)
	var got bot.JudgeRequest
	judge := func(_ context.Context, _ bot.Config, r bot.JudgeRequest, _ *slog.Logger) (bot.Judgment, error) {
		got = r
		return bot.Judgment{Decision: "admit", Reason: "useful"}, nil
	}
	var out strings.Builder
	if err := executeWith(context.Background(), []string{"judge", "--config", path, "--pr", "4"}, quiet(), commands{judge: judge}, &out); err != nil {
		t.Fatal(err)
	}
	if got.PR != 4 || got.Issue != 0 || string(got.Arrival) != `{"kind":"pr"}` || !strings.Contains(out.String(), `"decision": "admit"`) {
		t.Fatalf("judge %+v %q", got, out.String())
	}
	for _, args := range [][]string{{"judge", "--config", path}, {"judge", "--config", path, "--issue", "1", "--pr", "2"}} {
		if err := executeWith(context.Background(), args, quiet(), commands{judge: judge}, io.Discard); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestBulletinCLI(t *testing.T) {
	path := cliFixture(t)
	var got bot.Window
	bulletin := func(_ context.Context, _ bot.Config, w bot.Window, _ *slog.Logger) (bot.Report, error) {
		got = w
		return bot.Report{Window: w, Bulletin: bot.Bulletin{Title: "Nothing new this time"}, Pulls: []int{}}, nil
	}
	var out strings.Builder
	if err := executeWith(context.Background(), []string{"bulletin", "--config", path, "--since", "72h"}, quiet(), commands{bulletin: bulletin}, &out); err != nil {
		t.Fatal(err)
	}
	if span := got.Until.Sub(got.Since); span != 72*time.Hour || !strings.Contains(out.String(), "Nothing new this time") {
		t.Fatalf("bulletin %v %q", span, out.String())
	}
	if err := executeWith(context.Background(), []string{"bulletin", "--config", path}, quiet(), commands{bulletin: bulletin}, io.Discard); err != nil || got.Until.Sub(got.Since) != 24*time.Hour {
		t.Fatalf("default window %v %v", got.Until.Sub(got.Since), err)
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

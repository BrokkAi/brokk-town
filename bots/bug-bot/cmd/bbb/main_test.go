package main

import (
	"context"
	bot "github.com/BrokkAi/bug-bot"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIOverridesAndInterspersedFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"remote":"https://github.com/o/r.git","agent":{"command":["sh"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	called := false
	run := func(_ context.Context, c bot.Config, _ *slog.Logger, once bool) error {
		called = true
		if c.Poll != bot.Duration(2*time.Minute) || !once || c.MaxIssues != 7 || !c.DryRun || c.Focus != "parser" || c.Agent.Model != "fixture" || c.Agent.Effort != "low" || len(c.Labels) != 2 {
			t.Fatalf("wrong settings %+v", c)
		}
		return nil
	}
	err := executeWithRun(context.Background(), []string{"once", "--config", path, "--max-issues", "7", "--poll", "2m", "--dry-run", "--focus", "parser", "--model", "fixture", "--effort", "low", "--label", "bug", "--label", "ready"}, slog.New(slog.NewTextHandler(io.Discard, nil)), run)
	if err != nil || !called {
		t.Fatalf("CLI %v %v", called, err)
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

func TestReportCLIWithoutTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"remote":"https://github.com/o/r.git","agent":{"command":["nonexistent-agent"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	output, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	original := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = original }()
	run := func(context.Context, bot.Config, *slog.Logger, bool) error {
		t.Fatal("report started scan engine")
		return nil
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := executeWithRun(context.Background(), []string{"report", "--config", path, "--status", "dry_run"}, log, run); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil || !strings.Contains(string(data), "# Saved findings") || !strings.Contains(string(data), "No saved findings") {
		t.Fatalf("report output: %s, %v", data, err)
	}
	if err := executeWithRun(context.Background(), []string{"report", "--config", path, "--status", "bogus"}, log, run); err == nil || !strings.Contains(err.Error(), "unsupported report status") {
		t.Fatalf("invalid filter: %v", err)
	}
}

func TestCLIReviewSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"remote":"https://github.com/o/r.git","agent":{"command":["sh"],"model":"scout","effort":"high"},"review_model":"json-judge","review_effort":"medium"}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		args            []string
		discovery, want string
	}{
		{nil, "scout/high", "json-judge/medium"},
		{[]string{"--review-model", "cli-judge"}, "scout/high", "cli-judge/medium"},
		{[]string{"--review-effort", "low"}, "scout/high", "json-judge/low"},
		{[]string{"--model", "m", "--review-model", "cli-judge", "--review-effort", "low"}, "m/high", "cli-judge/low"},
	} {
		var got bot.Config
		run := func(_ context.Context, c bot.Config, _ *slog.Logger, _ bool) error { got = c; return nil }
		if err := executeWithRun(context.Background(), append([]string{"once", "--config", path}, tc.args...), log, run); err != nil {
			t.Fatal(err)
		}
		r := got.ReviewAgent()
		if got.Agent.Model+"/"+got.Agent.Effort != tc.discovery || r.Model+"/"+r.Effort != tc.want {
			t.Fatalf("%v: discovery %+v, review %+v", tc.args, got.Agent, r)
		}
	}
	for _, args := range [][]string{{"--review-model", ""}, {"--review-model", "  "}, {"--review-effort", ""}, {"--review-effort=\t"}} {
		run := func(context.Context, bot.Config, *slog.Logger, bool) error {
			t.Fatal("blank review selection started a scan")
			return nil
		}
		if err := executeWithRun(context.Background(), append([]string{"once", "--config", path}, args...), log, run); err == nil || !strings.Contains(err.Error(), "cannot be empty") {
			t.Fatalf("%q: %v", args, err)
		}
	}
}

func TestCLIOnlyOnChange(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		config string
		args   []string
		want   bool
	}{
		{"", nil, false},
		{"", []string{"--only-on-change"}, true},
		{`,"only_on_change":true`, nil, true},
		{`,"only_on_change":true`, []string{"--only-on-change=false"}, false},
		{`,"only_on_change":false`, []string{"--only-on-change"}, true},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"remote":"https://github.com/o/r.git","agent":{"command":["sh"]}`+tc.config+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		var got bot.Config
		run := func(_ context.Context, c bot.Config, _ *slog.Logger, _ bool) error { got = c; return nil }
		if err := executeWithRun(context.Background(), append([]string{"run", "--config", path}, tc.args...), log, run); err != nil {
			t.Fatal(err)
		}
		if got.OnlyOnChange != tc.want {
			t.Fatalf("%s %v: only_on_change = %t", tc.config, tc.args, got.OnlyOnChange)
		}
	}
}

package main

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"

	bot "github.com/BrokkAi/release-bot"
	"github.com/BrokkAi/release-bot/internal/worker"
)

func TestWorkerPolicyAppliesReleaseCadence(t *testing.T) {
	defaults := bot.DefaultConfig()

	unchanged := workerConfig(worker.Request{})
	if unchanged.Daily != defaults.Daily || unchanged.Quiet != defaults.Quiet || unchanged.Triage != defaults.Triage || unchanged.Attempts != defaults.Attempts {
		t.Fatalf("a request with no policy changed the defaults: %+v", unchanged)
	}

	empty := workerConfig(worker.Request{Policy: &worker.Policy{}})
	if empty.Daily != defaults.Daily || empty.Burst != defaults.Burst {
		t.Fatalf("an empty policy changed the defaults: %+v", empty)
	}

	off := false
	dir := t.TempDir()
	workspace := worker.Request{
		Remote: "https://github.com/o/r.git", Branch: "main",
		Directory: filepath.Join(dir, "checkout"), StateDirectory: filepath.Join(dir, "state"),
		Repo: "o/r", Host: "github.com", Agent: runner.AgentConfig{Command: []string{"simulated"}},
	}
	workspace.Policy = &worker.Policy{
		Attempts: 5,
		Verify:   []string{"house-verify"},
		Release: &worker.ReleasePolicy{
			DailySeconds: 7200, MinimumGapSeconds: 900, QuietSeconds: 300,
			Burst: 4, BurstWindowSeconds: 1800, Triage: &off,
			Preflight: []string{"make", "preflight"}, VerificationTimeoutSeconds: 120,
			Workflows: []string{"ci"}, Assets: []string{"dist/*.tar.gz"},
		},
	}
	cfg := workerConfig(workspace)
	for _, tc := range []struct {
		name string
		got  bot.Duration
		want time.Duration
	}{
		{"daily", cfg.Daily, 2 * time.Hour},
		{"minimum gap", cfg.MinimumGap, 15 * time.Minute},
		{"quiet", cfg.Quiet, 5 * time.Minute},
		{"burst window", cfg.BurstWindow, 30 * time.Minute},
		{"verification timeout", cfg.VerificationTimeout, 2 * time.Minute},
	} {
		if time.Duration(tc.got) != tc.want {
			t.Fatalf("%s = %v, want %v", tc.name, time.Duration(tc.got), tc.want)
		}
	}
	if cfg.Burst != 4 || cfg.Attempts != 5 || cfg.Triage {
		t.Fatalf("policy scalars not applied: burst %d attempts %d triage %v", cfg.Burst, cfg.Attempts, cfg.Triage)
	}
	if !reflect.DeepEqual(cfg.Preflight, []string{"make", "preflight"}) || !reflect.DeepEqual(cfg.Verify, []string{"house-verify"}) {
		t.Fatalf("commands not applied: preflight %v verify %v", cfg.Preflight, cfg.Verify)
	}
	if !reflect.DeepEqual(cfg.GitHub.Workflows, []string{"ci"}) || !reflect.DeepEqual(cfg.GitHub.Assets, []string{"dist/*.tar.gz"}) {
		t.Fatalf("release gating not applied: workflows %v assets %v", cfg.GitHub.Workflows, cfg.GitHub.Assets)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a policy produced a configuration the bot rejects: %v", err)
	}

	// Triage is a pointer precisely so that turning it on is expressible.
	on := true
	if !workerConfig(worker.Request{Policy: &worker.Policy{Release: &worker.ReleasePolicy{Triage: &on}}}).Triage {
		t.Fatal("triage could not be turned on")
	}
}

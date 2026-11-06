package town

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/BrokkAi/acp-go/runner"
)

// writeMuseConfig lays out a Muse config directory and returns the agent
// environment that points at it.
func writeMuseConfig(t *testing.T, settings string) (string, map[string]string) {
	t.Helper()
	xdg := t.TempDir()
	dir := filepath.Join(xdg, "muse")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"settings.json": settings,
		"auth.json":     `{"token":"live"}`,
		"trust.json":    `{"projects":{}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, map[string]string{"XDG_CONFIG_HOME": xdg}
}

// stubHarnessPATH puts no-op executables on PATH so the launch path resolves
// without those harnesses being installed.
func stubHarnessPATH(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Only the Muse adapter is redirected, and the redirect outranks an operator's
// own XDG_CONFIG_HOME because the derivation already read through it.
func TestAgentConfigRedirectsMuseOnly(t *testing.T) {
	stubHarnessPATH(t, "npx")
	_, env := writeMuseConfig(t, `{"permissions":{"default_profile":":auto-review"}}`)
	root := t.TempDir()
	base := Config{Agent: runner.AgentConfig{Environment: env}}

	muse := base
	muse.Harness = "muse-acp"
	a, err := agentConfig(context.Background(), muse, root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "muse-acp-config")
	if a.Environment["XDG_CONFIG_HOME"] != want {
		t.Fatalf("muse-acp XDG_CONFIG_HOME = %q, want %q", a.Environment["XDG_CONFIG_HOME"], want)
	}

	other := base
	other.Harness = "brokkai/anvil"
	if a, err = agentConfig(context.Background(), other, root); err != nil {
		t.Fatal(err)
	}
	if a.Environment["XDG_CONFIG_HOME"] != env["XDG_CONFIG_HOME"] {
		t.Fatalf("anvil was redirected to %q", a.Environment["XDG_CONFIG_HOME"])
	}
}

// The opt-out leaves the operator's configuration untouched.
func TestAgentConfigMusePermissionsOptOut(t *testing.T) {
	for _, name := range []string{"BROKK_TOWN_MUSE_PERMISSIONS"} {
		t.Run(name, func(t *testing.T) {
			stubHarnessPATH(t, "npx")
			t.Setenv(name, "keep")
			_, env := writeMuseConfig(t, `{"permissions":{"default_profile":":auto-review"}}`)
			cfg := Config{Harness: "muse-acp", Agent: runner.AgentConfig{Environment: env}}
			a, err := agentConfig(context.Background(), cfg, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if a.Environment["XDG_CONFIG_HOME"] != env["XDG_CONFIG_HOME"] {
				t.Fatalf("opt-out still redirected to %q", a.Environment["XDG_CONFIG_HOME"])
			}
		})
	}
}

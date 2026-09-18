package town

import (
	"context"
	"encoding/json"
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

// The refused profile is the only key dropped; every other setting survives.
func TestMuseConfigHomeDropsDefaultProfile(t *testing.T) {
	real, env := writeMuseConfig(t, `{"model":"muse-spark-1.3","permissions":{"schema_version":1,"default_profile":":auto-review"}}`)
	home := museConfigHome(t.TempDir(), env)
	if home == "" {
		t.Fatal("expected a derived config home")
	}
	raw, err := os.ReadFile(filepath.Join(home, "muse", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model       string         `json:"model"`
		Permissions map[string]any `json:"permissions"`
	}
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "muse-spark-1.3" {
		t.Fatalf("unrelated settings lost: %q", got.Model)
	}
	if _, ok := got.Permissions["default_profile"]; ok {
		t.Fatal("default_profile survived the derivation")
	}
	if got.Permissions["schema_version"] == nil {
		t.Fatal("sibling permission settings were dropped")
	}
	// Credentials and trust stay live rather than becoming stale copies.
	for _, name := range []string{"auth.json", "trust.json"} {
		link, err := os.Readlink(filepath.Join(home, "muse", name))
		if err != nil {
			t.Fatalf("%s is not a symlink: %v", name, err)
		}
		if link != filepath.Join(real, name) {
			t.Fatalf("%s points at %q", name, link)
		}
	}
}

// A config that names no default profile is left entirely alone.
func TestMuseConfigHomeSkipsUnaffectedConfig(t *testing.T) {
	_, env := writeMuseConfig(t, `{"model":"muse-spark-1.3"}`)
	if home := museConfigHome(t.TempDir(), env); home != "" {
		t.Fatalf("derived a config home for an unaffected config: %q", home)
	}
	_, env = writeMuseConfig(t, `{"permissions":{"schema_version":1}}`)
	if home := museConfigHome(t.TempDir(), env); home != "" {
		t.Fatalf("derived a config home for a profile-less permissions block: %q", home)
	}
}

// The derivation rebuilds, so edits to the real config land on the next run.
func TestMuseConfigHomeRebuilds(t *testing.T) {
	real, env := writeMuseConfig(t, `{"model":"one","permissions":{"default_profile":":auto-review"}}`)
	root := t.TempDir()
	museConfigHome(root, env)
	settings := `{"model":"two","permissions":{"default_profile":":auto-review"}}`
	if err := os.WriteFile(filepath.Join(real, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	home := museConfigHome(root, env)
	raw, err := os.ReadFile(filepath.Join(home, "muse", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model string `json:"model"`
	}
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "two" {
		t.Fatalf("stale derivation: model %q", got.Model)
	}
}

// stubHarnessPATH puts no-op executables for the supplement commands on PATH,
// so the launch path resolves without those harnesses being installed.
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
	stubHarnessPATH(t, "muse-acp", "anvil")
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
	stubHarnessPATH(t, "muse-acp")
	t.Setenv(museKeepPermissions, "keep")
	_, env := writeMuseConfig(t, `{"permissions":{"default_profile":":auto-review"}}`)
	cfg := Config{Harness: "muse-acp", Agent: runner.AgentConfig{Environment: env}}
	a, err := agentConfig(context.Background(), cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a.Environment["XDG_CONFIG_HOME"] != env["XDG_CONFIG_HOME"] {
		t.Fatalf("opt-out still redirected to %q", a.Environment["XDG_CONFIG_HOME"])
	}
}

package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
	home := MuseConfigHome(t.TempDir(), env)
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
	if home := MuseConfigHome(t.TempDir(), env); home != "" {
		t.Fatalf("derived a config home for an unaffected config: %q", home)
	}
	_, env = writeMuseConfig(t, `{"permissions":{"schema_version":1}}`)
	if home := MuseConfigHome(t.TempDir(), env); home != "" {
		t.Fatalf("derived a config home for a profile-less permissions block: %q", home)
	}
}

// The derivation rebuilds, so edits to the real config land on the next run.
func TestMuseConfigHomeRebuilds(t *testing.T) {
	real, env := writeMuseConfig(t, `{"model":"one","permissions":{"default_profile":":auto-review"}}`)
	root := t.TempDir()
	MuseConfigHome(root, env)
	settings := `{"model":"two","permissions":{"default_profile":":auto-review"}}`
	if err := os.WriteFile(filepath.Join(real, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	home := MuseConfigHome(root, env)
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

package mayorbot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadConfigDefaultsAndPaths(t *testing.T) {
	root, err := canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte(`{"remote":"mirror","github":{"repo":"o/r"},"directory":"checkout","state_directory":"state"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote != filepath.Join(root, "mirror") || cfg.Directory != filepath.Join(root, "checkout") || cfg.StateDirectory != filepath.Join(root, "state") || cfg.GitHubRepo() != "o/r" || cfg.Timeout != DefaultConfig().Timeout || cfg.MaxItems != DefaultConfig().MaxItems {
		t.Fatalf("config %+v", cfg)
	}
}
func TestReadConfigRejectsMalformedInput(t *testing.T) {
	for _, raw := range []string{
		`{"remote":"https://github.com/o/r.git","unknown":true}`,
		`{"remote":"https://github.com/o/r.git"} {}`,
		`{"remote":"https://github.com/o/r.git","timeout":"never"}`,
		`{"remote":"https://github.com/o/r.git","agent":{"command":[]}}`,
		`{"remote":"https://github.com/o/r.git","directory":"same","state_directory":"same/state"}`,
		`{"remote":"https://github.com/o/r.git","directory":"same","state_directory":"same-items/state"}`,
		`[]`,
	} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadConfig(path); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
func TestValidateConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"missing remote", func(c *Config) { c.Remote = "" }},
		{"option remote", func(c *Config) { c.Remote = "-flag" }},
		{"invalid branch", func(c *Config) { c.Branch = "../branch" }},
		{"empty directory", func(c *Config) { c.Directory = "" }},
		{"empty state", func(c *Config) { c.StateDirectory = "" }},
		{"overlap", func(c *Config) { c.StateDirectory = c.Directory }},
		{"missing agent", func(c *Config) { c.Agent.Command = nil }},
		{"blank effort", func(c *Config) { c.Agent.Effort = " " }},
		{"invalid repository", func(c *Config) { c.GitHub.Repo = "not-a-slug" }},
		{"invalid host", func(c *Config) { c.GitHub.Host = "https://github.com" }},
		{"empty verification command", func(c *Config) { c.Verify = []string{""} }},
		{"escaping instruction", func(c *Config) { c.InstructionFiles = []string{"../outside"} }},
		{"zero timeout", func(c *Config) { c.Timeout = 0 }},
		{"negative poll", func(c *Config) { c.Poll = -1 }},
		{"zero limit", func(c *Config) { c.MaxItems = 0 }},
		{"excess limit", func(c *Config) { c.MaxItems = 200 + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Remote = "https://github.com/o/r.git"
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			tc.change(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
func TestStateIdentityAndLocking(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Remote = "https://github.com/o/r.git"
	root := t.TempDir()
	cfg.Directory = filepath.Join(root, "checkout")
	cfg.StateDirectory = filepath.Join(root, "state")
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "locks"))
	if s, err := ReadState(cfg); err != nil || s != nil {
		t.Fatalf("missing state: %+v %v", s, err)
	}
	if err := writeState(cfg, newState(cfg)); err != nil {
		t.Fatal(err)
	}
	changed := cfg
	changed.Branch = "different"
	if _, err := ReadState(changed); err == nil {
		t.Fatal("foreign state accepted")
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDirectory, "state.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(cfg); err == nil {
		t.Fatal("corrupt state accepted")
	}
	unlock, err := lockConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	if release, err := lockConfig(cfg); err == nil {
		release()
		t.Fatal("concurrent lock acquired")
	}
	unlock()
	unlock = nil
	release, err := lockConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	release()
	for _, value := range []string{strings.Repeat("a", 40), strings.Repeat("b", 64)} {
		if !validCommit(value) {
			t.Fatal("valid commit refused")
		}
	}
	for _, value := range []string{"", strings.Repeat("a", 39), strings.Repeat("z", 40)} {
		if validCommit(value) {
			t.Fatal("invalid commit accepted")
		}
	}
}

package town

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Muse composes each session's permission profile from the operator's own
// `permissions.default_profile` setting, and MSP carries no wire override for
// it: `session/start` selects an approval mode and nothing else. A profile
// whose reviewer `muse serve` cannot reach — `:auto-review` is the one people
// hit, since its reviewer is available to the TUI but never to the stdio host
// — therefore refuses every session, and the adapter reports it as a login
// failure. That setting is interactive posture; the ACP child is driven by
// Town, so Town gives the child a derived config directory instead of asking
// every operator to edit their settings by hand.
//
// The derived directory rewrites `settings.json` without that one key and
// symlinks every sibling back to the real directory, so credentials, trust
// decisions and their refreshes stay shared and live. Set
// BROKK_TOWN_MUSE_PERMISSIONS=keep to launch against the unmodified config.
const museKeepPermissions = "BROKK_TOWN_MUSE_PERMISSIONS"

// museUserConfig reports the directory Muse reads its own config from.
func museUserConfig(env map[string]string) string {
	if xdg := museEnv(env, "XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "muse")
	}
	if home := museEnv(env, "HOME"); home != "" {
		return filepath.Join(home, ".config", "muse")
	}
	return ""
}

// museEnv prefers an explicit agent environment entry over the process one, so
// the derivation reads the same config the child would have read.
func museEnv(env map[string]string, key string) string {
	if v, ok := env[key]; ok {
		return v
	}
	return os.Getenv(key)
}

// museConfigHome derives the ACP-only config directory under root and returns
// the XDG_CONFIG_HOME to hand the child, or "" when nothing needs deriving.
//
// Every failure returns "": the derivation is best-effort, and a child left on
// the operator's own config reports the host's refusal itself.
func museConfigHome(root string, env map[string]string) string {
	if os.Getenv(museKeepPermissions) == "keep" {
		return ""
	}
	real := museUserConfig(env)
	if real == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(real, "settings.json"))
	if err != nil {
		return ""
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(raw, &settings) != nil {
		return ""
	}
	var permissions map[string]json.RawMessage
	if json.Unmarshal(settings["permissions"], &permissions) != nil {
		return ""
	}
	if _, ok := permissions["default_profile"]; !ok {
		return ""
	}
	delete(permissions, "default_profile")
	if settings["permissions"], err = json.Marshal(permissions); err != nil {
		return ""
	}
	rewritten, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return ""
	}
	home := filepath.Join(root, "muse-acp-config")
	if err = museMirror(real, filepath.Join(home, "muse"), rewritten); err != nil {
		return ""
	}
	return home
}

// museMirror rebuilds shadow as a view of real: settings.json is the rewritten
// file, every other entry a symlink. It rebuilds on each launch so edits to the
// real config take effect on the next worker run.
func museMirror(real, shadow string, settings []byte) error {
	if err := os.RemoveAll(shadow); err != nil {
		return err
	}
	if err := os.MkdirAll(shadow, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "settings.json" {
			continue
		}
		if err = os.Symlink(filepath.Join(real, e.Name()), filepath.Join(shadow, e.Name())); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(shadow, "settings.json"), settings, 0o600)
}

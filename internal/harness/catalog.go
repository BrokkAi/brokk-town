// Package harness consumes the official ACP registry without running an agent.
package harness

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const RegistryURL = "https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"

// The Apache-2.0 registry snapshot keeps offline startup and demos useful.
//
//go:embed registry.json
var bundled []byte

type Package struct {
	Package string            `json:"package"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}
type Binary struct {
	Archive string            `json:"archive"`
	Cmd     string            `json:"cmd"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	SHA256  string            `json:"sha256,omitempty"`
}
type Distribution struct {
	Npx    *Package          `json:"npx,omitempty"`
	Uvx    *Package          `json:"uvx,omitempty"`
	Binary map[string]Binary `json:"binary,omitempty"`
}
type Entry struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Description  string       `json:"description"`
	Repository   string       `json:"repository,omitempty"`
	Website      string       `json:"website,omitempty"`
	License      string       `json:"license,omitempty"`
	Distribution Distribution `json:"distribution"`
	// Only Town's explicitly requested supplements use an installed command.
	Command []string `json:"command,omitempty"`
	Setup   string   `json:"setup,omitempty"`
}
type index struct {
	Version string  `json:"version"`
	Agents  []Entry `json:"agents"`
}
type Option struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Repository  string `json:"repository,omitempty"`
	Source      string `json:"source"`
	Available   bool   `json:"available"`
	Setup       string `json:"setup"`
}
type Listing struct {
	Agents  []Option  `json:"agents"`
	Source  string    `json:"source"`
	Fetched time.Time `json:"fetched"`
	Stale   bool      `json:"stale"`
	Demo    bool      `json:"demo"`
}
type Catalog struct {
	mu      sync.RWMutex
	entries []Entry
	fetched time.Time
	cache   string
	demo    bool
	refresh chan struct{}
	url     string
	client  *http.Client
}

func Canonical(id string) string {
	switch strings.ToLower(id) {
	case "", "codex":
		return "codex-acp"
	case "claude":
		return "claude-acp"
	case "anvil", "brokkai/anvil":
		return "brokkai/anvil"
	case "muse-acp", "brokkai/muse-acp":
		return "brokkai/muse-acp"
	case "draupnir", "foundev/draupnir":
		return "foundev/draupnir"
	default:
		return strings.ToLower(id)
	}
}

var agentID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

func ValidID(id string) bool {
	id = Canonical(id)
	return agentID.MatchString(id) || id == "brokkai/anvil" || id == "brokkai/muse-acp" || id == "foundev/draupnir"
}
func Platform() string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	return runtime.GOOS + "-" + arch
}
func supplements() []Entry {
	return []Entry{
		{ID: "brokkai/anvil", Name: "Anvil", Version: "installed", Repository: "https://github.com/BrokkAi/anvil", Description: "Brokk.ai's ACP agent runtime", Command: []string{"anvil"}, Setup: "Uses anvil on PATH. Configure its model provider first. Install: npm install -g @brokkai/anvil"},
		{ID: "brokkai/muse-acp", Name: "Muse ACP", Version: "installed", Repository: "https://github.com/BrokkAi/muse-acp", Description: "Brokk.ai's ACP adapter for Muse Code", Command: []string{"muse-acp"}, Setup: "Uses muse-acp on PATH. Muse Code must also be installed and authenticated with muse login. Adapter releases are linked below."},
		{ID: "foundev/draupnir", Name: "Draupnir", Version: "installed", Repository: "https://github.com/foundev/draupnir", Description: "Portable ACP agent runtime", Command: []string{"draupnir"}, Setup: "Uses draupnir on PATH. Configure its model provider first. Install using the repository's release instructions."},
	}
}
func safeHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}
func validVector(args []string, env map[string]string) bool {
	if len(args) > 256 || len(env) > 256 {
		return false
	}
	for _, v := range args {
		if len(v) > 8192 || strings.ContainsRune(v, 0) {
			return false
		}
	}
	for k, v := range env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) || len(v) > 8192 {
			return false
		}
	}
	return true
}
func (e Entry) Validate() error {
	if !ValidID(e.ID) || e.ID != Canonical(e.ID) || e.Name == "" || len(e.Name) > 200 || e.Version == "" || len(e.Version) > 200 {
		return errors.New("invalid registry agent identity")
	}
	if len(e.Command) > 0 {
		for _, extra := range supplements() {
			if e.ID == extra.ID && len(e.Command) == 1 && e.Command[0] == extra.Command[0] {
				return nil
			}
		}
		return errors.New("unrecognized additional harness command")
	}
	if e.Distribution.Npx == nil && e.Distribution.Uvx == nil && len(e.Distribution.Binary) == 0 {
		return errors.New("agent has no supported distributions")
	}
	for _, p := range []*Package{e.Distribution.Npx, e.Distribution.Uvx} {
		if p != nil && (p.Package == "" || strings.HasPrefix(p.Package, "-") || strings.ContainsAny(p.Package, "\x00\r\n\t ") || !validVector(p.Args, p.Env)) {
			return errors.New("invalid package distribution")
		}
	}
	for _, b := range e.Distribution.Binary {
		// Windows separators are legal in the registry, even on Unix clients.
		cmd := strings.ReplaceAll(b.Cmd, "\\", "/")
		if !safeHTTPS(b.Archive) || !localPath(cmd) || !validVector(b.Args, b.Env) || (b.SHA256 != "" && (len(b.SHA256) != 64 || strings.Trim(strings.ToLower(b.SHA256), "0123456789abcdef") != "")) {
			return errors.New("invalid binary distribution")
		}
	}
	return nil
}
func parse(data []byte) ([]Entry, error) {
	var idx index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(idx.Version, "1.") || len(idx.Agents) == 0 || len(idx.Agents) > 1000 {
		return nil, errors.New("unsupported ACP registry index")
	}
	ids := map[string]bool{}
	for _, e := range idx.Agents {
		if !agentID.MatchString(e.ID) || len(e.Command) > 0 || ids[e.ID] {
			return nil, errors.New("duplicate or invalid registry agent")
		}
		if err := e.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", e.ID, err)
		}
		ids[e.ID] = true
	}
	return idx.Agents, nil
}
func copyEntries(entries []Entry) []Entry {
	b, _ := json.Marshal(entries)
	var result []Entry
	_ = json.Unmarshal(b, &result)
	return result
}
func Bundled(id string) (Entry, error) {
	entries, err := parse(bundled)
	if err != nil {
		return Entry{}, err
	}
	return lookup(append(entries, supplements()...), id, "")
}
func lookup(entries []Entry, id, version string) (Entry, error) {
	for _, e := range entries {
		if e.ID == Canonical(id) {
			if version != "" && e.Version != version {
				return Entry{}, errors.New("registry version changed; reload the harness list and select it again")
			}
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("unknown harness %q; refresh the official registry or use custom", id)
}
func New(dir string, demo bool) *Catalog {
	entries, _ := parse(bundled)
	c := &Catalog{entries: entries, cache: filepath.Join(dir, "registry-cache.json"), demo: demo, refresh: make(chan struct{}, 1), url: RegistryURL, client: &http.Client{Timeout: 25 * time.Second}}
	if !demo {
		var saved struct {
			Data    json.RawMessage `json:"data"`
			Fetched time.Time       `json:"fetched"`
		}
		if data, err := os.ReadFile(c.cache); err == nil && len(data) <= 4<<20 && json.Unmarshal(data, &saved) == nil {
			if entries, err := parse(saved.Data); err == nil {
				c.entries, c.fetched = entries, saved.Fetched
			}
		}
	}
	return c
}
func (c *Catalog) Lookup(id, version string) (Entry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return lookup(append(copyEntries(c.entries), supplements()...), id, version)
}
func (c *Catalog) List() Listing {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := Listing{Agents: []Option{}, Source: RegistryURL, Fetched: c.fetched, Stale: c.fetched.IsZero() || time.Since(c.fetched) > time.Hour, Demo: c.demo}
	for _, e := range append(copyEntries(c.entries), supplements()...) {
		source, setup, available := "registry", "", true
		switch {
		case len(e.Command) > 0:
			source, setup = "additional", e.Setup
		case e.Distribution.Npx != nil:
			setup = "Runs the registry's versioned npm package with npx."
		case e.Distribution.Uvx != nil:
			setup = "Runs the registry's Python package with uvx (install uv first)."
		default:
			_, available = e.Distribution.Binary[Platform()]
			setup = "Downloads the registry's native archive into Town's private cache when first used."
			if !available {
				setup = "No registry distribution for " + Platform() + ". Use a custom command for a separately installed agent."
			}
		}
		out.Agents = append(out.Agents, Option{e.ID, e.Name, e.Version, e.Description, e.Repository, source, available, setup})
	}
	sort.Slice(out.Agents, func(i, j int) bool { return strings.ToLower(out.Agents[i].Name) < strings.ToLower(out.Agents[j].Name) })
	return out
}
func (c *Catalog) Refresh(ctx context.Context) error {
	if c.demo {
		return nil
	}
	select {
	case c.refresh <- struct{}{}:
		defer func() { <-c.refresh }()
	case <-ctx.Done():
		return ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.url, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return errors.New("official ACP registry is unavailable; keeping the cached catalog")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ACP registry returned HTTP %d; keeping the cached catalog", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return errors.New("registry response is incomplete or too large")
	}
	entries, err := parse(data)
	if err != nil {
		return err
	}
	fetched := time.Now()
	saved, _ := json.Marshal(struct {
		Data    json.RawMessage `json:"data"`
		Fetched time.Time       `json:"fetched"`
	}{data, fetched})
	if err = atomicFile(c.cache, saved); err != nil {
		return err
	}
	c.mu.Lock()
	c.entries, c.fetched = entries, fetched
	c.mu.Unlock()
	return nil
}
func atomicFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".registry-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func identity(e Entry) string {
	data, _ := json.Marshal(e)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

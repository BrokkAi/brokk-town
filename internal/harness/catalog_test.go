package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(data []byte) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}
}
func sampleEntry() Entry {
	return Entry{ID: "test-agent", Name: "Test Agent", Version: "1.2.3", Distribution: Distribution{Npx: &Package{Package: "@example/agent@1.2.3", Args: []string{"--acp", "literal $(text)"}, Env: map[string]string{"SETTING": "value"}}}}
}
func indexBytes(t *testing.T, e ...Entry) []byte {
	t.Helper()
	b, err := json.Marshal(index{Version: "1.0.0", Agents: e})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBundledOfficialCatalogAndRequestedSupplements(t *testing.T) {
	entries, err := parse(bundled)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 30 {
		t.Fatal("bundled catalog is a handpicked subset")
	}
	c := New(t.TempDir(), true)
	for _, id := range []string{"codex", "claude", "gemini", "opencode", "kimi", "fast-agent", "BrokkAi/anvil", "BrokkAi/muse-acp", "foundev/draupnir"} {
		e, err := c.Lookup(id, "")
		if err != nil || e.ID != Canonical(id) {
			t.Fatal(id, e, err)
		}
	}
	if len(c.List().Agents) != len(entries)+3 {
		t.Fatal("supplements missing")
	}
	for _, e := range supplements() {
		if e.Repository == "" || e.Version != "installed" || len(e.Command) != 1 {
			t.Fatal(e)
		}
	}
	if _, err := c.Lookup("not-a-real-agent", ""); err == nil {
		t.Fatal("unknown harness silently selected")
	}
}

func TestRegistryRefreshPersistsAndKeepsLastGoodCatalog(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, false)
	entry := sampleEntry()
	data := indexBytes(t, entry)
	c.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != RegistryURL {
			t.Fatal(r.URL)
		}
		return response(data), nil
	})}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := c.Lookup(entry.ID, entry.Version)
	if err != nil {
		t.Fatal(err)
	}
	if c.List().Stale || c.List().Fetched.IsZero() {
		t.Fatal("refresh timestamp missing")
	}
	entry.Version = "2.0.0"
	entry.Distribution.Npx.Package = "@example/agent@2.0.0"
	data = indexBytes(t, entry)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first.Version != "1.2.3" || first.Distribution.Npx.Package != "@example/agent@1.2.3" {
		t.Fatal("returned definition mutated")
	}
	if _, err = c.Lookup(entry.ID, "1.2.3"); err == nil {
		t.Fatal("stale selection accepted")
	}
	reopened := New(dir, false)
	current, err := reopened.Lookup(entry.ID, "")
	if err != nil || current.Version != "2.0.0" {
		t.Fatal("cache did not survive restart", current, err)
	}
	data = []byte(`{"version":"2.0.0","agents":[]}`)
	if err = c.Refresh(context.Background()); err == nil {
		t.Fatal("bad registry accepted")
	}
	if current, err = c.Lookup(entry.ID, ""); err != nil || current.Version != "2.0.0" {
		t.Fatal("lost last good registry")
	}
	c.client.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if err = c.Refresh(context.Background()); err == nil {
		t.Fatal("offline refresh reported success")
	}
	if _, err = c.Lookup(entry.ID, ""); err != nil {
		t.Fatal(err)
	}
	// A corrupt cache must fall back to the bundled index, without crashing startup.
	if err = os.WriteFile(filepath.Join(dir, "registry-cache.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = New(dir, false).Lookup("codex", ""); err != nil {
		t.Fatal(err)
	}
}

func TestDemoCatalogNeverReadsNetworkOrLiveCache(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, true)
	c.client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { t.Fatal("demo network access"); return nil, nil })}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "registry-cache.json")); !os.IsNotExist(err) {
		t.Fatal("demo wrote a cache")
	}
}

func TestInvalidMetadataAndUnavailablePlatforms(t *testing.T) {
	e := sampleEntry()
	if _, err := parse(indexBytes(t, e, e)); err == nil {
		t.Fatal("duplicate registry entry accepted")
	}
	e.Command = []string{"sh", "-c", "bad"}
	if _, err := parse(indexBytes(t, e)); err == nil {
		t.Fatal("official registry injected an additional command")
	}
	e = sampleEntry()
	e.Distribution = Distribution{Binary: map[string]Binary{"windows-x86_64": {Archive: "https://example.com/a.zip", Cmd: "./agent.exe"}}}
	c := New(t.TempDir(), false)
	c.entries = []Entry{e}
	for _, v := range c.List().Agents {
		if v.ID == e.ID && v.Available {
			t.Fatal("unsupported platform enabled")
		}
	}
	e.Distribution.Binary["windows-x86_64"] = Binary{Archive: "http://example.com/a", Cmd: "../outside"}
	if err := e.Validate(); err == nil {
		t.Fatal("unsafe binary metadata accepted")
	}
}

func TestPackageCommandsAndAdditionalInstalledHarnesses(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"npx", "uvx", "anvil", "muse-acp", "draupnir"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	e := sampleEntry()
	command, env, err := Launch(context.Background(), t.TempDir(), e)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "npx"), "--yes", "--", "@example/agent@1.2.3", "--acp", "literal $(text)"}
	if !reflect.DeepEqual(command, want) || env["SETTING"] != "value" {
		t.Fatal(command, env)
	}
	e.Distribution = Distribution{Uvx: &Package{Package: "agent==1.2.3", Args: []string{"acp"}}}
	command, _, err = Launch(context.Background(), t.TempDir(), e)
	if err != nil || !reflect.DeepEqual(command, []string{filepath.Join(dir, "uvx"), "--", "agent==1.2.3", "acp"}) {
		t.Fatal(command, err)
	}
	for _, extra := range supplements() {
		command, _, err := Launch(context.Background(), t.TempDir(), extra)
		if err != nil || len(command) != 1 || command[0] != filepath.Join(dir, extra.Command[0]) {
			t.Fatal(extra.ID, command, err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	for _, extra := range supplements() {
		if _, _, err := Launch(context.Background(), t.TempDir(), extra); err == nil || !strings.Contains(err.Error(), extra.Name) {
			t.Fatal("missing harness did not fail clearly", err)
		}
	}
}

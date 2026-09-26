package mjolnir

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixture = `{"revision":42,"profiles":[{"id":"codex","harness":"codex","private":"secret-profile"}],"targets":[{"id":"localhost","kind":"local-bare","requires_project_directory":true,"availability":"ready"},{"id":"builder","kind":"ssh-bare","requires_project_directory":true,"availability":"unavailable","unavailable_reason":"Host is offline; reconnect it.","host":"build","ssh_key":"private-key"}],"bundles":[],"hosts":[{"id":"build","label":"Build machine","targets":["builder"],"stale":true,"refreshing":false,"has_error":true}],"default":{"profile_id":"codex","target_id":"localhost"}}`

func catalogFixture(t *testing.T, handler http.HandlerFunc) (*Catalog, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("private-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return New(dir, false, Connection{URL: server.URL + "/api/v1", TokenFile: token}), server
}

func TestCatalogContractCacheRestartAndUnavailableDaemon(t *testing.T) {
	var calls atomic.Int32
	c, server := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/options" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("unexpected request")
		}
		w.Header().Set("Mj-Api-Version", "1")
		fmt.Fprint(w, fixture)
	})
	if !c.List().Stale || calls.Load() != 0 {
		t.Fatal("construction/list performed I/O")
	}
	c.refreshNow(context.Background())
	v := c.List()
	if v.Error != "" || v.Stale || len(v.Targets) != 2 || v.Targets[1].UnavailableReason != "Host is offline; reconnect it." || v.Default.Target != "localhost" || !c.Contains(Selection{"builder", "codex"}) {
		t.Fatal(v)
	}
	v.Targets[0].ID = "mutated"
	v.Default.Target = "mutated"
	if c.List().Targets[0].ID != "localhost" || c.List().Default.Target != "localhost" {
		t.Fatal("listing aliases cache")
	}
	data, err := os.ReadFile(c.cache)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(c.cache)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	for _, secret := range []string{"private-token", "private-key", "secret-profile", c.connection.URL, c.connection.TokenFile} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("private data cached: %s", secret)
		}
	}
	server.Close()
	restarted := New(filepath.Dir(c.cache), false, c.connection)
	if len(restarted.List().Targets) != 2 {
		t.Fatal("cache lost on restart")
	}
	restarted.refreshNow(context.Background())
	after := restarted.List()
	if after.Error == "" || !after.Stale || after.Fetched != v.Fetched || len(after.Targets) != 2 {
		t.Fatal(after)
	}
	other := New(filepath.Dir(c.cache), false, Connection{URL: "http://127.0.0.1:1/api/v1", TokenFile: c.connection.TokenFile})
	if len(other.List().Targets) != 0 {
		t.Fatal("used another daemon's catalog")
	}
}

func TestFailuresPreserveLastCatalogAndDoNotExposeRawErrors(t *testing.T) {
	for _, name := range []string{"auth", "version", "redirect", "invalid", "oversized", "duplicate", "missing", "control"} {
		t.Run(name, func(t *testing.T) {
			var failing atomic.Bool
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Mj-Api-Version", "1")
				if !failing.Load() {
					fmt.Fprint(w, fixture)
					return
				}
				switch name {
				case "auth":
					w.WriteHeader(401)
					fmt.Fprint(w, "private-token")
				case "version":
					w.Header().Set("Mj-Api-Version", "2")
					fmt.Fprint(w, fixture)
				case "redirect":
					http.Redirect(w, r, "/private-token", 302)
				case "invalid":
					fmt.Fprint(w, "private-token")
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", maxBytes+1))
				case "duplicate":
					fmt.Fprint(w, strings.Replace(fixture, `"id":"builder"`, `"id":"localhost"`, 1))
				case "missing":
					fmt.Fprint(w, `{}`)
				case "control":
					fmt.Fprint(w, strings.Replace(fixture, "Host is offline; reconnect it.", `bad\nline`, 1))
				}
			})
			c.refreshNow(context.Background())
			failing.Store(true)
			c.refreshNow(context.Background())
			v := c.List()
			if v.Error == "" || !v.Stale || len(v.Targets) != 2 {
				t.Fatal(v)
			}
			if strings.Contains(v.Error, "private-token") {
				t.Fatal("raw error exposed")
			}
		})
	}
}

func TestBackgroundReadDoesNotBlockListAndCancels(t *testing.T) {
	started := make(chan struct{}, 10)
	var calls atomic.Int32
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no refresh")
	}
	for range 100 {
		c.RequestRefresh()
	}
	listed := make(chan Listing, 1)
	go func() { listed <- c.List() }()
	select {
	case v := <-listed:
		if !v.Refreshing {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("list blocked on daemon")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh did not cancel")
	}
	if calls.Load() != 1 || c.List().Error != "" {
		t.Fatal("duplicate requests or shutdown overwrote error", calls.Load(), c.List())
	}
}

func TestDemoAndInvalidConnectionsNeverContactDaemon(t *testing.T) {
	var calls atomic.Int32
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); t.Error("unexpected contact") })
	demo := New(filepath.Dir(c.cache), true, c.connection)
	demo.RequestRefresh()
	demo.refreshNow(context.Background())
	demo.Run(context.Background())
	if !demo.List().Demo || demo.List().Configured || len(demo.List().Targets) != 0 || calls.Load() != 0 {
		t.Fatal(demo.List())
	}
	for _, connection := range []Connection{
		{URL: "http://example.com/api/v1", TokenFile: "token"},
		{URL: "http://user:secret@127.0.0.1/api/v1", TokenFile: "token"},
		{URL: "http://127.0.0.1/api/v1?token=secret", TokenFile: "token"},
		{URL: "http://127.0.0.1/api/v1"},
	} {
		x := New(t.TempDir(), false, connection)
		x.RequestRefresh()
		x.Run(context.Background())
		data, _ := json.Marshal(x.List())
		if x.List().Error == "" || strings.Contains(string(data), "secret") {
			t.Fatal(string(data))
		}
	}
}

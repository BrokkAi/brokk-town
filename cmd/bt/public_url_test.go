package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// In a container the browser's published address is not where Town listens.
// connection.json keeps both, and a client on Town's host, such as bt status
// run with docker compose exec, reaches the one Town listens on.
func TestAClientOnTheHostReachesAPublishedService(t *testing.T) {
	t.Setenv("BROKK_TOWN_BOT_DIR", t.TempDir())
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, dir, "0.0.0.0:0", true, "", "http://127.0.0.1:9") }()
	defer func() {
		cancel()
		if err := <-served; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		conn, err := readConnection(dir)
		if err == nil {
			if conn.URL != "http://127.0.0.1:9" || conn.Local == "" || conn.Local == conn.URL {
				t.Fatalf("connection %+v", conn)
			}
			var state struct{}
			if err := request(ctx, conn, "GET", "/api/state", nil, &state); err != nil {
				t.Fatalf("the local address refused the client: %v", err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no connection.json")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Only a shared Town is published over HTTPS; a single-operator Town in a
// container keeps a plain loopback-forwarded address.
func TestOnlyASharedTownTakesAnHTTPSPublicURL(t *testing.T) {
	for _, tc := range []struct {
		url       string
		multiUser bool
		want      string
	}{
		{"https://town.example.com", true, "https://town.example.com"},
		{"https://town.example.com/", true, "https://town.example.com"},
		{"http://127.0.0.1:8080", true, "http://127.0.0.1:8080"},
		{"https://town.example.com", false, ""},
		{"https://town.example.com/town", true, ""},
		{"ftp://town.example.com", true, ""},
	} {
		got, err := serviceOrigin("0.0.0.0:8080", tc.url, tc.multiUser)
		if (err != nil) != (tc.want == "") || got != tc.want {
			t.Errorf("%s multi-user=%v: %q, %v", tc.url, tc.multiUser, got, err)
		}
	}
}

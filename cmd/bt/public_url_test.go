package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/auth"
	"github.com/BrokkAi/brokk-town/internal/town"
)

// testPublicURL is the address people would open for a test Town.
const testPublicURL = "https://town.example.com"

// sharedTown sets what every Town needs to start: a GitHub OAuth client, an
// allowlist, and each bot's URL and token. Nothing listens at those URLs; Town
// reaches a bot only when it dispatches to it.
func sharedTown(t *testing.T) {
	t.Helper()
	secret := filepath.Join(t.TempDir(), "github-client-secret")
	if err := os.WriteFile(secret, []byte("test-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(auth.EnvGitHubClientID, "test-client")
	t.Setenv(auth.EnvGitHubClientSecretFile, secret)
	t.Setenv(auth.EnvAllowGitHubUsers, "octocat")
	t.Setenv("BROKK_TOWN_CALLBACK_URL", "")
	for _, role := range town.AgentRoles {
		t.Setenv(botEnv(role, "URL"), "http://127.0.0.1:9")
		t.Setenv(botEnv(role, "TOKEN"), "test-bot-token")
		t.Setenv(botEnv(role, "TOKEN_FILE"), "")
	}
}

// People open Town at its published address, which is not where it listens.
// connection.json keeps both, and a client in Town's pod, such as bt status
// run with kubectl exec, reaches the one Town listens on.
func TestAClientInThePodReachesAPublishedService(t *testing.T) {
	sharedTown(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, dir, "0.0.0.0:0", "", testPublicURL) }()
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
			if conn.URL != testPublicURL || conn.Local == "" || conn.Local == conn.URL {
				t.Fatalf("connection %+v", conn)
			}
			var state struct{}
			if err := request(ctx, conn, "GET", "/api/state", nil, &state); err != nil {
				t.Fatalf("the local address refused the client: %v", err)
			}
			var session struct {
				Providers []string `json:"providers"`
			}
			if err := request(ctx, conn, "GET", "/api/session", nil, &session); err != nil || !slices.Contains(session.Providers, "github") {
				t.Fatalf("Town does not offer sign-in: %+v, %v", session, err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no connection.json")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPublicURLIsTheAddressPeopleOpen(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://town.example.com", "https://town.example.com"},
		{"https://town.example.com/", "https://town.example.com"},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"http://[::1]:8080", "http://[::1]:8080"},
		// Over plain HTTP to anything but loopback, sign-in cookies and the
		// access key would cross the network in the clear.
		{"http://town.example.com", ""},
		{"http://10.0.0.5:8080", ""},
		{"https://town.example.com/town", ""},
		{"ftp://town.example.com", ""},
		{"", ""},
	} {
		got, err := serviceOrigin("0.0.0.0:8080", tc.url)
		if (err != nil) != (tc.want == "") || got != tc.want {
			t.Errorf("%q: %q, %v", tc.url, got, err)
		}
	}
}

// Every Town is shared: it does not start without the address people open,
// sign-in, or a way to reach each bot.
func TestTownStartsOnlyWhenSharedAndEveryBotIsReachable(t *testing.T) {
	t.Run("public URL", func(t *testing.T) {
		sharedTown(t)
		err := run(t.Context(), []string{"--state-dir", t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "--public-url is required") {
			t.Fatalf("started without a public URL: %v", err)
		}
	})
	t.Run("sign-in", func(t *testing.T) {
		sharedTown(t)
		t.Setenv(auth.EnvGitHubClientID, "")
		t.Setenv(auth.EnvGitHubClientSecretFile, "")
		err := serve(t.Context(), t.TempDir(), "127.0.0.1:0", "", testPublicURL)
		if err == nil || !strings.Contains(err.Error(), "OAuth client") {
			t.Fatalf("started without sign-in: %v", err)
		}
	})
	t.Run("bots", func(t *testing.T) {
		sharedTown(t)
		t.Setenv(botEnv(town.Review, "URL"), "")
		err := serve(t.Context(), t.TempDir(), "127.0.0.1:0", "", testPublicURL)
		if err == nil || !strings.Contains(err.Error(), "review-bot (BROKK_TOWN_BOT_REVIEW_URL)") {
			t.Fatalf("started without review-bot: %v", err)
		}
	})
	t.Run("bots before the config file", func(t *testing.T) {
		sharedTown(t)
		t.Setenv(botEnv(town.Hall, "URL"), "")
		dir := t.TempDir()
		config := filepath.Join(t.TempDir(), "towns.json")
		if err := os.WriteFile(config, []byte(`{"max_workers":7,"towns":[{"repo":"acme/app","harness":"custom","agent":{"command":["fake"]},"merge_policy":"manual","poll_seconds":60,"report_seconds":1800,"max_cycles":5}]}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := serve(t.Context(), dir, "127.0.0.1:0", config, testPublicURL); err == nil || !strings.Contains(err.Error(), "mayor-bot") {
			t.Fatalf("started without mayor-bot: %v", err)
		}
		store, err := town.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if state := store.Snapshot(); len(state.Towns) != 0 || state.ServiceConfig.MaxWorkers == 7 {
			t.Fatalf("a Town that did not start applied its config file: %d towns, max_workers %d", len(state.Towns), state.ServiceConfig.MaxWorkers)
		}
	})
	t.Run("bot in the config file", func(t *testing.T) {
		sharedTown(t)
		t.Setenv(botEnv(town.Review, "URL"), "")
		t.Setenv(botEnv(town.Review, "TOKEN"), "")
		token := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(token, []byte("review-token\n"), 0600); err != nil {
			t.Fatal(err)
		}
		bots, err := townBots(map[town.Role]fileBot{town.Review: {URL: "http://review-bot:8080", TokenFile: token}})
		if err != nil || bots[town.Review] == nil || len(bots) != len(town.AgentRoles) {
			t.Fatalf("config file bot: %d bots, %v", len(bots), err)
		}
	})
}

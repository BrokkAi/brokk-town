package town

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type botVersionTransport func(*http.Request) (*http.Response, error)

func (f botVersionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckBotVersionsReadsEveryStablePackage(t *testing.T) {
	seen := map[string]bool{}
	client := &http.Client{Transport: botVersionTransport(func(r *http.Request) (*http.Response, error) {
		seen[r.URL.Path] = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"version":"1.2.3"}`))}, nil
	})}
	versions, err := CheckBotVersions(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range AgentRoles {
		if versions[role] != "1.2.3" || !seen["/"+workerPackageNames[role]+"/latest"] {
			t.Fatalf("missing %s package lookup: %#v %#v", role, versions, seen)
		}
	}
}

func TestBotVersionPinsAreValidatedAndPublic(t *testing.T) {
	cfg := DefaultConfig("acme/project")
	if cfg.BotVersion(Issue) != "0.5.2" {
		t.Fatal("default Issue Bot pin changed unexpectedly")
	}
	cfg.BotVersions = map[Role]string{Issue: "0.6.0"}
	if err := cfg.Validate(); err != nil || cfg.Public().BotVersions[Issue] != "0.6.0" {
		t.Fatalf("valid pin was not preserved: %v %#v", err, cfg.Public().BotVersions)
	}
	cfg.BotVersions[Issue] = "latest"
	if err := cfg.Validate(); err == nil {
		t.Fatal("floating bot version was accepted")
	}
}

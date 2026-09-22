package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestSettingsCLISendsSpecificProfileOrDefaults(t *testing.T) {
	type input struct {
		Town  string         `json:"town"`
		Role  string         `json:"role"`
		Agent map[string]any `json:"agent"`
	}
	received := make(chan input, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" && r.Method == "GET" {
			// The CLI probes a running service before every command.
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.URL.Path != "/api/settings" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("incorrect settings request")
		}
		var body input
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args  []string
		role  string
		agent map[string]any
	}{
		{[]string{"--model", "default-model"}, "", map[string]any{"model": "default-model"}},
		{[]string{"--role", "review", "--harness", "claude", "--model", "review-model", "--effort", "xhigh"}, "review", map[string]any{"harness": "claude", "model": "review-model", "effort": "xhigh"}},
		{[]string{"--role", "issue", "--model", "", "--effort", ""}, "issue", map[string]any{"model": "", "effort": ""}},
		{[]string{"--role", "review", "--inherit"}, "review", map[string]any{"inherit": true}},
	} {
		args := append([]string{"settings", "--state-dir", dir, "--repo", "Acme/Team"}, tc.args...)
		if err := run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		got := <-received
		if got.Town != "acme/team" || got.Role != tc.role || !reflect.DeepEqual(got.Agent, tc.agent) {
			t.Fatalf("got %+v for %v", got, tc.args)
		}
	}
	for _, flags := range [][]string{
		{"--role", "repo"}, {"--role", "all"}, {"--role", "invalid"},
		{"--inherit"}, {"--role", "review", "--inherit", "--model", "conflict"},
	} {
		args := append([]string{"settings", "--state-dir", dir, "--repo", "Acme/Team"}, flags...)
		if err := run(context.Background(), args); err == nil {
			t.Fatal("accepted invalid settings", flags)
		}
		select {
		case got := <-received:
			t.Fatal("invalid settings sent to service", got)
		default:
		}
	}
}

func TestBudgetCLISendsEditsAndRejectsUnenforceableCombinations(t *testing.T) {
	received := make(chan map[string]any, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" && r.Method == "GET" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer h.Close()
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	base := []string{"settings", "--state-dir", dir, "--repo", "Acme/Team"}

	if err := run(context.Background(), append(append([]string{}, base...), "--budget-period", "week", "--budget-attempts", "25")); err != nil {
		t.Fatal(err)
	}
	budget := (<-received)["budget"].(map[string]any)["budget"].(map[string]any)
	if budget["period"] != "week" || budget["max_attempts"].(float64) != 25 || budget["max_agent_minutes"].(float64) != 0 {
		t.Fatalf("budget payload = %+v", budget)
	}

	if err := run(context.Background(), append(append([]string{}, base...), "--budget-period", "none")); err != nil {
		t.Fatal(err)
	}
	if edit := (<-received)["budget"].(map[string]any); edit["budget"] != nil {
		t.Fatalf("--budget-period none did not clear the budget: %+v", edit)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--budget-attempts", "5"}, "--budget-period day|week|month is required"},
		{[]string{"--budget-period", "day"}, "no bundled agent harness reports usage"},
		{[]string{"--budget-period", "none", "--budget-attempts", "5"}, "removes the budget"},
		{[]string{"--role", "issue", "--budget-period", "day", "--budget-attempts", "5"}, "omit --role"},
	} {
		err := run(context.Background(), append(append([]string{}, base...), tc.args...))
		if err == nil {
			t.Fatalf("accepted %v", tc.args)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("error for %v = %q, want it to mention %q", tc.args, err, tc.want)
		}
	}
}

func TestConfigFileCarriesTownBudget(t *testing.T) {
	entries, _, err := decodeConfigFile([]byte(`[{"repo":"acme/team","harness":"custom","agent":{"command":["fake"]},"merge_policy":"bot","poll_seconds":60,"report_seconds":60,"max_cycles":1,"budget":{"period":"month","max_agent_minutes":90}}]`))
	if err != nil {
		t.Fatal(err)
	}
	budget := entries[0].Config.Budget
	if budget == nil || budget.Period != "month" || budget.MaxAgentMinutes != 90 {
		t.Fatalf("config budget = %+v", budget)
	}
	if err := entries[0].Config.Validate(); err != nil {
		t.Fatalf("a valid config budget was rejected: %v", err)
	}
	// An unknown period decodes; validation is what rejects it.
	bad, _, err := decodeConfigFile([]byte(`[{"repo":"acme/team","harness":"custom","agent":{"command":["fake"]},"merge_policy":"bot","poll_seconds":60,"report_seconds":60,"max_cycles":1,"budget":{"period":"never","max_attempts":1}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if err := bad[0].Config.Validate(); err == nil {
		t.Fatal("an invalid budget period was accepted")
	}
}

func TestExampleConfigValidates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	entries, limit, err := decodeConfigFile(data)
	if err != nil {
		t.Fatalf("the documented example does not decode: %v", err)
	}
	if limit == nil || len(entries) == 0 {
		t.Fatalf("example config produced %d towns and limit %v", len(entries), limit)
	}
	for _, entry := range entries {
		if err := entry.Config.Validate(); err != nil {
			t.Fatalf("the documented example does not validate: %v", err)
		}
	}
	cfg := entries[0].Config
	if cfg.Budget == nil {
		t.Fatal("the example no longer documents a budget")
	}
	issue, configured := cfg.PolicyForRole(town.Issue)
	if !configured || len(issue.Labels) == 0 || len(issue.Verify) == 0 {
		t.Fatalf("the example no longer documents an issue work policy: %+v", issue)
	}
	release, configured := cfg.PolicyForRole(town.Release)
	if !configured || release.Release == nil || release.Release.Burst == 0 {
		t.Fatalf("the example no longer documents release cadence: %+v", release)
	}
}

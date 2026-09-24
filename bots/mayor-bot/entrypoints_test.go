package mayorbot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func judgeFixture(head string) fixtureGitHub {
	pull, _ := json.Marshal(map[string]any{"number": 8, "state": "open", "title": "Pull", "head": map[string]string{"sha": head}, "base": map[string]string{"sha": head}})
	return fixtureGitHub{Routes: map[string]fixtureResponse{
		"repos/o/r/issues/7":          {Body: `{"number":7,"state":"open","title":"Issue","body":"Source evidence"}`},
		"repos/o/r/issues/7/comments": {Body: `[{"body":"Confirmed by a user"}]`},
		"repos/o/r/pulls/8":           {Body: string(pull)},
		"repos/o/r/issues/8/comments": {Body: "[]"},
	}}
}

func TestJudgePublicEntryPoint(t *testing.T) {
	for _, decision := range []string{"admit", "decline", "delay", "missing reason", "edit", "commit"} {
		t.Run(decision, func(t *testing.T) {
			remote, head := originRepo(t)
			cfg := testConfig(t, remote)
			originGit(t, remote)("update-ref", "refs/pull/8/head", head)
			reply := `MAYOR_DECISION {"decision":"` + decision + `","reason":"README.md supports this decision"}`
			if decision == "missing reason" {
				reply = `MAYOR_DECISION {"decision":"admit"}`
			}
			if decision == "edit" || decision == "commit" {
				reply = `MAYOR_DECISION {"decision":"admit","reason":"yes"}`
			}
			root := installFixture(t, &cfg, reply, judgeFixture(head))
			cfg.Agent.Environment["BOT_TEST_ACTION"] = decision
			request := JudgeRequest{Issue: 7, Arrival: json.RawMessage(`{"kind":"issue"}`)}
			if decision == "decline" {
				request = JudgeRequest{PR: 8, HeadSHA: head, Arrival: json.RawMessage(`{"kind":"pr"}`)}
			}
			got, err := Judge(context.Background(), cfg, request, nil)
			valid := decision == "admit" || decision == "decline"
			if valid && (err != nil || got.Decision != decision) {
				t.Fatalf("judgment %+v: %v", got, err)
			}
			if !valid && err == nil {
				t.Fatal("invalid judgment accepted")
			}
			prompts := fixtureLines[fixturePrompt](t, filepath.Join(root, "prompts"))
			if len(prompts) != 1 || !strings.HasPrefix(prompts[0].Directory, cfg.Directory+"-items"+string(os.PathSeparator)) {
				t.Fatalf("prompts %+v", prompts)
			}
			for _, call := range fixtureLines[fixtureCall](t, filepath.Join(root, "calls")) {
				if call.Method != "GET" {
					t.Fatal("Mayor wrote to GitHub")
				}
			}
		})
	}
}

func TestJudgeRejectsMismatchedRevisionBeforeAgent(t *testing.T) {
	remote, head := originRepo(t)
	cfg := testConfig(t, remote)
	root := installFixture(t, &cfg, "must not run", judgeFixture(head))
	_, err := Judge(context.Background(), cfg, JudgeRequest{PR: 8, HeadSHA: strings.Repeat("a", 40), Arrival: json.RawMessage(`{"kind":"pr"}`)}, nil)
	if err == nil || !strings.Contains(err.Error(), "head moved") {
		t.Fatalf("moved head: %v", err)
	}
	if len(fixtureLines[fixturePrompt](t, filepath.Join(root, "prompts"))) != 0 {
		t.Fatal("agent ran for a stale revision")
	}
}

func TestWriteBulletinPublicEntryPoint(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	window := Window{Since: since, Until: since.Add(time.Hour)}
	for _, scenario := range []string{"success", "empty", "outside window", "invalid window", "bad receipt"} {
		t.Run(scenario, func(t *testing.T) {
			remote, _ := originRepo(t)
			cfg := testConfig(t, remote)
			pulls := `[{"number":12,"state":"closed","title":"Useful fix","merged_at":"2026-09-01T00:30:00Z","updated_at":"2026-09-01T00:30:00Z"}]`
			if scenario == "empty" {
				pulls = "[]"
			}
			reply := `MAYOR_BULLETIN {"title":"Updates","summary":"Useful fixes","items":[{"kind":"fix","title":"Fixed export","detail":"Keeps filters","pulls":[12]}]}`
			if scenario == "outside window" {
				reply = strings.ReplaceAll(reply, "[12]", "[99]")
			}
			if scenario == "bad receipt" {
				reply = "No structured bulletin"
			}
			f := fixtureGitHub{Routes: map[string]fixtureResponse{"repos/o/r/pulls": {Body: pulls}, "repos/o/r/pulls/12/files": {Body: "[]"}}}
			root := installFixture(t, &cfg, reply, f)
			requested := window
			if scenario == "invalid window" {
				requested.Until = requested.Since
			}
			report, err := WriteBulletin(context.Background(), cfg, requested, nil)
			valid := scenario == "success" || scenario == "empty"
			if valid && (err != nil || report.Window != window) {
				t.Fatalf("report %+v: %v", report, err)
			}
			if !valid && err == nil {
				t.Fatal("invalid bulletin accepted")
			}
			saved, readErr := ReadState(cfg)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if scenario == "success" {
				if saved == nil || len(saved.Bulletins) != 1 || len(saved.Bulletins[0].Pulls) != 1 || saved.Bulletins[0].Pulls[0] != 12 {
					t.Fatalf("bulletin not saved: %+v", saved)
				}
			} else if saved != nil && len(saved.Bulletins) > 0 {
				t.Fatal("unsuccessful or empty bulletin recorded")
			}
			prompts := fixtureLines[fixturePrompt](t, filepath.Join(root, "prompts"))
			if (scenario == "empty" || scenario == "invalid window") && len(prompts) != 0 {
				t.Fatal("agent ran unnecessarily")
			}
			for _, call := range fixtureLines[fixtureCall](t, filepath.Join(root, "calls")) {
				if call.Method != "GET" {
					t.Fatal("bulletin wrote to GitHub")
				}
			}
		})
	}
}

func TestJudgeCancellationStopsAgent(t *testing.T) {
	remote, head := originRepo(t)
	cfg := testConfig(t, remote)
	root := installFixture(t, &cfg, "unused", judgeFixture(head))
	cfg.Agent.Environment["BOT_TEST_ACTION"] = "wait"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Judge(ctx, cfg, JudgeRequest{Issue: 7, Arrival: json.RawMessage(`{"kind":"issue"}`)}, nil)
		done <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("agent stopped before cancellation: %v", err)
		case <-ctx.Done():
			t.Fatal("agent never reached prompt")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(root, "prompts")); err == nil {
				cancel()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("cancelled judgment succeeded")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled agent did not stop")
				}
				return
			}
		}
	}
}

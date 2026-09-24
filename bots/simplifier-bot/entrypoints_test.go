package simplifierbot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const assessmentReply = `SIMPLIFY_RESULT {"decision":"admit","summary":"Useful fix","detail":"README.md supports the change"}`
const scanReply = `SIMPLIFY_SCAN {"summary":"One candidate","proposals":[{"title":"Simplify dispatch","concern":"Duplicate paths","alternative":"One path","subsystems":["internal/dispatch"],"evidence":["Two identical callers"]}]}`

func assessmentFixture(head string) fixtureGitHub {
	pull, _ := json.Marshal(map[string]any{"number": 8, "state": "open", "title": "Pull", "head": map[string]string{"sha": head}, "base": map[string]string{"sha": head}})
	return fixtureGitHub{Routes: map[string]fixtureResponse{
		"repos/o/r/issues/7":          {Body: `{"number":7,"state":"open","title":"Issue","body":"Source evidence"}`},
		"repos/o/r/issues/7/comments": {Body: `[{"body":"Confirmed by a user"}]`},
		"repos/o/r/pulls/8":           {Body: string(pull)},
		"repos/o/r/issues/8/comments": {Body: "[]"},
	}}
}

func TestAssessPublicEntryPoint(t *testing.T) {
	for _, mode := range []string{"suggest", "auto"} {
		for _, target := range []string{"issue", "pr"} {
			t.Run(mode+"/"+target, func(t *testing.T) {
				remote, head := originRepo(t)
				originGit(t, remote)("update-ref", "refs/pull/8/head", head)
				cfg := testConfig(t, remote)
				root := installFixture(t, &cfg, assessmentReply, assessmentFixture(head))
				issue, pr, name := 7, 0, "issue-7"
				if target == "pr" {
					issue, pr, name = 0, 8, "pr-8"
				}
				got, err := Assess(context.Background(), cfg, mode, issue, pr, nil)
				if err != nil || got.Decision != "admit" {
					t.Fatalf("assessment %+v: %v", got, err)
				}
				prompts := fixtureLines[fixturePrompt](t, filepath.Join(root, "prompts"))
				if len(prompts) != 1 || prompts[0].Directory != filepath.Join(cfg.Directory+"-items", name) || !strings.Contains(prompts[0].Text, "Mode: "+mode) {
					t.Fatalf("prompts %+v", prompts)
				}
				for _, call := range fixtureLines[fixtureCall](t, filepath.Join(root, "calls")) {
					if call.Method != "GET" {
						t.Fatalf("assessment wrote: %+v", call)
					}
				}
			})
		}
	}
}

func TestAssessRejectsInvalidOrMutatingAgent(t *testing.T) {
	for _, tc := range []struct{ name, reply, action string }{
		{"missing receipt", "Looks good", ""},
		{"missing detail", `SIMPLIFY_RESULT {"decision":"admit"}`, ""},
		{"unknown verdict", `SIMPLIFY_RESULT {"decision":"merge","detail":"yes"}`, ""},
		{"tracked edit", assessmentReply, "edit"},
		{"changed HEAD", assessmentReply, "commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote, head := originRepo(t)
			cfg := testConfig(t, remote)
			installFixture(t, &cfg, tc.reply, assessmentFixture(head))
			cfg.Agent.Environment["BOT_TEST_ACTION"] = tc.action
			if _, err := Assess(context.Background(), cfg, "auto", 7, 0, nil); err == nil {
				t.Fatal("unsafe assessment accepted")
			}
		})
	}
}

func scanFixture() fixtureGitHub {
	return fixtureGitHub{Create: "success", Routes: map[string]fixtureResponse{
		"repos/o/r/issues": {Body: "[]"}, "repos/o/r/issues/comments": {Body: "[]"},
	}}
}

func TestRunPublicScanAndDryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "dry run"}[dry], func(t *testing.T) {
			remote, _ := originRepo(t)
			cfg := testConfig(t, remote)
			cfg.DryRun = dry
			root := installFixture(t, &cfg, scanReply, scanFixture())
			if err := Run(context.Background(), cfg, nil, true); err != nil {
				t.Fatal(err)
			}
			s, err := ReadState(cfg)
			if err != nil || s == nil || len(s.Proposals) != 1 {
				t.Fatalf("state %+v: %v", s, err)
			}
			want := "submitted"
			if dry {
				want = "dry_run"
			}
			if s.Proposals[0].Status != want || !validCommit(s.LastCommit) {
				t.Fatalf("state %+v", s)
			}
			calls := fixtureLines[fixtureCall](t, filepath.Join(root, "calls"))
			posts := 0
			for _, c := range calls {
				if c.Method == "POST" {
					posts++
				}
			}
			if (dry && posts != 0) || (!dry && posts != 1) {
				t.Fatalf("posts=%d dry=%v", posts, dry)
			}
			if err := Run(context.Background(), cfg, nil, true); err != nil {
				t.Fatal(err)
			}
			if len(fixtureLines[fixturePrompt](t, filepath.Join(root, "prompts"))) != 1 {
				t.Fatal("scheduled scan ran twice")
			}
		})
	}
}

func TestRunLostCreateResponseReconcilesWithoutReposting(t *testing.T) {
	remote, _ := originRepo(t)
	cfg := testConfig(t, remote)
	f := scanFixture()
	f.Create = "lost"
	root := installFixture(t, &cfg, scanReply, f)
	if err := Run(context.Background(), cfg, nil, true); err == nil {
		t.Fatal("lost response reported success")
	}
	saved, err := ReadState(cfg)
	if err != nil || saved == nil || len(saved.Proposals) != 1 || saved.Proposals[0].Status != "posting" {
		t.Fatalf("missing durable intent: %+v %v", saved, err)
	}
	requestID := saved.Proposals[0].RequestID
	// Earlier versions accepted evidence-free proposals. They must remain
	// readable so their uncertain writes can still be reconciled.
	saved.Proposals[0].Evidence = nil
	if err := writeState(cfg, saved); err != nil {
		t.Fatal(err)
	}
	f.State = cfg.StateDirectory
	f.Hidden = true
	saveGitHubFixture(t, root, f)
	for i := 0; i < 2; i++ {
		if err := Run(context.Background(), cfg, nil, true); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("uncertainty lost: %v", err)
		}
	}
	f.Hidden = false
	saveGitHubFixture(t, root, f)
	if err := Run(context.Background(), cfg, nil, true); err != nil {
		t.Fatal(err)
	}
	saved, err = ReadState(cfg)
	if err != nil || saved.Proposals[0].Status != "submitted" || saved.Proposals[0].RequestID != requestID || saved.Proposals[0].URL != "https://github.com/o/r/issues/42" {
		t.Fatalf("recovery %+v: %v", saved, err)
	}
	posts := 0
	for _, call := range fixtureLines[fixtureCall](t, filepath.Join(root, "calls")) {
		if call.Method == "POST" {
			posts++
		}
	}
	if posts != 1 || len(fixtureLines[fixturePrompt](t, filepath.Join(root, "prompts"))) != 1 {
		t.Fatalf("repeated work: posts %d", posts)
	}
}

func TestRunRejectsIncompleteProposalsBeforeWriting(t *testing.T) {
	for _, reply := range []string{
		"No receipt",
		`SIMPLIFY_SCAN {"summary":"missing proposals"}`,
		`SIMPLIFY_SCAN {"summary":"bad","proposals":[{"title":"x","concern":"x","alternative":"x","subsystems":["src"]}]}`,
		`SIMPLIFY_SCAN {"summary":"bad","proposals":[{"title":"x","concern":"x","alternative":"x","subsystems":["../outside"],"evidence":["x"]}]}`,
	} {
		t.Run(reply, func(t *testing.T) {
			remote, _ := originRepo(t)
			cfg := testConfig(t, remote)
			root := installFixture(t, &cfg, reply, scanFixture())
			if err := Run(context.Background(), cfg, nil, true); err == nil {
				t.Fatal("invalid scan accepted")
			}
			for _, call := range fixtureLines[fixtureCall](t, filepath.Join(root, "calls")) {
				if call.Method != "GET" {
					t.Fatal("invalid scan wrote to GitHub")
				}
			}
			s, err := ReadState(cfg)
			if err != nil || (s != nil && len(s.Proposals) > 0) {
				t.Fatalf("invalid proposals persisted: %+v %v", s, err)
			}
		})
	}
}

func TestAssessCancellationStopsAgent(t *testing.T) {
	remote, head := originRepo(t)
	cfg := testConfig(t, remote)
	root := installFixture(t, &cfg, assessmentReply, assessmentFixture(head))
	cfg.Agent.Environment["BOT_TEST_ACTION"] = "wait"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Assess(ctx, cfg, "auto", 7, 0, nil); done <- err }()
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
						t.Fatal("cancelled assessment succeeded")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled agent did not stop")
				}
				return
			}
		}
	}
}

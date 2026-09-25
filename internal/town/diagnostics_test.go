package town

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

func TestMergeBlockersPreventWritesAndRecover(t *testing.T) {
	for _, tc := range []struct {
		name, code, status string
		change             func(*fakeGH, *Task)
	}{
		{"stale audit", "town_review", "blocked", func(g *fakeGH, task *Task) { task.Audit.Head = fixSHA }},
		{"different revision", "town_review", "blocked", func(g *fakeGH, task *Task) { g.gate.Head = fixSHA }},
		{"approval", "approval", "blocked", func(g *fakeGH, task *Task) { g.gate.Review = "REVIEW_REQUIRED"; g.gate.MergeState = "BLOCKED" }},
		{"changes", "approval", "blocked", func(g *fakeGH, task *Task) { g.gate.Review = "CHANGES_REQUESTED" }},
		{"conflict", "conflicts", "blocked", func(g *fakeGH, task *Task) { g.gate.Mergeable = "CONFLICTING"; g.gate.MergeState = "DIRTY" }},
		{"mergeability unknown", "mergeability", "unknown", func(g *fakeGH, task *Task) { g.gate.Mergeable = "UNKNOWN" }},
		{"checks missing", "checks", "unknown", func(g *fakeGH, task *Task) { g.gate.MergeState = "BLOCKED" }},
		{"checks failing", "checks", "blocked", func(g *fakeGH, task *Task) {
			_ = json.Unmarshal([]byte(`{"statusCheckRollup":{"state":"FAILURE"}}`), &g.gate)
			g.gate.MergeState = "UNSTABLE"
		}},
		{"checks pending", "checks", "blocked", func(g *fakeGH, task *Task) {
			_ = json.Unmarshal([]byte(`{"statusCheckRollup":{"state":"PENDING"}}`), &g.gate)
			g.gate.MergeState = "BLOCKED"
		}},
		{"inconsistent clean checks", "checks", "blocked", func(g *fakeGH, task *Task) {
			_ = json.Unmarshal([]byte(`{"statusCheckRollup":{"state":"ERROR"}}`), &g.gate)
		}},
		{"queue", "merge_queue", "blocked", func(g *fakeGH, task *Task) { _ = json.Unmarshal([]byte(`{"mergeQueue":{"id":"queue"}}`), &g.gate) }},
		{"strategy", "merge_strategy", "blocked", func(g *fakeGH, task *Task) { g.gate.SquashAllowed = false }},
		{"policy unavailable", "merge_policy", "unknown", func(g *fakeGH, task *Task) { g.gate.PolicyKnown = false }},
		{"behind", "behind", "blocked", func(g *fakeGH, task *Task) { g.gate.MergeState = "BEHIND" }},
		{"other branch rule", "branch_rules", "unknown", func(g *fakeGH, task *Task) {
			_ = json.Unmarshal([]byte(`{"statusCheckRollup":{"state":"SUCCESS"}}`), &g.gate)
			g.gate.MergeState = "BLOCKED"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t, false)
			x := setupPR(t, s, 1)
			gh := newGH(1)
			update(t, s, func(st *State) { tc.change(gh, st.Towns[x.ID].Tasks["pr:1"]) })
			x = s.Snapshot().Towns[x.ID]
			sup := NewSupervisor(s, gh, observing{gh: gh})
			if _, err := sup.mergeReady(t.Context(), x, slog.Default()); err != nil {
				t.Fatal(err)
			}
			saved := s.Snapshot().Towns[x.ID]
			if gh.merged != 0 || len(saved.Intents) != 0 {
				t.Fatal("blocked gate wrote a merge intent or merged")
			}
			report := saved.Tasks["pr:1"].MergeWait
			if report == nil || report.Head != headSHA || report.Base != baseSHA || report.At.IsZero() {
				t.Fatalf("lost revision: %+v", report)
			}
			found := false
			for _, check := range report.Checks {
				if check.Code == tc.code {
					found = true
					if check.Status != tc.status || check.Action == "" || !strings.HasPrefix(check.URL, "https://github.com/acme/orchard/pull/1") {
						t.Fatal(check)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tc.code, report)
			}
			// The same diagnostic survives the actual persisted-state reopen.
			dir := filepath.Dir(s.path)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if !reflect.DeepEqual(reopened.Snapshot().Towns[x.ID].Tasks["pr:1"].MergeWait, report) {
				t.Fatal("merge diagnostic lost on restart")
			}
			update(t, reopened, func(st *State) { st.Towns[x.ID].Tasks["pr:1"].Audit = clean() })
			gh.gate = newGH(1).gate
			sup = NewSupervisor(reopened, gh, observing{gh: gh})
			if _, err := sup.mergeReady(t.Context(), reopened.Snapshot().Towns[x.ID], slog.Default()); err != nil {
				t.Fatal(err)
			}
			if gh.merged != 1 || reopened.Snapshot().Towns[x.ID].Tasks["pr:1"].MergeWait != nil {
				t.Fatal("corrected gate did not recover")
			}
		})
	}
}

type unavailableGate struct{ *fakeGH }

func (g unavailableGate) Gate(context.Context, string, int) (MergeGate, error) {
	return MergeGate{}, errors.New("secret credential from failed process")
}

func TestUnavailableMergeGateIsDurableAndDoesNotExposeOutput(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	gh := unavailableGate{newGH(1)}
	sup := NewSupervisor(s, gh, nil)
	if _, err := sup.mergeReady(t.Context(), x, slog.Default()); err == nil {
		t.Fatal("expected failed read")
	}
	saved := s.Snapshot().Towns[x.ID]
	raw, _ := json.Marshal(saved.Tasks["pr:1"].MergeWait)
	if gh.merged != 0 || len(saved.Intents) != 0 || strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), "github_unavailable") {
		t.Fatal(string(raw))
	}
}

func TestSetupChecksMissingCommandsAndNeverExecutesThem(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	update(t, s, func(st *State) {
		c := &st.Towns[x.ID].Config
		c.Harness = "custom"
		c.Agent.Command = []string{"agent", "private-argument"}
		c.Agent.Environment = map[string]string{"SECRET": "private-env"}
		c.Verify = []string{"verify", "private-verifier-argument"}
	})
	sup := NewSupervisor(s, nil, nil)
	report, err := sup.Diagnose(t.Context(), x.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCheck := func(report *DiagnosticReport, code string, role Role, status string) {
		t.Helper()
		for _, c := range report.Checks {
			if c.Code == code && c.Role == role {
				if c.Status != status {
					t.Fatal(c)
				}
				return
			}
		}
		t.Fatal("missing check", code, role)
	}
	assertCheck(report, "git", "", "blocked")
	assertCheck(report, "gh", "", "blocked")
	assertCheck(report, "agent", Issue, "blocked")
	assertCheck(report, "verify", Issue, "blocked")
	for _, name := range []string{"git", "gh", "agent", "verify"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf invoked > '"+dir+"/unexpected'\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	report, err = sup.Diagnose(t.Context(), x.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCheck(report, "git", "", "passed")
	assertCheck(report, "gh", "", "passed")
	assertCheck(report, "agent", Issue, "passed")
	assertCheck(report, "verify", Issue, "passed")
	assertCheck(report, "agent_auth", Issue, "unknown")
	assertCheck(report, "repository", "", "unknown")
	if _, err := os.Stat(filepath.Join(dir, "unexpected")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("diagnostics executed a configured command")
	}
	raw, _ := json.Marshal(s.Snapshot().Public())
	if strings.Contains(string(raw), "private-") {
		t.Fatal("private settings leaked into diagnostic projection")
	}
	if !reflect.DeepEqual(s.Snapshot().Towns[x.ID].Diagnostics, report) {
		t.Fatal("setup report not saved")
	}
}

func TestSetupGitHubUsesOnlyMetadataReadsAndDemoSkipsEverything(t *testing.T) {
	for _, demo := range []bool{false, true} {
		t.Run(strconv.FormatBool(demo), func(t *testing.T) {
			s := testStore(t, demo)
			x := addTown(t, s)
			gh := fakeGitHubCLI(t, ghRoute{prefix: apiPrefix + "GET user", stdout: `{"login":"reader"}`}, ghRoute{prefix: apiPrefix + "GET repos/acme/orchard", stdout: `{"full_name":"acme/orchard"}`})
			sup := NewSupervisor(s, GitHubClient{}, nil)
			report, err := sup.Diagnose(t.Context(), x.ID)
			if err != nil {
				t.Fatal(err)
			}
			calls := gh.calls(t)
			if demo {
				if len(calls) != 0 || len(report.Checks) != 1 || report.Checks[0].Code != "demo" {
					t.Fatal(calls, report)
				}
			} else {
				if len(calls) != 2 {
					t.Fatal(calls)
				}
				for _, c := range report.Checks {
					if (c.Code == "repository" || c.Code == "github_auth") && c.Status != "passed" {
						t.Fatal(c)
					}
				}
			}
		})
	}
}

type heldSetup struct {
	*fakeGH
	entered chan struct{}
	release chan struct{}
}

func (g *heldSetup) Actor(ctx context.Context) (string, error) {
	close(g.entered)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-g.release:
		return "operator", nil
	}
}
func (g *heldSetup) SetupAccess(context.Context, string) error { return nil }

func TestSetupCancellationAndSettingsChangesKeepLastReport(t *testing.T) {
	for _, cancelRead := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelRead), func(t *testing.T) {
			s := testStore(t, false)
			x := addTown(t, s)
			fakeGitHubCLI(t) // a local gh exists, but must never actually run
			previous := &DiagnosticReport{At: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC), Checks: []Diagnostic{{Code: "previous", Status: "unknown", Detail: "Previous result"}}}
			update(t, s, func(st *State) { st.Towns[x.ID].Diagnostics = previous })
			gh := &heldSetup{fakeGH: newGH(1), entered: make(chan struct{}), release: make(chan struct{})}
			sup := NewSupervisor(s, gh, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := sup.Diagnose(ctx, x.ID); done <- err }()
			<-gh.entered
			if _, err := sup.Diagnose(t.Context(), x.ID); err == nil || !strings.Contains(err.Error(), "already running") {
				t.Fatal("overlapping probe accepted", err)
			}
			if cancelRead {
				cancel()
			} else {
				update(t, s, func(st *State) { st.Towns[x.ID].Config.Agent.Model = "changed-model" })
				close(gh.release)
			}
			if err := <-done; err == nil {
				t.Fatal("canceled or stale probe was accepted")
			}
			if !reflect.DeepEqual(previous, s.Snapshot().Towns[x.ID].Diagnostics) {
				t.Fatal("last completed report overwritten")
			}
			sup.GitHub = nil
			if _, err := sup.Diagnose(t.Context(), x.ID); err != nil {
				t.Fatal("probe slot leaked", err)
			}
		})
	}
}

func TestSetupRelativeExecutablesAndPerBotOverride(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "review-agent"), []byte("must never run"), 0700); err != nil {
		t.Fatal(err)
	}
	update(t, s, func(st *State) {
		c := &st.Towns[x.ID].Config
		c.Harness = "custom"
		c.Agent.Command = []string{"missing-agent"}
		c.Verify = []string{"./verify-in-checkout"}
		c.BotAgents = map[Role]BotAgentConfig{Review: {Harness: "custom", Agent: runner.AgentConfig{Command: []string{"review-agent"}}}}
		c.BotPolicies = map[Role]BotPolicy{Review: {Verify: []string{"missing-verifier"}}}
	})
	report, err := NewSupervisor(s, nil, nil).Diagnose(t.Context(), x.ID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, c := range report.Checks {
		statuses[string(c.Role)+"/"+c.Code] = c.Status
	}
	for key, want := range map[string]string{"issue/agent": "blocked", "review/agent": "passed", "issue/verify": "unknown", "review/verify": "blocked"} {
		if statuses[key] != want {
			t.Fatal(key, statuses[key], want)
		}
	}
}

func TestOldMergeObservationCannotOverwriteNewRevision(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	sup := NewSupervisor(s, newGH(1), nil)
	checks := []Diagnostic{{Code: "checks", Status: "blocked", Detail: "Old check failed"}}
	if err := sup.saveMergeWait(x, x.Tasks["pr:1"], pull(1), checks); err != nil {
		t.Fatal(err)
	}
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], inventory(func() Pull { p := pull(1); p.Head.SHA = fixSHA; return p }()), time.Now())
	})
	if err := sup.saveMergeWait(x, x.Tasks["pr:1"], pull(1), checks); err != nil {
		t.Fatal(err)
	}
	current := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
	if current.MergeWait != nil || current.Detail == "Old check failed " || current.Head != fixSHA {
		t.Fatal("old observation overwrote new revision", current)
	}
}

func TestSetupCallerDeadlinePreservesLastCompletedReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := testStore(t, false)
		x := addTown(t, s)
		fakeGitHubCLI(t)
		previous := &DiagnosticReport{At: time.Now().UTC(), Checks: []Diagnostic{{Code: "previous", Status: "passed", Detail: "Last completed probe"}}}
		update(t, s, func(st *State) { st.Towns[x.ID].Diagnostics = previous })
		gh := &heldSetup{fakeGH: newGH(1), entered: make(chan struct{}), release: make(chan struct{})}
		sup := NewSupervisor(s, gh, nil)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if report, err := sup.Diagnose(ctx, x.ID); !errors.Is(err, context.DeadlineExceeded) || report != nil {
			t.Fatalf("expired caller was acknowledged: report=%+v err=%v", report, err)
		}
		if !reflect.DeepEqual(previous, s.Snapshot().Towns[x.ID].Diagnostics) {
			t.Fatal("caller deadline replaced the last completed report")
		}
	})
}

func TestMergeDiagnosticsCannotReplaceRetargetExplanation(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(strconv.FormatBool(clear), func(t *testing.T) {
			s := testStore(t, false)
			x := setupPR(t, s, 1)
			sup := NewSupervisor(s, newGH(1), nil)
			checks := []Diagnostic{{Code: "checks", Status: "blocked", Detail: "Old check failed", Action: "Inspect the check logs."}}
			if err := sup.saveMergeWait(x, x.Tasks["pr:1"], pull(1), checks); err != nil {
				t.Fatal(err)
			}
			x = s.Snapshot().Towns[x.ID]
			update(t, s, func(st *State) { Reconcile(st, st.Towns[x.ID], inventory(retarget(pull(1), "release")), time.Now()) })
			before := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
			if before.Head != x.Tasks["pr:1"].Head || before.Base != x.Tasks["pr:1"].Base || before.Stage != "ready" {
				t.Fatal("fixture changed revision or stage")
			}
			if clear {
				checks = nil
			}
			if err := sup.saveMergeWait(x, x.Tasks["pr:1"], pull(1), checks); err != nil {
				t.Fatal(err)
			}
			after := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
			if after.Detail != before.Detail || !after.Offbranch || !after.Blocked || after.MergeWait != nil {
				t.Fatalf("old gate replaced retarget explanation: %+v", after)
			}
		})
	}
}

func TestRepositoryCompletionRetiresMergeDiagnostics(t *testing.T) {
	for _, merged := range []bool{false, true} {
		t.Run(strconv.FormatBool(merged), func(t *testing.T) {
			s := testStore(t, false)
			x := setupPR(t, s, 1)
			sup := NewSupervisor(s, newGH(1), nil)
			if err := sup.saveMergeWait(x, x.Tasks["pr:1"], pull(1), []Diagnostic{{Code: "checks", Status: "blocked", Detail: "Checks failed", Action: "Fix them."}}); err != nil {
				t.Fatal(err)
			}
			p := pull(1)
			p.State = "closed"
			if merged {
				now := time.Now()
				p.MergedAt = &now
				p.MergeCommit = fixSHA
			}
			update(t, s, func(st *State) { Reconcile(st, st.Towns[x.ID], inventory(p), time.Now()) })
			task := s.Snapshot().Towns[x.ID].Tasks["pr:1"]
			if task.MergeWait != nil || strings.Contains(task.Detail, "Checks failed") {
				t.Fatal("completed task kept old blocker", task)
			}
		})
	}
}

func TestSetupReadDeadlineStillSavesUnknownResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := testStore(t, false)
		x := addTown(t, s)
		fakeGitHubCLI(t)
		gh := &heldSetup{fakeGH: newGH(1), entered: make(chan struct{}), release: make(chan struct{})}
		sup := NewSupervisor(s, gh, nil)
		report, err := sup.Diagnose(t.Context(), x.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range report.Checks {
			if check.Code == "github_auth" && check.Status != "unknown" {
				t.Fatal("timed-out read passed", check)
			}
		}
		if !reflect.DeepEqual(report, s.Snapshot().Towns[x.ID].Diagnostics) {
			t.Fatal("completed report was not saved")
		}
	})
}

package featurebot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

func ptr(s string) *string { return &s }

// promptKind classifies a prompt as discovery, review, coverage correction or
// receipt recovery for either receipt.
func promptKind(prompt string) string {
	switch {
	case strings.Contains(prompt, "Previous answer (data):") && strings.Contains(prompt, "FEATURE_RESULT receipt, for example"):
		return "scan-recovery"
	case strings.Contains(prompt, "Previous answer (data):"):
		return "review-recovery"
	case strings.Contains(prompt, "Correct the rejected review receipt"):
		return "correction"
	case strings.Contains(prompt, "Scan context (data):"):
		return "scan"
	case strings.Contains(prompt, "Independently inspect code"):
		return "validate"
	}
	return "review"
}

// stagedAgent records the stage and selection each prompt was sent with.
type stagedAgent struct {
	stage  string
	config AgentConfig
	calls  *[]string
	answer func(context.Context, string, string) (string, error)
}

func (a stagedAgent) Execute(ctx context.Context, prompt string) (string, error) {
	kind := promptKind(prompt)
	*a.calls = append(*a.calls, fmt.Sprintf("%s:%s %s/%s", a.stage, kind, a.config.Model, a.config.Effort))
	return a.answer(ctx, kind, prompt)
}

type stagedRun struct {
	calls, sessions []string
}

// stagedFixture uses research model scout/high; answers come from base unless
// answer is replaced.
func stagedFixture(t *testing.T) (engine, *State, *fakeSource, *fakeAgent, *stagedRun) {
	t.Helper()
	e, s, f, base, _ := fixture(t)
	e.config.Agent.Model, e.config.Agent.Effort = "scout", "high"
	run := &stagedRun{}
	e.agent = func(cfg Config, stage string) Agent {
		run.sessions = append(run.sessions, stage)
		return stagedAgent{stage: stage, config: cfg.Agent, calls: &run.calls, answer: func(ctx context.Context, _, prompt string) (string, error) {
			return base.Execute(ctx, prompt)
		}}
	}
	return e, s, f, base, run
}

func TestReviewSelectionInheritsResearch(t *testing.T) {
	for _, tc := range []struct {
		model, effort *string
		want          string
	}{
		{nil, nil, "scout/high"},
		{ptr("judge"), nil, "judge/high"},
		{nil, ptr("low"), "scout/low"},
		{ptr("judge"), ptr("low"), "judge/low"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			e, s, f, _, run := stagedFixture(t)
			e.config.ReviewModel, e.config.ReviewEffort = tc.model, tc.effort
			if err := e.step(context.Background(), s, true); err != nil {
				t.Fatal(err)
			}
			want := []string{"discovery:scan scout/high", "review:validate " + tc.want}
			if strings.Join(run.calls, ",") != strings.Join(want, ",") || f.creates != 1 {
				t.Fatalf("calls %v, creates %d", run.calls, f.creates)
			}
			if strings.Join(run.sessions, ",") != "discovery,review" {
				t.Fatalf("sessions %v", run.sessions)
			}
		})
	}
}

// Discovery and its receipt recovery keep the research selection; every review
// batch, coverage correction and review receipt recovery uses the review selection.
func TestReviewSelectionForEveryReviewCall(t *testing.T) {
	e, s, f, base, run := stagedFixture(t)
	e.config.ReviewModel, e.config.ReviewEffort = ptr("judge"), ptr("low")
	// One issue split into two review batches.
	f.items = []Issue{{Number: 7, Title: "Unrelated", Body: strings.Repeat("a", 24000) + "tail", State: "open"}}
	reviews := 0
	e.agent = func(cfg Config, stage string) Agent {
		run.sessions = append(run.sessions, stage)
		return stagedAgent{stage: stage, config: cfg.Agent, calls: &run.calls, answer: func(ctx context.Context, kind, prompt string) (string, error) {
			switch kind {
			case "scan-recovery":
				return strings.Split(prompt, "Previous answer (data):\n")[1] + "]}", nil
			case "review-recovery":
				return strings.Split(prompt, "Previous answer (data):\n")[1] + "}", nil
			}
			text, err := base.Execute(ctx, prompt)
			switch kind {
			case "scan":
				return strings.TrimSuffix(text, "]}"), err
			case "correction":
				return strings.TrimSuffix(text, "}"), err
			}
			if reviews++; reviews == 1 {
				return `FEATURE_REVIEW {"verdict":"new","reason":"Checked nothing","checked":[]}`, nil
			}
			return text, err
		}}
	}
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"discovery:scan scout/high",
		"discovery:scan-recovery scout/high",
		"review:validate judge/low",
		"review:correction judge/low",
		"review:review-recovery judge/low",
		"review:review judge/low",
	}
	if strings.Join(run.calls, "\n") != strings.Join(want, "\n") || f.creates != 1 {
		t.Fatalf("creates %d, calls:\n%s", f.creates, strings.Join(run.calls, "\n"))
	}
}

func TestReviewCheckpointReviewerIdentity(t *testing.T) {
	for _, change := range []string{"unchanged", "same-effective", "model", "effort", "legacy"} {
		t.Run(change, func(t *testing.T) {
			e, s, source, base, run := stagedFixture(t)
			e.config.ReviewModel, e.config.ReviewEffort = ptr("judge"), ptr("low")
			// Three batches; the second is interrupted after the first is saved.
			source.items = []Issue{{Number: 7, Body: strings.Repeat("a", 24000) + strings.Repeat("b", 24000) + "last"}}
			reviews := 0
			e.agent = func(cfg Config, stage string) Agent {
				return stagedAgent{stage: stage, config: cfg.Agent, calls: &run.calls, answer: func(ctx context.Context, kind, prompt string) (string, error) {
					if kind != "scan" {
						if reviews++; reviews == 2 {
							return "", errors.New("interrupted")
						}
					}
					return base.Execute(ctx, prompt)
				}}
			}
			if err := e.step(context.Background(), s, true); err == nil {
				t.Fatal("expected interruption")
			}
			saved, err := ReadState(e.config)
			if err != nil {
				t.Fatal(err)
			}
			c := saved.Scan.Candidates[0]
			if !c.Checkpoint.Validated || len(c.Checkpoint.Batches) != 1 || c.Checkpoint.Reviewer == nil || *c.Checkpoint.Reviewer != (Reviewer{"judge", "low"}) {
				t.Fatalf("checkpoint %+v", c.Checkpoint)
			}
			switch change {
			case "same-effective":
				// Inheriting the same values is the same reviewer.
				e.config.Agent.Model, e.config.ReviewModel = "judge", nil
			case "model":
				e.config.ReviewModel = ptr("judge-2")
			case "effort":
				e.config.ReviewEffort = nil
			case "legacy":
				c.Checkpoint.Reviewer = nil
				if err := writeState(e.config, saved); err != nil {
					t.Fatal(err)
				}
				if saved, err = ReadState(e.config); err != nil {
					t.Fatal(err)
				}
			}
			saved.Scan.RetryAt = time.Time{}
			run.calls = nil
			e.agent = func(cfg Config, stage string) Agent {
				return stagedAgent{stage: stage, config: cfg.Agent, calls: &run.calls, answer: func(ctx context.Context, _, prompt string) (string, error) {
					return base.Execute(ctx, prompt)
				}}
			}
			if err := e.step(context.Background(), saved, true); err != nil {
				t.Fatal(err)
			}
			selected := e.config.ReviewAgent()
			current := selected.Model + "/" + selected.Effort
			want := []string{"review:review " + current, "review:review " + current}
			if change == "model" || change == "effort" || change == "legacy" {
				// Fresh validation and full coverage of all three batches.
				want = append([]string{"review:validate " + current}, want...)
			}
			if strings.Join(run.calls, ",") != strings.Join(want, ",") || source.creates != 1 || base.scans != 1 {
				t.Fatalf("calls %v, want %v, creates %d, scans %d", run.calls, want, source.creates, base.scans)
			}
			done := saved.Completed[len(saved.Completed)-1]
			if done.Status != "submitted" || *done.Checkpoint.Reviewer != (Reviewer{selected.Model, selected.Effort}) || len(done.Checkpoint.Batches) != 3 {
				t.Fatalf("completed %+v %+v", done, done.Checkpoint)
			}
		})
	}
}

func TestUnsupportedReviewSelectionPreservesCandidates(t *testing.T) {
	e, s, f, base, run := stagedFixture(t)
	e.config.ReviewModel = ptr("unoffered")
	e.agent = func(cfg Config, stage string) Agent {
		run.sessions = append(run.sessions, stage)
		return stagedAgent{stage: stage, config: cfg.Agent, calls: &run.calls, answer: func(ctx context.Context, _, prompt string) (string, error) {
			if cfg.Agent.Model == "unoffered" {
				return "", &runner.SetupError{Err: errors.New(`review model "unoffered" was not accepted by the ACP adapter; choose a value it offers with --review-model or review_model`)}
			}
			return base.Execute(ctx, prompt)
		}}
	}
	err := e.step(context.Background(), s, true)
	var setup *runner.SetupError
	if !errors.As(err, &setup) || !strings.Contains(err.Error(), "--review-model") {
		t.Fatalf("expected actionable setup error, got %v", err)
	}
	saved, readErr := ReadState(e.config)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if f.creates != 0 || saved.Scan == nil || !saved.Scan.Discovered || saved.Scan.Tries != 0 || len(saved.Scan.Candidates) != 1 || saved.Scan.Candidates[0].Status != "pending" {
		t.Fatalf("candidate not preserved: creates %d, scan %+v", f.creates, saved.Scan)
	}
	// No fallback: no review ran with the research selection.
	if strings.Join(run.calls, ",") != "discovery:scan scout/high,review:validate unoffered/high" {
		t.Fatalf("calls %v", run.calls)
	}
	e.config.ReviewModel = ptr("judge")
	run.calls = nil
	if err := e.step(context.Background(), saved, true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(run.calls, ",") != "review:validate judge/high" || base.scans != 1 || f.creates != 1 {
		t.Fatalf("calls %v, scans %d, creates %d", run.calls, base.scans, f.creates)
	}
}

// Review overrides change only who reviews, never the publication gates.
func TestReviewSelectionKeepsPublicationGates(t *testing.T) {
	for _, verdict := range []string{"duplicate", "uncertain", "invalid"} {
		t.Run(verdict, func(t *testing.T) {
			e, s, f, base, _ := stagedFixture(t)
			e.config.ReviewModel, e.config.ReviewEffort = ptr("judge"), ptr("low")
			f.items = []Issue{{Number: 9, Title: "Existing", State: "open"}}
			base.onReview = func([]Issue) Review {
				r := Review{Verdict: verdict, Reason: "Reviewed against the existing proposal"}
				if verdict == "duplicate" {
					r.Duplicate = 9
				}
				return r
			}
			if err := e.step(context.Background(), s, true); err != nil {
				t.Fatal(err)
			}
			if f.creates != 0 || s.Completed[0].Status != verdict {
				t.Fatalf("creates %d, status %s", f.creates, s.Completed[0].Status)
			}
		})
	}
	t.Run("verify", func(t *testing.T) {
		e, s, f, _, _ := stagedFixture(t)
		e.config.ReviewModel = ptr("judge")
		e.config.Verify = []string{"sh", "-c", "exit 7"}
		if err := e.step(context.Background(), s, true); err == nil || !strings.Contains(err.Error(), "operator verification") {
			t.Fatalf("verification bypassed: %v", err)
		}
		if f.creates != 0 || s.Scan.Candidates[0].Status != "pending" {
			t.Fatal("published despite failed verification")
		}
	})
	t.Run("dry-run", func(t *testing.T) {
		e, s, f, _, _ := stagedFixture(t)
		e.config.ReviewModel, e.config.DryRun = ptr("judge"), true
		if err := e.step(context.Background(), s, true); err != nil {
			t.Fatal(err)
		}
		if f.creates != 0 || s.Completed[0].Status != "dry_run" {
			t.Fatal("dry run published")
		}
	})
}

func TestLogsIdentifyStageSelections(t *testing.T) {
	e, s, _, _, _ := stagedFixture(t)
	e.config.ReviewModel = ptr("judge")
	e.config.Agent.Environment = map[string]string{"API_KEY": "secret-value"}
	var out strings.Builder
	e.log = slog.New(slog.NewTextHandler(&out, nil))
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`msg="Researching new features" stage=discovery model=scout effort=high`,
		`msg="Reviewing proposals" stage=review model=judge effort=high`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "secret-value") {
		t.Fatal("agent environment logged")
	}
}

func TestZeroFindingsStartNoReviewSession(t *testing.T) {
	e, s, _, base, run := stagedFixture(t)
	base.findings = []Finding{}
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(run.sessions, ",") != "discovery" {
		t.Fatalf("sessions %v", run.sessions)
	}
}

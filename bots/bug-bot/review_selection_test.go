package bugbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

func ptr(s string) *string { return &s }

// stagedAgent records the selection each prompt was sent with.
type stagedAgent struct {
	*fakeAgent
	stage  string
	config AgentConfig
	calls  *[]string
	fail   func(AgentConfig) error
}

func (a stagedAgent) Execute(ctx context.Context, prompt string) (string, error) {
	kind := "review"
	if strings.Contains(prompt, "Scan context (data):") {
		kind = "scan"
	}
	*a.calls = append(*a.calls, fmt.Sprintf("%s:%s %s/%s", a.stage, kind, a.config.Model, a.config.Effort))
	if a.fail != nil {
		if err := a.fail(a.config); err != nil {
			return "", err
		}
	}
	return a.fakeAgent.Execute(ctx, prompt)
}

func stagedFixture(t *testing.T) (engine, *State, *fakeSource, *fakeAgent, *[]string, *[]string) {
	t.Helper()
	e, s, f, a, _ := fixture(t)
	e.config.Agent.Model, e.config.Agent.Effort = "scout", "high"
	var calls, sessions []string
	e.agent = func(cfg Config, stage string) Agent {
		sessions = append(sessions, stage)
		return stagedAgent{fakeAgent: a, stage: stage, config: cfg.Agent, calls: &calls}
	}
	return e, s, f, a, &calls, &sessions
}

func TestReviewSelectionInheritsDiscovery(t *testing.T) {
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
			e, s, f, _, calls, _ := stagedFixture(t)
			e.config.ReviewModel, e.config.ReviewEffort = tc.model, tc.effort
			if err := e.step(context.Background(), s, true); err != nil {
				t.Fatal(err)
			}
			want := []string{"discovery:scan scout/high", "review:review " + tc.want}
			if strings.Join(*calls, ",") != strings.Join(want, ",") || f.creates != 1 {
				t.Fatalf("calls %v, creates %d", *calls, f.creates)
			}
		})
	}
}

func TestReviewSelectionAppliesToRefreshedHistoryBatches(t *testing.T) {
	e, s, f, a, calls, _ := stagedFixture(t)
	e.config.ReviewModel, e.config.ReviewEffort = ptr("judge"), ptr("low")
	f.items = []Issue{{Number: 3, Title: "Unrelated", Body: "old text", State: "open"}}
	a.findings = []Finding{finding(), func() Finding { g := finding(); g.Title = "Second"; g.RootCause = "Other"; return g }()}
	edited, created := false, false
	a.onReview = func(issues []Issue) Review {
		switch {
		case !edited:
			edited = true
			f.items[0].Body = "edited text"
		case !created:
			created = true
			f.items = append(f.items, Issue{Number: 4, Title: "Also unrelated", State: "open"})
		}
		return Review{Verdict: "new", Reason: "Distinct"}
	}
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	reviews := 0
	for _, call := range *calls {
		if strings.Contains(call, ":review") {
			reviews++
			if call != "review:review judge/low" {
				t.Fatalf("review batch used %q", call)
			}
		} else if call != "discovery:scan scout/high" {
			t.Fatalf("discovery used %q", call)
		}
	}
	// Two findings, each re-reviewing edited and newly created issues.
	if reviews < 4 || f.creates != 2 || !edited || !created {
		t.Fatalf("reviews %d, creates %d, calls %v", reviews, f.creates, *calls)
	}
}

func TestLogsDistinguishStageSelections(t *testing.T) {
	e, s, _, _, _, _ := stagedFixture(t)
	e.config.ReviewModel = ptr("judge")
	e.config.Agent.Environment = map[string]string{"API_KEY": "secret-value"}
	var out strings.Builder
	e.log = slog.New(slog.NewTextHandler(&out, nil))
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{`msg="Investigating new bugs" stage=discovery model=scout effort=high`, `msg="Reviewing findings" stage=review model=judge effort=high`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in logs:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret-value") {
		t.Fatal("agent environment logged")
	}
}

func TestZeroFindingsStartNoReviewSession(t *testing.T) {
	e, s, _, a, calls, sessions := stagedFixture(t)
	e.config.ReviewModel = ptr("judge")
	a.findings = []Finding{}
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*sessions, ",") != "discovery" || len(*calls) != 1 || a.reviews != 0 {
		t.Fatalf("sessions %v, calls %v", *sessions, *calls)
	}
}

func TestUnsupportedReviewSelectionPreservesPendingAndResumes(t *testing.T) {
	e, s, f, a, calls, _ := stagedFixture(t)
	e.config.ReviewModel = ptr("missing")
	e.agent = func(cfg Config, stage string) Agent {
		return stagedAgent{fakeAgent: a, stage: stage, config: cfg.Agent, calls: calls, fail: func(c AgentConfig) error {
			if c.Model == "missing" {
				return &runner.SetupError{Err: errors.New(`unknown Model "missing"`)}
			}
			return nil
		}}
	}
	var setup *runner.SetupError
	if err := e.step(context.Background(), s, true); !errors.As(err, &setup) {
		t.Fatalf("expected setup error, got %v", err)
	}
	if f.creates != 0 || a.reviews != 0 || s.Scan == nil || s.Scan.Tries != 0 || s.Scan.Candidates[0].Status != "pending" {
		t.Fatalf("unsupported review changed outcome: creates %d, scan %+v", f.creates, s.Scan)
	}
	for _, call := range *calls {
		if strings.HasPrefix(call, "review:") && call != "review:review missing/high" {
			t.Fatalf("review fell back to %q", call)
		}
	}
	saved, err := ReadState(e.config)
	if err != nil || saved.Scan == nil || !saved.Scan.Discovered {
		t.Fatalf("pending scan not saved: %+v, %v", saved, err)
	}
	*calls = nil
	e.config.ReviewModel = ptr("judge")
	if err := e.step(context.Background(), saved, true); err != nil {
		t.Fatal(err)
	}
	if a.scans != 1 || f.creates != 1 || strings.Join(*calls, ",") != "review:review judge/high" {
		t.Fatalf("resume repeated discovery or used stale selection: scans %d, calls %v", a.scans, *calls)
	}
}

func TestReviewSelectionCancellationPreservesPending(t *testing.T) {
	e, s, f, a, _, _ := stagedFixture(t)
	e.config.ReviewModel = ptr("judge")
	ctx, cancel := context.WithCancel(context.Background())
	a.onReview = func([]Issue) Review { cancel(); return Review{Verdict: "new", Reason: "Distinct"} }
	if err := e.step(ctx, s, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if f.creates != 0 || s.Scan.Candidates[0].Status != "pending" {
		t.Fatal("cancelled review published or dropped the finding")
	}
}

func TestReviewSelectionKeepsDryRunAndVerdicts(t *testing.T) {
	for _, mode := range []string{"dry_run", "duplicate", "uncertain", "verify"} {
		t.Run(mode, func(t *testing.T) {
			e, s, f, a, _, _ := stagedFixture(t)
			e.config.ReviewModel, e.config.ReviewEffort = ptr("judge"), ptr("low")
			want := mode
			switch mode {
			case "dry_run":
				e.config.DryRun = true
			case "verify":
				e.config.Verify = []string{"sh", "-c", "exit 3"}
			default:
				f.items = []Issue{{Number: 5, Title: "Existing", State: "open"}}
				a.onReview = func([]Issue) Review {
					r := Review{Verdict: mode, Reason: "Reviewed"}
					if mode == "duplicate" {
						r.Duplicate = 5
					}
					return r
				}
			}
			err := e.step(context.Background(), s, true)
			if f.creates != 0 {
				t.Fatal("published")
			}
			if mode == "verify" {
				if err == nil || !strings.Contains(err.Error(), "operator verification") {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil || s.Completed[0].Status != want {
				t.Fatalf("status %+v, %v", s.Completed, err)
			}
		})
	}
}

// Drives the real ACP runner: the selected review model reaches the agent, and a
// model the adapter does not advertise fails before any review prompt is sent.
func TestReviewSelectionThroughACPRunner(t *testing.T) {
	e, s, source, _, _ := fixture(t)
	dir := canonicalTestDir(t)
	script := filepath.Join(dir, "agent.py")
	record := filepath.Join(dir, "prompts.log")
	writeTestFile(t, script, `import json, os, sys
models = ['scout', 'judge']
def options(current):
    return [dict(id='model', name='Model', category='model', type='select', currentValue=current,
        options=[dict(value=m, name=m) for m in models])]
def send(value):
    print(json.dumps(value), flush=True)
current = 'scout'
for line in sys.stdin:
    request = json.loads(line)
    method = request.get('method')
    if method == 'initialize':
        result = dict(protocolVersion=1, agentCapabilities={}, authMethods=[])
    elif method == 'session/new':
        result = dict(sessionId='fixture', configOptions=options(current))
    elif method == 'session/set_config_option':
        current = request['params']['value']
        result = dict(configOptions=options(current))
    elif method == 'session/prompt':
        prompt = request['params']['prompt'][0]['text']
        scan = 'Scan context (data):' in prompt
        with open(os.environ['BUG_PROMPTS'], 'a') as f:
            f.write(('scan ' if scan else 'review ') + current + '\n')
        text = os.environ['BUG_SCAN'] if scan else 'BUG_REVIEW ' + os.environ['BUG_REVIEW']
        send(dict(jsonrpc='2.0', method='session/update', params=dict(sessionId='fixture',
            update=dict(sessionUpdate='agent_message_chunk', messageId='m', content=dict(type='text', text=text)))))
        result = dict(stopReason='end_turn')
    else:
        continue
    send(dict(jsonrpc='2.0', id=request['id'], result=result))
`)
	e.config.Agent.Command = []string{"python3", script}
	e.config.Agent.Model = "scout"
	e.config.Agent.Environment = map[string]string{
		"BUG_PROMPTS": record,
		"BUG_SCAN":    "BUG_RESULT " + jsonContextCompact(ScanResult{Summary: "Reproduced parser failure", Findings: []Finding{finding()}}),
		"BUG_REVIEW":  jsonContextCompact(Review{Verdict: "new", Reason: "Independently reproduced", Checked: []int{}}),
	}
	e.config.ReviewModel = ptr("unsupported")
	e.agent = func(cfg Config, stage string) Agent {
		return agentProcess{config: cfg, log: e.log.With("stage", stage)}
	}
	// Bound the real subprocesses so a stuck fake agent fails the test instead of hanging it.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var setup *runner.SetupError
	if err := e.step(ctx, s, true); !errors.As(err, &setup) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected setup error for unsupported review model, got %v", err)
	}
	if got, _ := os.ReadFile(record); string(got) != "scan scout\n" || source.creates != 0 || s.Scan.Candidates[0].Status != "pending" {
		t.Fatalf("prompts %q, creates %d, scan %+v", got, source.creates, s.Scan)
	}
	e.config.ReviewModel = ptr("judge")
	if err := e.step(ctx, s, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(record); string(got) != "scan scout\nreview judge\n" || source.creates != 1 {
		t.Fatalf("prompts %q, creates %d", got, source.creates)
	}
}

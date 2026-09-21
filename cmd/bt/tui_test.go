package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/town"
	"github.com/rivo/uniseg"
)

func TestTerminalFramesFitAndNeutralizeControls(t *testing.T) {
	s := town.NewState(true)
	x, _ := s.Add(town.DefaultConfig("acme/orchard"))
	_, _ = s.Add(town.DefaultConfig("acme/paper-trail"))
	x.Workers[town.Bug].Task = "危険\x1b[2J 👩‍💻 é"
	x.Report("New arrivals", "10 issues arrived", time.Now())
	x.Tasks["source:done"] = &town.Task{ID: "source:done", Kind: "source", Title: "Checked off in Slack", House: town.Issue, Stage: "complete"}
	for _, size := range [][2]int{{120, 35}, {80, 24}, {25, 10}, {10, 3}, {1, 1}} {
		for _, role := range []int{-1, 0, 3} {
			frame := renderTUI(s, "v0.1.2", 0, role, size[0], size[1], "status")
			lines := strings.Split(frame, "\n")
			if len(lines) > size[1] {
				t.Fatalf("height exceeded %v", size)
			}
			for _, line := range lines {
				if uniseg.StringWidth(line) > size[0] {
					t.Fatalf("width exceeded %v: %q", size, line)
				}
				if strings.ContainsRune(line, '\x1b') {
					t.Fatal("untrusted terminal escape")
				}
			}
		}
	}
	overview := renderTUI(s, "v0.1.2", 0, -1, 100, 30, "")
	if !strings.Contains(overview, "acme/orchard") || !strings.Contains(overview, "acme/paper-trail") {
		t.Fatal("overview omitted a town")
	}
	if strings.Contains(overview, "1 queued") {
		t.Fatal("done source item was counted as queued", overview)
	}
	issue := renderTUI(s, "", 0, 2, 100, 30, "")
	if strings.Contains(issue, "Checked off in Slack") {
		t.Fatal("done source item remained at the issue-bot door", issue)
	}
	if !strings.Contains(issue, "Authority:") || !strings.Contains(issue, "create pull requests") {
		t.Fatal("issue house omitted write authority", issue)
	}
	review := renderTUI(s, "", 0, 3, 140, 35, "")
	if !strings.Contains(review, "merge eligible pull requests when merge policy permits") {
		t.Fatal("review house omitted policy-dependent merge authority", review)
	}
}

func TestTUIShowsManualReleaseBoundaryAndWakeSet(t *testing.T) {
	state := town.NewState(false)
	config := town.DefaultConfig("acme/orchard")
	config.MergePolicy = "manual"
	_, _ = state.Add(config)
	frame := renderTUI(state, "", 0, 4, 140, 35, "")
	for _, want := range []string{"release-preparation pull requests", "Paused while every merge is manual", "wake available workers"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("manual release authority omitted %q: %s", want, frame)
		}
	}
}
func TestPastedCommandsNeverOperateTown(t *testing.T) {
	var d keyDecoder
	out := d.feed("\x1b[200~aspq\x1b[201~")
	if len(out) != 0 {
		t.Fatal("paste became controls", out)
	}
	out = d.feed("q")
	if len(out) != 1 || out[0] != "q" {
		t.Fatal(out)
	}
}
func TestBlockedIssueAttentionAndRecovery(t *testing.T) {
	s := town.NewState(false)
	x, _ := s.Add(town.DefaultConfig("acme/orchard"))
	blocked := &town.Task{
		ID: "issue:7", Kind: "issue", Number: 7, Title: "Clarify API contract",
		House: town.Issue, Stage: "blocked", Blocked: true, Attempts: 2,
		Detail: "The API response format is missing.",
		IssueJob: &town.IssueJob{
			Status: "blocked", RetryEligible: true,
			RetryDetail: "Clarify the issue on GitHub, then retry.",
		},
	}
	x.Tasks[blocked.ID] = blocked
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("issue:%d", i)
		x.Tasks[id] = &town.Task{ID: id, Kind: "issue", Number: i, Title: "Waiting issue", House: town.Issue, Stage: "queued"}
	}
	overview := renderTUI(s, "", 0, -1, 100, 24, "")
	if !strings.Contains(overview, "6 queued") || !strings.Contains(overview, "1 need attention") {
		t.Fatalf("blocked issue was missing from overview: %s", overview)
	}
	house := renderTUI(s, "", 0, 2, 100, 26, "")
	for _, detail := range []string{
		blocked.Title,
		blocked.Detail,
		"2 attempts · Clarify the issue on GitHub, then retry.",
		"bt retry --repo acme/orchard --task issue:7",
	} {
		if !strings.Contains(house, detail) {
			t.Fatalf("blocked issue inspection omitted %q: %s", detail, house)
		}
	}
	ordered := renderTUI(s, "", 0, 2, 100, 35, "")
	if strings.Index(ordered, blocked.Title) > strings.Index(ordered, "Waiting issue") {
		t.Fatalf("blocked issue should precede waiting work:\n%s", ordered)
	}
	blocked.IssueJob.RetryEligible = false
	blocked.IssueJob.RetryDetail = "Wait for the pending issue claim update."
	house = renderTUI(s, "", 0, 2, 100, 24, "")
	if strings.Contains(house, "bt retry") || !strings.Contains(house, blocked.IssueJob.RetryDetail) {
		t.Fatalf("unavailable retry was not explained: %s", house)
	}
	blocked.Blocked = false
	blocked.Stage = "implemented"
	blocked.IssueJob.Status = "submitted"
	overview = renderTUI(s, "", 0, -1, 100, 24, "")
	if !strings.Contains(overview, "5 queued") || !strings.Contains(overview, "0 need attention") {
		t.Fatalf("submitted issue still needed attention: %s", overview)
	}
	house = renderTUI(s, "", 0, 2, 100, 24, "")
	if strings.Contains(house, blocked.Title) || strings.Contains(house, blocked.Detail) {
		t.Fatalf("submitted issue remained in the queue: %s", house)
	}
}

func TestTUIHeaderShowsServiceVersion(t *testing.T) {
	s := town.NewState(true)
	_, _ = s.Add(town.DefaultConfig("acme/orchard"))
	withVersion := renderTUI(s, "v0.1.2", 0, -1, 100, 30, "")
	if !strings.Contains(withVersion, "BROKK TOWN v0.1.2") {
		t.Fatal("service version missing from TUI header", withVersion)
	}
	withoutVersion := renderTUI(s, "", 0, -1, 100, 30, "")
	if strings.Contains(withoutVersion, "BROKK TOWN v") {
		t.Fatal("empty version should omit the TUI suffix", withoutVersion)
	}
	if !strings.Contains(withoutVersion, "BROKK TOWN") {
		t.Fatal("TUI header missing without a version", withoutVersion)
	}
}

func TestNewTUIHandlesServiceBeforeFeatureUpgrade(t *testing.T) {
	s := town.NewState(false)
	x, _ := s.Add(town.DefaultConfig("acme/orchard"))
	delete(x.Workers, town.Feature)
	for role := range town.Roles {
		frame := renderTUI(s, "", 0, role, 140, 40, "")
		if !strings.Contains(frame, "Restart the town service") || !strings.Contains(frame, "unavailable") {
			t.Fatal("missing upgrade guidance", frame)
		}
	}
	if x.Workers[town.Feature] != nil {
		t.Fatal("renderer changed the service snapshot")
	}
}
func TestRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer h.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var result any
		done <- request(ctx, connection{URL: h.URL, Token: "key"}, "GET", "/api/state", nil, &result)
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("request blocked cancellation")
	}
}
func TestRejectPublicBindAndUnexpectedArguments(t *testing.T) {
	if e := serve(context.Background(), t.TempDir(), "0.0.0.0:8099", false, "", ""); e == nil {
		t.Fatal("public bind accepted")
	}
	if e := run(context.Background(), []string{"status", "unexpected"}); e == nil {
		t.Fatal("extra argument ignored")
	}
}

// A town that follows the repository default has no configured branch. The
// header must still name the branch the town works on.
func TestTUIHeaderShowsTheBranchInUse(t *testing.T) {
	s := town.NewState(true)
	follower, _ := s.Add(town.DefaultConfig("acme/follower"))
	follower.DefaultBranch = "trunk"
	chosen := town.DefaultConfig("acme/zchosen")
	chosen.Branch = "release"
	x, _ := s.Add(chosen)
	x.DefaultBranch = "trunk"
	if frame := renderTUI(s, "", 0, 0, 100, 24, ""); !strings.Contains(frame, "branch trunk") {
		t.Fatalf("a town following the repository default shows no branch:\n%s", frame)
	}
	if frame := renderTUI(s, "", 1, 0, 100, 24, ""); !strings.Contains(frame, "branch release") {
		t.Fatalf("a town with its own branch shows the wrong one:\n%s", frame)
	}
}

func TestTerminalNamesEachHousesHarnessModelAndEffort(t *testing.T) {
	s := town.NewState(false)
	config := town.DefaultConfig("acme/orchard")
	config.Agent.Model, config.Agent.Effort = "gpt-5-codex", "medium"
	config.BotAgents = map[town.Role]town.BotAgentConfig{
		town.Review: {Harness: "claude-acp", Agent: runner.AgentConfig{Model: "claude-opus-5", Effort: "high"}},
	}
	x, err := s.Add(config)
	if err != nil {
		t.Fatal(err)
	}
	// Bug-bot is mid-dispatch, so it must report the profile it was started
	// with rather than whatever the configuration says a later run will use.
	x.Workers[town.Bug].Status = "working"
	x.Workers[town.Bug].Agent = &town.PublicBotAgentConfig{Harness: "codex-acp", Model: "gpt-5-codex", Effort: "low", Inherited: true}
	roleIndex := func(role town.Role) int {
		for i, r := range town.Roles {
			if r == role {
				return i
			}
		}
		t.Fatalf("role %q is not a town house", role)
		return -1
	}

	frame := renderTUI(s, "", 0, roleIndex(town.Bug), 140, 35, "")
	for _, want := range []string{
		"AGENT ▸ running · * own",
		"▸ codex · gpt-5-codex · low",
		"* claude · claude-opus-5 · high",
		"  codex · gpt-5-codex · medium",
		"no agent · read-only",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("house table omitted %q:\n%s", want, frame)
		}
	}
	// The selected house spells the same profile out, unabbreviated.
	review := renderTUI(s, "", 0, roleIndex(town.Review), 140, 35, "")
	for _, want := range []string{
		"AGENT (next run · set for this house only)  harness claude-acp · model claude-opus-5 · effort high",
	} {
		if !strings.Contains(review, want) {
			t.Fatalf("selected house omitted %q:\n%s", want, review)
		}
	}
	for _, want := range []string{
		"AGENT (running now · inherited from town defaults)  harness codex-acp · model gpt-5-codex · effort low",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("running house omitted %q:\n%s", want, frame)
		}
	}
	// A narrow terminal keeps current work readable and drops the column.
	if narrow := renderTUI(s, "", 0, roleIndex(town.Bug), 80, 35, ""); strings.Contains(narrow, "AGENT ▸") {
		t.Fatalf("narrow terminal kept the agent column:\n%s", narrow)
	}
	if watchtower := renderTUI(s, "", 0, roleIndex(town.Repo), 140, 35, ""); strings.Contains(watchtower, "AGENT (") {
		t.Fatalf("the agentless watchtower advertised a profile:\n%s", watchtower)
	}
}

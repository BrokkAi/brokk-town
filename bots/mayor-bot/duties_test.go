package mayorbot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSource struct {
	issues  map[int]Issue
	pulls   map[int]Pull
	merged  []Pull
	changed map[int][]ChangedFile
}

func (f fakeSource) issue(_ context.Context, n int) (Issue, error) {
	i, ok := f.issues[n]
	if !ok {
		return Issue{}, errors.New("no such issue")
	}
	return i, nil
}
func (f fakeSource) pull(_ context.Context, n int) (Pull, error) {
	p, ok := f.pulls[n]
	if !ok {
		return Pull{}, errors.New("no such pull request")
	}
	return p, nil
}
func (f fakeSource) mergedPulls(context.Context, time.Time, time.Time) ([]Pull, error) {
	return f.merged, nil
}
func (f fakeSource) files(_ context.Context, n int) ([]ChangedFile, error) { return f.changed[n], nil }

type fakeAgent struct {
	reply   string
	prompts *[]string
	act     func(dir string) error
	dir     string
}

func (a fakeAgent) Execute(_ context.Context, prompt string) (string, error) {
	*a.prompts = append(*a.prompts, prompt)
	if a.act != nil {
		if err := a.act(a.dir); err != nil {
			return "", err
		}
	}
	return a.reply, nil
}

func testEngine(t *testing.T, remote, reply string, source fakeSource, act func(string) error) (engine, *[]string) {
	t.Helper()
	prompts := &[]string{}
	cfg := testConfig(t, remote)
	cfg.GitHub.Repo = "acme/orchard"
	return engine{config: cfg, source: source, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		agent: func(c Config) Agent { return fakeAgent{reply: reply, prompts: prompts, act: act, dir: c.Directory} },
		sleep: pause, observe: func(Progress) {}}, prompts
}

func TestJudgeIssueRunsInAnIssueWorktreeWithArrivalAndSource(t *testing.T) {
	remote, head := originRepo(t)
	source := fakeSource{issues: map[int]Issue{7: {Number: 7, Title: "Exports lose filters", Body: "Steps to reproduce.", State: "open", Comments: []string{"Confirmed on 2.1"}}}}
	e, prompts := testEngine(t, remote, `MAYOR_DECISION {"decision":"admit","reason":"A real defect with a bounded fix."}`, source, nil)
	arrival := json.RawMessage(`{"kind":"issue","number":7,"title":"Exports lose filters","simplifier_advice":{"decision":"admit","detail":"Focused."}}`)
	got, err := e.judge(context.Background(), JudgeRequest{Issue: 7, Arrival: arrival})
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != "admit" || !strings.Contains(got.Reason, "bounded fix") {
		t.Fatalf("unexpected judgment %+v", got)
	}
	prompt := (*prompts)[0]
	for _, want := range []string{"simplifier_advice", "Confirmed on 2.1", "Steps to reproduce", "MAYOR_DECISION", "admit or decline"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
	worktree := checkout{config: e.config}
	worktree.config.Directory = filepath.Join(e.config.Directory+"-items", "issue-7")
	if !worktree.pristine(context.Background(), head) {
		t.Fatal("the issue worktree was not left pristine at the branch head")
	}
}

func TestJudgePullRequestUsesTheExactHeadAndRejectsEdits(t *testing.T) {
	remote, base := originRepo(t)
	run := originGit(t, remote)
	run("commit", "--allow-empty", "-m", "pull")
	prHead := run("rev-parse", "HEAD")
	run("update-ref", "refs/pull/8/head", prHead)
	run("update-ref", "refs/heads/master", base)
	pull := Pull{Number: 8, Title: "Add CSV export", Body: "Closes #7", State: "open"}
	pull.Head.SHA, pull.Base.SHA = prHead, base
	source := fakeSource{pulls: map[int]Pull{8: pull}}
	arrival := json.RawMessage(`{"kind":"pr","number":8,"title":"Add CSV export","external":true}`)
	e, prompts := testEngine(t, remote, `MAYOR_DECISION {"decision":"decline","reason":"Duplicates the export already shipping."}`, source, nil)
	got, err := e.judge(context.Background(), JudgeRequest{PR: 8, HeadSHA: prHead, Arrival: arrival})
	if err != nil || got.Decision != "decline" {
		t.Fatalf("judgment %+v: %v", got, err)
	}
	if !strings.Contains((*prompts)[0], "Closes #7") {
		t.Fatal("prompt lacks the pull request body")
	}
	if _, err := e.judge(context.Background(), JudgeRequest{PR: 8, HeadSHA: base, Arrival: arrival}); err == nil || !strings.Contains(err.Error(), "head moved") {
		t.Fatalf("a moved head was judged: %v", err)
	}
	editing, _ := testEngine(t, remote, `MAYOR_DECISION {"decision":"admit","reason":"Edited it."}`, source, func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "README.md"), []byte("edited\n"), 0600)
	})
	if _, err := editing.judge(context.Background(), JudgeRequest{PR: 8, HeadSHA: prHead, Arrival: arrival}); err == nil || !strings.Contains(err.Error(), "modified tracked source") {
		t.Fatalf("an agent that edited the checkout was accepted: %v", err)
	}
}

func TestBulletinSummarizesMergedPullRequestsForUsers(t *testing.T) {
	remote, head := originRepo(t)
	mergedAt := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	first := Pull{Number: 12, Title: "Keep filters on export", Body: "Fixes #9", State: "closed", MergedAt: &mergedAt, MergeCommit: head}
	first.User.Login = "issue-bot"
	second := Pull{Number: 14, Title: "CSV export", Body: "Adds a CSV download.", State: "closed", MergedAt: &mergedAt}
	source := fakeSource{
		merged:  []Pull{first, second},
		issues:  map[int]Issue{9: {Number: 9, Title: "Export drops the active filter", Body: "Repro attached", State: "closed"}},
		changed: map[int][]ChangedFile{12: {{Path: "internal/export/filter.go", Status: "modified", Additions: 8, Deletions: 2}}},
	}
	reply := `MAYOR_BULLETIN {"title":"Exports you can trust","summary":"Exports keep your filters and can be downloaded as CSV.","items":[{"kind":"fix","title":"Exports keep the active filter","detail":"An export no longer drops the filter you were viewing.","pulls":[12],"issues":[9]},{"kind":"feature","title":"Download reports as CSV","detail":"Reports can be downloaded as CSV files.","pulls":[14]}]}`
	e, prompts := testEngine(t, remote, reply, source, nil)
	window := Window{Since: mergedAt.Add(-time.Hour), Until: mergedAt.Add(time.Hour)}
	state := newState(e.config)
	report, err := e.bulletin(context.Background(), state, window)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 2 || report.Items[0].Kind != "fix" || len(report.Pulls) != 2 || report.Pulls[1] != 14 || report.Since != window.Since {
		t.Fatalf("unexpected report %+v", report)
	}
	prompt := (*prompts)[0]
	for _, want := range []string{"Keep filters on export", "Export drops the active filter", "internal/export/filter.go", "issue-bot", "MAYOR_BULLETIN", "at most 40 items"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
	saved, err := ReadState(e.config)
	if err != nil || saved == nil || len(saved.Bulletins) != 1 || saved.Bulletins[0].Items != 2 || saved.Bulletins[0].Title != "Exports you can trust" {
		t.Fatalf("bulletin record was not saved: %+v (%v)", saved, err)
	}
	stray, _ := testEngine(t, remote, `MAYOR_BULLETIN {"title":"t","summary":"s","items":[{"kind":"fix","title":"x","detail":"y","pulls":[99]}]}`, source, nil)
	if _, err := stray.bulletin(context.Background(), newState(stray.config), window); err == nil || !strings.Contains(err.Error(), "outside the window") {
		t.Fatalf("a citation outside the window was accepted: %v", err)
	}
}

func TestBulletinWithNothingMergedNeedsNoAgent(t *testing.T) {
	remote, _ := originRepo(t)
	e, prompts := testEngine(t, remote, "", fakeSource{}, nil)
	report, err := e.bulletin(context.Background(), newState(e.config), Window{Since: time.Now().Add(-time.Hour), Until: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(*prompts) != 0 || len(report.Items) != 0 || report.Title == "" || len(report.Pulls) != 0 {
		t.Fatalf("an empty window started an agent or returned a bulletin: %+v", report)
	}
	if _, err := e.bulletin(context.Background(), newState(e.config), Window{}); err == nil {
		t.Fatal("an unbounded window was accepted")
	}
}

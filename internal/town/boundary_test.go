package town

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BrokkAi/brokk-town/internal/botapi"
	"github.com/BrokkAi/brokk-town/internal/harness"
)

// The service reaches a bot only through the bot contract. Only the bots'
// registry and the binary that hosts them may name a bot package; anything
// else would let the service depend on running in the bot's process.
func TestOnlyTheRegistryAndHostImportBots(t *testing.T) {
	module := filepath.Join("..", "..")
	allowed := map[string]bool{filepath.Join("bots", "inprocess.go"): true, filepath.Join("cmd", "bt", "bots.go"): true}
	scanned := map[string]bool{}
	err := filepath.WalkDir(module, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(module, path)
		if entry.IsDir() {
			if rel == "." {
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || (rel != "bots" && strings.HasPrefix(rel, "bots"+string(filepath.Separator))) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || allowed[rel] {
			return nil
		}
		scanned[filepath.ToSlash(rel)] = true
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			if p, _ := strconv.Unquote(spec.Path.Value); strings.HasPrefix(p, "github.com/BrokkAi/brokk-town/bots") {
				t.Errorf("%s imports %s; reach bots through internal/botapi", rel, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Guard against a walk that silently skips the module.
	for _, must := range []string{"internal/town/bots.go", "internal/web/server.go", "cmd/bt/main.go"} {
		if !scanned[must] {
			t.Fatalf("import check did not scan %s (%d files scanned)", must, len(scanned))
		}
	}
}

// recordingBot is a bot reached only through the contract.
type recordingBot struct {
	request botapi.Request
	events  []botapi.Event
	result  botapi.Result
}

func (r *recordingBot) Run(_ context.Context, request botapi.Request, emit func(botapi.Event)) (botapi.Result, error) {
	r.request = request
	for _, e := range r.events {
		emit(e)
	}
	return r.result, nil
}

func (r *recordingBot) Storage(context.Context, botapi.StorageRequest) (botapi.StorageResult, error) {
	return botapi.StorageResult{}, nil
}

func TestDispatchSendsASelectionAndKeepsOnlyTheBotsReport(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	update(t, store, func(st *State) {
		c := &st.Towns[x.ID].Config
		c.Harness = "codex-acp"
		c.HarnessDefinition = &harness.Entry{ID: "codex-acp", Name: "Fake Codex", Version: "0.0.1", Distribution: harness.Distribution{Npx: &harness.Package{Package: "fake-agent@0.0.1"}}}
		st.Towns[x.ID].Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, Title: "Complex request", Stage: "simplifying", House: Simplifier, Updated: time.Now()}
	})
	x = store.Snapshot().Towns[x.ID]
	digest := strings.Repeat("ab", 32)
	bot := &recordingBot{
		events: []botapi.Event{
			{Progress: &botapi.Progress{Phase: "", Task: strings.Repeat("x", 10000)}},
			{Log: &botapi.Log{Level: "bogus", Message: "noted", Attrs: map[string]string{"k": "v"}}},
		},
		result: botapi.Result{
			Simplification: &botapi.Simplification{Mode: "suggest", Decision: "decline", Summary: "Low value", Detail: "No callers."},
			Transcripts: []botapi.Transcript{
				{Path: "state/sessions/run.jsonl", Digest: digest, Bytes: 12},
				{Path: "../../escape.jsonl", Digest: digest, Bytes: 12},
				{Path: "state/state.json", Digest: digest, Bytes: 12},
				{Path: "state/sessions/bad-digest.jsonl", Digest: "nope", Bytes: 12},
			},
		},
	}
	workers := &BotWorkers{Root: t.TempDir(), Store: store, Bots: map[Role]botapi.Bot{Simplifier: bot}}
	var progress []Progress
	result, err := workers.Run(context.Background(), x, Simplifier, func(p Progress) { progress = append(progress, p) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Simplification == nil || result.Simplification.Decision != "decline" {
		t.Fatalf("result not carried: %+v", result)
	}
	sent := bot.request
	if sent.Town != Key(x.ID) || sent.Role != "simplifier" || sent.Issue != 7 || sent.Mode != "suggest" {
		t.Fatalf("request does not name the workspace and target: %+v", sent)
	}
	// The bot is told which harness to run, not a command resolved here.
	if len(sent.Agent.Command) != 0 || sent.Agent.Harness == nil || sent.Agent.Harness.Distribution.Npx.Package != "fake-agent@0.0.1" || sent.Agent.HarnessName != "codex-acp" {
		t.Fatalf("agent selection = %+v", sent.Agent)
	}
	last := progress[len(progress)-1]
	if last.Phase != "running" || len(last.Task) > 4096 || !utf8.ValidString(last.Task) {
		t.Fatalf("bot progress not bounded: %+v", last)
	}
	records := store.Snapshot().Towns[x.ID].Artifacts
	if len(records) != 1 {
		t.Fatalf("recorded %d transcripts, want only the valid one: %+v", len(records), records)
	}
	for _, r := range records {
		if r.Path != filepath.Join("simplifier", "state", "sessions", "run.jsonl") || r.Digest != digest || r.Bytes != 12 || r.Task != "issue:7" || !r.Complete {
			t.Fatalf("transcript receipt = %+v", r)
		}
	}
}

func TestDispatchWithoutABotFails(t *testing.T) {
	workers := &BotWorkers{}
	if _, err := workers.dispatchBot(context.Background(), Review, workerRequest{}, false, nil); err == nil || !strings.Contains(err.Error(), "review-bot") {
		t.Fatalf("dispatch without a bot: %v", err)
	}
}

// A bot's result is input. Text that would land in saved state is bounded,
// a malformed branch is never taken as ownership, and an oversized review is
// refused whole.
func TestWireResultsAreBounded(t *testing.T) {
	huge := strings.Repeat("界", 20000)
	got := fromWire(botapi.Result{
		Jobs: map[int]*botapi.IssueJob{
			7:  {Status: huge, Failure: huge, URL: "https://github.com/o/r/pull/" + huge, Branch: "bad branch\n", Tries: -3, Result: &botapi.IssueJobResult{Status: huge, Detail: huge}},
			-1: {Status: "pending"},
		},
		Issue: &botapi.IssueResult{Owned: []botapi.IssueOwnership{{PR: 3, Issue: 7, Branch: "bt/issue-7"}, {PR: 4, Issue: 8, Branch: "../escape"}, {PR: 5, Issue: 9}}},
	})
	job := got.Jobs[7]
	if len(got.Jobs) != 1 || job == nil {
		t.Fatalf("jobs = %+v", got.Jobs)
	}
	if len(job.Status) > wireStatusBytes || len(job.Failure) > wireTextBytes || job.URL != "" || job.Branch != "" || job.Tries != 0 || len(job.Result.Detail) > wireTextBytes || !utf8.ValidString(job.Failure) {
		t.Fatalf("job not bounded: status %d failure %d url %q branch %q tries %d", len(job.Status), len(job.Failure), job.URL, job.Branch, job.Tries)
	}
	owned := map[int]string{}
	for _, o := range got.Issue.Owned {
		owned[o.PR] = o.Branch
	}
	if len(owned) != 2 || owned[3] != "bt/issue-7" || owned[5] != "" {
		t.Fatalf("ownership = %+v", got.Issue.Owned)
	}

	findings := map[string]string{}
	for i := 0; i <= maxReviewFindings; i++ {
		findings[strconv.Itoa(i)] = "finding"
	}
	if err := validateWorkerResult(workerResult{Review: &workerReviewResult{Findings: findings}}, Review); err == nil {
		t.Fatal("accepted a review with too many findings")
	}
	if err := validateWorkerResult(workerResult{Review: &workerReviewResult{Findings: map[string]string{"a": strings.Repeat("x", wireTextBytes*4+1)}}}, Review); err == nil {
		t.Fatal("accepted an oversized finding")
	}
	if err := validateWorkerResult(workerResult{Review: &workerReviewResult{Findings: map[string]string{"a": "fine"}}}, Review); err != nil {
		t.Fatal(err)
	}
}

type unreachableStorage struct{}

func (unreachableStorage) BotStorage(context.Context, Role, botapi.StorageRequest) (botapi.StorageResult, error) {
	return botapi.StorageResult{}, errors.New("connection refused")
}

// A removal the bot never received removed nothing, so it is retained, not
// left uncertain.
func TestUnreachableBotRetainsSelectedTranscripts(t *testing.T) {
	s := &Supervisor{}
	selected := map[string]StorageArtifact{"a": {Role: Issue, botPath: "state/sessions/a.jsonl", digest: strings.Repeat("0", 64), Bytes: 1}}
	answers := s.removeBotTranscripts(context.Background(), &Town{ID: "o/r"}, []string{"a"}, selected, unreachableStorage{})
	if a := answers["a"]; a.Status != "retained" || !strings.Contains(a.Detail, "could not be reached") {
		t.Fatalf("answer = %+v", a)
	}
}

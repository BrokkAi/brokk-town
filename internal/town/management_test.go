package town

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

func ptr[T any](v T) *T { return &v }

func TestSettingsApplyAtDispatchAndDoNotChangeActiveRun(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) { st.Towns[x.ID].Config.Agent.Model = "old"; st.Towns[x.ID].Workers[Bug].Enabled = true })
	stale := s.Snapshot().Towns[x.ID]
	seen := make(chan string, 2)
	release := make(chan struct{})
	workers := workerFunc(func(_ context.Context, town *Town, _ Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		seen <- town.Config.Agent.Model
		<-release
		seen <- town.Config.Agent.Model
		return RunResult{}, nil
	})
	sup := NewSupervisor(s, nil, workers)
	done := make(chan struct{})
	go func() { sup.execute(context.Background(), stale, Bug); close(done) }()
	if <-seen != "old" {
		t.Fatal("wrong starting model")
	}
	if err := sup.Settings(x.ID, AgentSettings{Model: ptr("new")}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if <-seen != "old" {
		t.Fatal("active config mutated")
	}
	<-done
	sup.execute(context.Background(), stale, Bug)
	if <-seen != "new" || <-seen != "new" {
		t.Fatal("stale scheduled config used")
	}
}

func TestAgentSettingsPreservePrivateConfigAndSwitchHarness(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		st.Towns[x.ID].Config.Agent = runner.AgentConfig{Command: []string{"agent", "private-argument"}, Environment: map[string]string{"KEY": "private-value"}, Model: "original", Effort: "low", AuthMethod: "login", Mode: "mode"}
	})
	sup := NewSupervisor(s, nil, nil)
	if err := sup.Settings(x.ID, AgentSettings{Model: ptr("next"), Effort: ptr("high")}); err != nil {
		t.Fatal(err)
	}
	c := s.Snapshot().Towns[x.ID].Config
	if c.Agent.Command[1] != "private-argument" || c.Agent.Environment["KEY"] != "private-value" || c.Agent.Model != "next" || c.Agent.Effort != "high" {
		t.Fatal("settings lost existing config", c)
	}
	public, _ := json.Marshal(s.Snapshot().Public())
	if strings.Contains(string(public), "private-") || !strings.Contains(string(public), `"harness":"custom"`) || !strings.Contains(string(public), `"model":"next"`) {
		t.Fatal("unsafe or incomplete public settings", string(public))
	}
	if err := sup.Settings(x.ID, AgentSettings{Harness: ptr("claude")}); err != nil {
		t.Fatal(err)
	}
	c = s.Snapshot().Towns[x.ID].Config
	if c.harness() != "claude" || !reflect.DeepEqual(c.Agent, runner.AgentConfig{}) {
		t.Fatal("authentication carried across harnesses", c)
	}
	for _, input := range []AgentSettings{{Harness: ptr("unknown")}, {Harness: ptr("custom")}, {Command: ptr([]string{"foo"})}} {
		if err := sup.Settings(x.ID, input); err == nil {
			t.Fatal("invalid setting accepted", input)
		}
	}
	if s.Snapshot().Towns[x.ID].Config.harness() != "claude" {
		t.Fatal("invalid update committed")
	}
	if err := sup.Settings(x.ID, AgentSettings{Harness: ptr("custom"), Command: ptr([]string{"agent", "--acp"}), Model: ptr("m"), Effort: ptr("e")}); err != nil {
		t.Fatal(err)
	}
	resolved := agentConfig(s.Snapshot().Towns[x.ID].Config)
	if resolved.Model != "m" || resolved.Effort != "e" || !reflect.DeepEqual(resolved.Command, []string{"agent", "--acp"}) {
		t.Fatal(resolved)
	}
}

func TestDeleteCancelsWorkRetainsRecoveryAndRestoresPaused(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		town.Workers[Repo].Enabled = false
		town.Workers[Issue].Enabled = true
		town.Owned[9] = Ownership{Branch: "issue/1", Issue: 1}
		_, _ = st.Add(DefaultConfig("acme/other"))
		st.Towns["acme/other"].Workers[Repo].Enabled = false
	})
	entered, stopping, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	workers := workerFunc(func(ctx context.Context, _ *Town, _ Role, progress func(Progress), _ *slog.Logger) (RunResult, error) {
		close(entered)
		<-ctx.Done()
		close(stopping)
		<-release
		progress(Progress{"cleanup", "Stopped"})
		return RunResult{Owned: map[int]Ownership{10: {Branch: "issue/2", Issue: 2}}}, ctx.Err()
	})
	sup := NewSupervisor(s, nil, workers)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	if err := sup.Delete(x.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopping:
	case <-time.After(time.Second):
		t.Fatal("worker was not canceled")
	}
	public := s.Snapshot().Public()
	if len(public["towns"].(map[string]any)) != 1 {
		t.Fatal("deleted town is visible")
	}
	if err := sup.Control(x.ID, Issue, "start", ""); err == nil {
		t.Fatal("deleted town restarted")
	}
	if _, err := sup.Add(DefaultConfig(x.ID)); err == nil {
		t.Fatal("restored while worker was still stopping")
	}
	close(release)
	eventually(t, func() bool { sup.mu.Lock(); defer sup.mu.Unlock(); return len(sup.running) == 0 })
	if len(s.Snapshot().Towns[x.ID].Owned) != 2 {
		t.Fatal("lost durable work result")
	}
	if _, err := sup.Add(DefaultConfig(x.ID)); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Role{Bug, Issue, Review, Release} {
		if s.Snapshot().Towns[x.ID].Workers[r].Enabled {
			t.Fatal("restoring resumed automation")
		}
	}
	// Avoid dispatching the reporter with a nil fake before shutting down.
	if err := sup.Control(x.ID, Repo, "stop", ""); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
}

type requestPublisher struct {
	post func(context.Context, string, string, string) (RemoteIssue, error)
	find func(context.Context, string, string) (*RemoteIssue, error)
}

func (p requestPublisher) CreateIssue(c context.Context, r, t, b string) (RemoteIssue, error) {
	return p.post(c, r, t, b)
}
func (p requestPublisher) FindRequest(c context.Context, r, id string) (*RemoteIssue, error) {
	return p.find(c, r, id)
}
func sampleRequest() IssueRequest {
	return IssueRequest{ID: "12345678-abcd-4321-1234-123456789abc", Kind: "feature", Title: "Add keyboard navigation", Body: "Support arrow keys in the town switcher."}
}

func TestIssueLostResponseSurvivesRestartWithoutDuplicatePost(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	x := addTown(t, s)
	sup := NewSupervisor(s, nil, nil)
	posts := 0
	var remote RemoteIssue
	p := requestPublisher{post: func(_ context.Context, repo, title, body string) (RemoteIssue, error) {
		posts++
		if s.Snapshot().Towns[x.ID].Requests[sampleRequest().ID].Status != "uncertain" {
			t.Fatal("POST before durable intent")
		}
		remote = RemoteIssue{Number: 51, Title: title, Body: body, URL: "https://github.com/" + repo + "/issues/51", State: "open"}
		return RemoteIssue{}, errors.New("response lost after creation")
	}, find: func(context.Context, string, string) (*RemoteIssue, error) { return &remote, nil }}
	sup.Publisher = p
	for range 2 {
		if _, err = sup.SubmitRequest(x.ID, sampleRequest()); err != nil {
			t.Fatal(err)
		}
	}
	sup.publishRequest(context.Background(), x.ID, sampleRequest().ID)
	if posts != 1 || s.Snapshot().Towns[x.ID].Requests[sampleRequest().ID].Status != "uncertain" {
		t.Fatal("lost response was not preserved")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sup = NewSupervisor(s, nil, nil)
	sup.Publisher = p
	sup.publishRequest(context.Background(), x.ID, sampleRequest().ID)
	sup.publishRequest(context.Background(), x.ID, sampleRequest().ID)
	st := s.Snapshot()
	town := st.Towns[x.ID]
	if posts != 1 || town.Requests[sampleRequest().ID].Status != "confirmed" || town.Tasks["issue:51"] == nil {
		t.Fatal("receipt did not reconcile", town.Requests)
	}
	if town.Workers[Issue].Enabled {
		t.Fatal("filing an issue enabled implementation")
	}
	deliveries := 0
	for _, e := range st.Events {
		if e.Cargo == "issue:51" {
			deliveries++
		}
	}
	if deliveries != 1 {
		t.Fatal("duplicate delivery", deliveries)
	}
	changed := sampleRequest()
	changed.Body = "Different request"
	if _, err = sup.SubmitRequest(x.ID, changed); err == nil {
		t.Fatal("idempotency key reused with different content")
	}
}

func TestMissingReceiptNeverRepostsAndDeleteCancelsQueuedSubmission(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	sup := NewSupervisor(s, nil, nil)
	posts := 0
	sup.Publisher = requestPublisher{post: func(context.Context, string, string, string) (RemoteIssue, error) {
		posts++
		return RemoteIssue{}, errors.New("offline")
	}, find: func(context.Context, string, string) (*RemoteIssue, error) { return nil, nil }}
	if _, err := sup.SubmitRequest(x.ID, sampleRequest()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		sup.publishRequest(context.Background(), x.ID, sampleRequest().ID)
	}
	if posts != 1 {
		t.Fatal("uncertain write replayed", posts)
	}
	r := sampleRequest()
	r.ID = strings.Repeat("a", 32)
	if _, err := sup.SubmitRequest(x.ID, r); err != nil {
		t.Fatal(err)
	}
	if err := sup.Delete(x.ID); err != nil {
		t.Fatal(err)
	}
	sup.publishRequest(context.Background(), x.ID, r.ID)
	if posts != 1 || s.Snapshot().Towns[x.ID].Requests[r.ID].Status != "canceled" {
		t.Fatal("deleted town submitted queued issue")
	}
}

func TestDemoRequestsAndChoicesCannotRunRealClients(t *testing.T) {
	s := testStore(t, true)
	x := addTown(t, s)
	sup := NewSupervisor(s, nil, nil)
	sup.Publisher = requestPublisher{post: func(context.Context, string, string, string) (RemoteIssue, error) {
		t.Fatal("demo POST")
		return RemoteIssue{}, nil
	}}
	r := sampleRequest()
	r.Kind = "bug"
	result, err := sup.SubmitRequest(x.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	sup.schedule(context.Background())
	sup.publishRequest(context.Background(), x.ID, r.ID)
	if result.Status != "confirmed" || result.URL != "" || s.Snapshot().Towns[x.ID].Tasks[fmt.Sprintf("issue:%d", result.Number)] == nil {
		t.Fatal(result)
	}
	c, err := sup.Choices(context.Background(), x.ID, AgentSettings{})
	if err != nil || len(c.Models) == 0 {
		t.Fatal("demo choices", c, err)
	}
}

func TestChoicesUseModelSpecificEffortWithoutPrompt(t *testing.T) {
	c := DefaultConfig("acme/orchard")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c.Agent = runner.AgentConfig{Command: []string{exe, "-test.run=TestChoiceAgentHelper"}, Environment: map[string]string{"BROKK_CHOICE_HELPER": "1"}, Model: "careful"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := ProbeAgent(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 2 || len(result.Efforts) != 1 || result.Efforts[0].Value != "high" {
		t.Fatal(result)
	}
}

func TestChoiceAgentHelper(t *testing.T) {
	if os.Getenv("BROKK_CHOICE_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	model := "quick"
	options := func() []any {
		effort := "low"
		if model == "careful" {
			effort = "high"
		}
		return []any{map[string]any{"id": "model", "type": "select", "currentValue": model, "options": []any{map[string]string{"value": "quick", "name": "Quick"}, map[string]string{"value": "careful", "name": "Careful"}}}, map[string]any{"id": "reasoning_effort", "type": "select", "currentValue": effort, "options": []any{map[string]string{"value": effort, "name": effort}}}}
	}
	for scanner.Scan() {
		var request struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(4)
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1}
		case "session/new":
			dir, _ := request.Params["cwd"].(string)
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				os.Exit(5)
			}
			result = map[string]any{"sessionId": "choices", "configOptions": options()}
		case "session/set_config_option":
			model, _ = request.Params["value"].(string)
			result = map[string]any{"configOptions": options()}
		default:
			os.Exit(6) // Any prompt or unexpected tool work fails discovery.
		}
		if enc.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
			os.Exit(7)
		}
	}
	os.Exit(0)
}

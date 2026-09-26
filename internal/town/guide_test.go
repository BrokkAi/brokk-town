package town

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

func guideFixture(t *testing.T) (*Supervisor, string) {
	t.Helper()
	store := testStore(t, false)
	x := addTown(t, store)
	update(t, store, func(st *State) {
		x := st.Towns[x.ID]
		x.Config.Harness = "custom"
		x.Config.Agent = runner.AgentConfig{Command: []string{"fixture-guide", "--private=supersecret123"}, Environment: map[string]string{"TOKEN": "supersecret123"}, Model: "test-model", Effort: "high"}
		for _, w := range x.Workers {
			w.Enabled = false
		}
	})
	return NewSupervisor(store, newGH(1), nil), x.ID
}
func askGuide(t *testing.T, s *Supervisor, id, request, question string) GuideTurn {
	t.Helper()
	g := s.Store.Snapshot().Towns[id].Guide
	var seq uint64
	if g != nil {
		seq = g.Next
	}
	turn, err := s.AskGuide(id, GuideQuestion{ID: request, Sequence: seq, Question: question})
	if err != nil {
		t.Fatal(err)
	}
	return turn
}
func TestGuideStreamsReadOnlyAndRequiresExactConfirmation(t *testing.T) {
	s, id := guideFixture(t)
	update(t, s.Store, func(st *State) { st.Towns[id].Workers[Issue].Enabled = true })
	var calls atomic.Int32
	first := make(chan struct{})
	finish := make(chan struct{})
	s.GuideRunner = func(ctx context.Context, a runner.AgentConfig, dir, prompt string, emit func(string) error) error {
		calls.Add(1)
		if a.Model != "test-model" || a.Effort != "high" || a.Mode != "" {
			t.Error(a.Model, a.Effort, a.Mode)
		}
		if strings.Contains(prompt, "supersecret123") || strings.Contains(prompt, "fixture-guide") {
			t.Error("private configuration leaked")
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Error("guide received a repository", entries, err)
		}
		if err := emit("Issue Bot is enabled. No action has been taken. "); err != nil {
			return err
		}
		close(first)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finish:
		}
		return emit("\nTOWN_GUIDE_PROPOSAL {\"action\":\"pause\",\"role\":\"issue\"}")
	}
	turn := askGuide(t, s, id, "request-one", "Pause issue after explaining its status")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.runGuide(ctx) }()
	<-first
	eventually(t, func() bool { return s.Store.Snapshot().Towns[id].Guide.turn(turn.ID).Status == "answering" })
	before := s.Store.Snapshot().Towns[id]
	if !before.Workers[Issue].Enabled || before.Guide.turn(turn.ID).Answer == "" {
		t.Fatal("question mutated workers or did not stream")
	}
	if _, err := s.AskGuide(id, GuideQuestion{ID: turn.ID, Sequence: turn.Sequence, Question: turn.Question}); err != nil {
		t.Fatal(err)
	}
	close(finish)
	eventually(t, func() bool { return s.Store.Snapshot().Towns[id].Guide.turn(turn.ID).Status == "complete" })
	saved := s.Store.Snapshot().Towns[id]
	proposal := saved.Guide.turn(turn.ID).Proposal
	if calls.Load() != 1 || proposal == nil || !saved.Workers[Issue].Enabled || strings.Contains(saved.Guide.turn(turn.ID).Answer, "TOWN_GUIDE_PROPOSAL") {
		t.Fatal(calls.Load(), proposal, saved.Guide)
	}
	if err := s.ConfirmGuide(id, turn.ID, "wrong"); err == nil {
		t.Fatal("confirmed changed action")
	}
	if err := s.ConfirmGuide(id, turn.ID, proposal.Digest); err != nil {
		t.Fatal(err)
	}
	after := s.Store.Snapshot()
	events := len(after.Events)
	if after.Towns[id].Workers[Issue].Enabled || after.Towns[id].Guide.turn(turn.ID).Proposal.Status != "confirmed" {
		t.Fatal("confirmed pause not applied")
	}
	if err := s.ConfirmGuide(id, turn.ID, proposal.Digest); err != nil {
		t.Fatal(err)
	}
	if len(s.Store.Snapshot().Events) != events {
		t.Fatal("confirmation duplicated control")
	}
	cancel()
	<-done
}
func TestGuideStaleProposalCannotPauseChangedWorker(t *testing.T) {
	s, id := guideFixture(t)
	s.GuideRunner = func(_ context.Context, _ runner.AgentConfig, _, _ string, emit func(string) error) error {
		return emit("Pause?\nTOWN_GUIDE_PROPOSAL {\"action\":\"pause\",\"role\":\"issue\"}")
	}
	turn := askGuide(t, s, id, "request-stale", "Pause issue")
	s.answerGuide(context.Background(), id, turn.ID)
	p := s.Store.Snapshot().Towns[id].Guide.turn(turn.ID).Proposal
	if p == nil {
		t.Fatal("no proposal")
	}
	update(t, s.Store, func(st *State) { st.Towns[id].Workers[Issue].Enabled = true })
	if err := s.ConfirmGuide(id, turn.ID, p.Digest); err == nil {
		t.Fatal("stale proposal applied")
	}
	if !s.Store.Snapshot().Towns[id].Workers[Issue].Enabled {
		t.Fatal("stale pause mutated worker")
	}
}
func TestGuideCancellationBoundsAndUnsupportedActions(t *testing.T) {
	for _, scenario := range []string{"cancel", "overflow", "unsupported"} {
		t.Run(scenario, func(t *testing.T) {
			s, id := guideFixture(t)
			entered := make(chan struct{})
			stopped := make(chan struct{})
			s.GuideRunner = func(ctx context.Context, _ runner.AgentConfig, _, _ string, emit func(string) error) error {
				defer close(stopped)
				close(entered)
				switch scenario {
				case "cancel":
					<-ctx.Done()
					return ctx.Err()
				case "overflow":
					return emit(strings.Repeat("x", guideAnswerBytes+1))
				default:
					return emit("Do it.\nTOWN_GUIDE_PROPOSAL {\"action\":\"start\",\"role\":\"release\"}")
				}
			}
			turn := askGuide(t, s, id, "request-limits", "What now?")
			done := make(chan struct{})
			go func() { defer close(done); s.answerGuide(context.Background(), id, turn.ID) }()
			<-entered
			if scenario == "cancel" {
				if err := s.CancelGuide(id, turn.ID); err != nil {
					t.Fatal(err)
				}
			}
			<-stopped
			<-done
			saved := s.Store.Snapshot().Towns[id].Guide.turn(turn.ID)
			if saved.Proposal != nil || len(saved.Answer) > guideAnswerBytes || guideBusy(saved.Status) {
				t.Fatal(saved)
			}
			if scenario == "overflow" && saved.Status != "failed" {
				t.Fatal(saved.Status)
			}
			if scenario == "cancel" && saved.Status != "cancelled" {
				t.Fatal(saved.Status)
			}
		})
	}
}
func TestGuideConversationReconnectRestartAndBoundedIDs(t *testing.T) {
	s, id := guideFixture(t)
	s.GuideRunner = func(_ context.Context, _ runner.AgentConfig, _, _ string, emit func(string) error) error {
		return emit("Saved answer")
	}
	original := askGuide(t, s, id, "request-original", "First question")
	s.answerGuide(context.Background(), id, original.ID)
	for i := 0; i < guideTurns; i++ {
		turn := askGuide(t, s, id, strings.Repeat("a", 8)+string(rune('A'+i)), "Next")
		s.answerGuide(context.Background(), id, turn.ID)
	}
	if _, err := s.AskGuide(id, GuideQuestion{ID: original.ID, Sequence: original.Sequence, Question: original.Question}); err == nil {
		t.Fatal("expired submission replayed")
	}
	queued := askGuide(t, s, id, "request-interrupted", "Interrupt")
	update(t, s.Store, func(st *State) { st.Towns[id].Guide.turn(queued.ID).Status = "answering" })
	root := filepath.Dir(s.Store.path)
	s.Store.Close()
	again, err := Open(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	g := again.Snapshot().Towns[id].Guide
	if len(g.Turns) != guideTurns || g.turn(queued.ID).Status != "interrupted" || g.turn(original.ID) != nil {
		t.Fatal(g)
	}
	resumed := NewSupervisor(again, newGH(1), nil)
	if _, err := resumed.AskGuide(id, GuideQuestion{ID: queued.ID, Sequence: queued.Sequence, Question: queued.Question}); err != nil {
		t.Fatal(err)
	}
	if g := again.Snapshot().Towns[id].Guide; g.turn(queued.ID).Status != "interrupted" {
		t.Fatal("restart replayed interrupted prompt")
	}
}
func TestGuideRedactsContextAndSplitOutputAndDemoNeverInvokesAgent(t *testing.T) {
	s, id := guideFixture(t)
	update(t, s.Store, func(st *State) {
		x := st.Towns[id]
		x.Workers[Review].Error = "fixture-guide --private=supersecret123"
		x.Workers[Review].Logs = []Log{{Level: "error", Text: "token=supersecret123"}}
	})
	t.Setenv("GUIDE_TEST_TOKEN", "environment-secret-456")
	contextText, _ := guideContext(s.Store.Snapshot().Towns[id])
	if strings.Contains(contextText, "supersecret123") || strings.Contains(contextText, "fixture-guide") {
		t.Fatal("private context", contextText)
	}
	redact := guideRedactor(s.Store.Snapshot().Towns[id].Config)
	if text := redact("environment-secret-456 /home/fixture/private/worktree"); strings.Contains(text, "environment-secret-456") || strings.Contains(text, "/home/fixture") {
		t.Fatal("private environment or path leaked", text)
	}
	if text := redact("prefix supersecret"); strings.Contains(text, "supersecret") {
		t.Fatal("split secret leaked", text)
	}
	s.GuideRunner = func(_ context.Context, _ runner.AgentConfig, _, _ string, emit func(string) error) error {
		if err := emit("supersecret123"); err != nil {
			return err
		}
		return errors.New("private failure with supersecret123")
	}
	turn := askGuide(t, s, id, "request-secret", "What is supersecret123?")
	s.answerGuide(context.Background(), id, turn.ID)
	raw, _ := json.Marshal(s.Store.Snapshot().Public())
	if strings.Contains(string(raw), `"answer":"supersecret123`) {
		t.Fatal("output leak")
	}
	saved := s.Store.Snapshot().Towns[id].Guide.turn(turn.ID)
	if strings.Contains(saved.Question+saved.Answer+saved.Detail, "supersecret123") {
		t.Fatal("guide leaked private value")
	}
	demo := testStore(t, true)
	x := addTown(t, demo)
	sup := NewSupervisor(demo, newGH(1), nil)
	sup.GuideRunner = func(context.Context, runner.AgentConfig, string, string, func(string) error) error {
		t.Error("demo invoked a real guide")
		return nil
	}
	q := askGuide(t, sup, x.ID, "demo-question", "Explain queued work")
	sup.answerGuide(context.Background(), x.ID, q.ID)
	answer := demo.Snapshot().Towns[x.ID].Guide.turn(q.ID)
	if answer.Status != "complete" || !strings.Contains(answer.Answer, "Demo Town Guide") {
		t.Fatal(answer)
	}
}

func TestDemoServiceLoopProcessesGuideWithoutExecutionDependencies(t *testing.T) {
	store := testStore(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, store, time.Hour) }()
	eventually(t, func() bool { return len(store.Snapshot().Towns) > 0 })
	sup := NewSupervisor(store, nil, nil)
	id := "brokkai/orchard"
	turn := askGuide(t, sup, id, "demo-loop-question", "What needs attention?")
	eventually(t, func() bool { return store.Snapshot().Towns[id].Guide.turn(turn.ID).Status == "complete" })
	cancel()
	<-done
}

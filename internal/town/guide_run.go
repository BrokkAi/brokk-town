package town

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/guide"
	"github.com/BrokkAi/brokk-town/internal/harness"
)

type GuideRunner func(context.Context, runner.AgentConfig, string, string, func(string) error) error

// runGuide owns one bounded conversation at a time, separate from execution
// workers. Requests only commit to the queue; no client input/render loop runs it.
func (s *Supervisor) runGuide(ctx context.Context) {
	for ctx.Err() == nil {
		changed := s.Store.Watch()
		state := s.Store.Snapshot()
		ids := []string{}
		for id := range state.Towns {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var chosen, turnID string
		for _, id := range ids {
			t := state.Towns[id]
			if t.Deleted || t.Guide == nil {
				continue
			}
			for _, turn := range t.Guide.Turns {
				if turn.Status == "queued" {
					chosen, turnID = id, turn.ID
					break
				}
			}
			if chosen != "" {
				break
			}
		}
		if chosen != "" {
			s.answerGuide(ctx, chosen, turnID)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}
func (s *Supervisor) answerGuide(parent context.Context, id, turnID string) {
	var saved *Town
	if err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return nil
		}
		turn := t.Guide.turn(turnID)
		if turn == nil || turn.Status != "queued" {
			return nil
		}
		t.Guide.Revision++
		turn.Status = "gathering"
		turn.Updated = s.now()
		cfg := t.PublicConfig()
		turn.Profile = PublicBotAgentConfig{Harness: cfg.Harness, HarnessVersion: cfg.HarnessVersion, Model: cfg.Model, Effort: cfg.Effort, Inherited: true}
		_, turn.Houses = guideContext(t)
		saved = clone(t)
		return nil
	}); err != nil {
		s.fail(err)
		return
	}
	if saved == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	redact := guideRedactor(saved.Config)
	var mu sync.Mutex
	var text strings.Builder
	emit := func(chunk string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		if text.Len()+len(chunk) > guideAnswerBytes {
			return errors.New("guide answer exceeded its limit")
		}
		text.WriteString(chunk)
		return nil
	}
	readAnswer := func() string { mu.Lock(); defer mu.Unlock(); return guideClip(redact(text.String()), guideAnswerBytes) }
	read := func() string { return guideVisibleText(readAnswer()) }
	done := make(chan error, 1)
	go func() {
		state := s.Store.Snapshot()
		if state.Demo {
			done <- demoGuide(ctx, saved, turnID, emit)
			return
		}
		run := s.GuideRunner
		if run == nil {
			run = guide.Run
		}
		config, err := s.guideAgent(ctx, saved.Config)
		if err != nil {
			done <- err
			return
		}
		root := filepath.Dir(s.Store.path)
		directory, err := guide.Workspace(root)
		if err != nil {
			done <- err
			return
		}
		defer os.RemoveAll(directory)
		contextText, _ := guideContext(saved)
		done <- run(ctx, config, directory, guidePrompt(saved, saved.Guide.turn(turnID), contextText), emit)
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		changed := s.Store.Watch()
		current := s.Store.Snapshot().Towns[id]
		if current == nil || current.Deleted || current.Guide.turn(turnID) == nil || !guideBusy(current.Guide.turn(turnID).Status) {
			cancel()
		}
		select {
		case err := <-done:
			answer := readAnswer()
			s.update(func(st *State) error {
				t := st.Towns[id]
				if t == nil {
					return nil
				}
				turn := t.Guide.turn(turnID)
				if turn == nil || !guideBusy(turn.Status) {
					return nil
				}
				t.Guide.Revision++
				turn.Answer = answer
				turn.Updated = s.now()
				turn.Status = "complete"
				if err != nil || strings.TrimSpace(answer) == "" {
					turn.Status = "failed"
					turn.Detail = "Guide could not complete this answer. Check the configured harness login, read-only mode, model and effort; no proposal was dispatched."
					if ctx.Err() != nil {
						turn.Status = "cancelled"
						turn.Detail = "Guide stopped or reached its two-minute deadline. No proposal was dispatched."
					}
					return nil
				}
				answer, proposal, parseErr := guideProposal(answer, saved)
				t.Guide.Revision++
				turn.Answer = answer
				turn.Proposal = proposal
				if parseErr != nil {
					turn.Detail = "The answer contained an unsupported proposal. Use the existing inspector controls."
				}
				return nil
			})
			return
		case <-ctx.Done():
			cancel()
			// Every runner must honor cancellation. The ACP runner kills its complete
			// process group; wait for it before admitting another conversation.
			err := <-done
			_ = err
			s.update(func(st *State) error {
				t := st.Towns[id]
				if t == nil {
					return nil
				}
				turn := t.Guide.turn(turnID)
				if turn != nil && guideBusy(turn.Status) {
					t.Guide.Revision++
					turn.Answer = read()
					turn.Status = "cancelled"
					turn.Detail = "Guide stopped or reached its two-minute deadline. No proposal was dispatched."
					turn.Updated = s.now()
				}
				return nil
			})
			return
		case <-changed:
		case <-ticker.C:
			answer := read()
			if answer == last {
				continue
			}
			last = answer
			s.update(func(st *State) error {
				t := st.Towns[id]
				if t == nil || t.Deleted {
					return nil
				}
				turn := t.Guide.turn(turnID)
				if turn != nil && guideBusy(turn.Status) {
					t.Guide.Revision++
					turn.Answer = answer
					turn.Status = "answering"
					turn.Updated = s.now()
				}
				return nil
			})
		}
	}
}
func (s *Supervisor) guideAgent(ctx context.Context, c Config) (runner.AgentConfig, error) {
	if err := requireLocalExecution(c); err != nil {
		return runner.AgentConfig{}, err
	}
	a := clone(c.Agent)
	// Resolve the same saved harness/model/effort as town defaults. Do not apply
	// execution-worker permission overrides (including Muse auto-approval config).
	if len(a.Command) == 0 {
		var entry harness.Entry
		if c.HarnessDefinition != nil {
			entry = *c.HarnessDefinition
		} else {
			var err error
			entry, err = s.Harnesses.Lookup(c.harness(), "")
			if err != nil {
				return a, err
			}
		}
		command, env, err := harness.Launch(ctx, filepath.Dir(s.Store.path), entry)
		if err != nil {
			return a, err
		}
		a.Command = command
		if env == nil {
			env = map[string]string{}
		}
		for key, value := range a.Environment {
			env[key] = value
		}
		a.Environment = env
	}
	a.Mode = ""
	return a, nil
}
func demoGuide(ctx context.Context, t *Town, turnID string, emit func(string) error) error {
	blocked, queued := 0, 0
	for _, task := range t.Tasks {
		if task.Blocked {
			blocked++
		}
		if task.Stage == "queued" || task.Stage == "simplifying" {
			queued++
		}
	}
	answer := fmt.Sprintf("Demo Town Guide: %s has %d queued or intake tasks and %d blocked tasks. ", t.ID, queued, blocked)
	answer += "The worker inspector shows each house's status and recent failures. Town settings show the model, effort, work filters and quiet hours. These are simulated observations. Uncertain writes remain unresolved, and zero new review comments does not prove a clean review."
	question := strings.ToLower(t.Guide.turn(turnID).Question)
	for _, role := range Roles {
		if strings.Contains(question, "pause") && strings.Contains(question, string(role)) {
			answer += "\nI can propose pausing this worker after its current work finishes. Confirm the exact action below to apply it.\nTOWN_GUIDE_PROPOSAL {\"action\":\"pause\",\"role\":\"" + string(role) + "\"}"
			break
		}
	}
	for _, chunk := range strings.SplitAfter(answer, " ") {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(15 * time.Millisecond):
		}
		if err := emit(chunk); err != nil {
			return err
		}
	}
	return nil
}

package mjolnir

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/schema"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// Executor speaks ACP to mj, never to a local coding harness. Command is an
// operator-supplied mj executable/prefix (for example mj --instance work).
type Executor struct {
	Runs    *Runs
	Command []string
}

type Answer struct {
	Text      string    `json:"-"`
	Artifacts Artifacts `json:"-"`
	Run       *Run      `json:"-"`
}

func (e Executor) Execute(ctx context.Context, plan RunPlan, prompt string, repair bool) (answer Answer, err error) {
	if !utf8.ValidString(prompt) || strings.TrimSpace(prompt) == "" || utf8.RuneCountInString(prompt) > 65536 || strings.HasPrefix(prompt, "!") {
		return answer, errors.New("Mjolnir prompts must contain 1–65536 characters and cannot start with !; no session was created")
	}
	if e.Runs == nil || e.Runs.Catalog == nil || e.Runs.Catalog.listing.Demo || len(e.Command) == 0 {
		return answer, errors.New("managed execution requires configured Mjolnir and a bounded prompt outside demo mode")
	}
	if err := plan.Validate(); err != nil {
		return answer, err
	}
	// Verify the adapter and HTTP artifacts address the same daemon before it
	// can create a session. API-info contains a token path, never the token.
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	info, probeErr := osrun.Run(probe, "", nil, append(append([]string{}, e.Command...), "api-info", "--json")...)
	cancel()
	var connection struct {
		URL   string `json:"base_url"`
		Token string `json:"token_path"`
	}
	wanted := e.Runs.Catalog.connection
	if probeErr != nil || json.Unmarshal([]byte(info), &connection) != nil || strings.TrimRight(connection.URL, "/") != strings.TrimRight(wanted.URL, "/") || filepath.Clean(connection.Token) != filepath.Clean(wanted.TokenFile) {
		return answer, errors.New("mj acp and Town's artifact API do not identify the same daemon; check the Mjolnir command and connection settings")
	}
	args := append(append([]string{}, e.Command...), "acp", "--workspace", plan.Placement.Workspace.Name, "--profile", plan.Selection.Profile, "--target", plan.Selection.Target, "--bundle", plan.Placement.Bundle, "--checkout-repository", plan.Checkout.Repository, "--checkout-commit", plan.Checkout.Commit, "--checkout-branch", plan.Checkout.Branch, "--expected-runtime-identity", plan.Runtime.Runtime.ID, "--on-exit", "keep")
	process, kill := context.WithCancel(context.Background())
	defer kill()
	cmd := osrun.StartCommand(process, "", args, nil)
	cmd.Stderr = io.Discard // Never persist private adapter diagnostics.
	in, err := cmd.StdinPipe()
	if err != nil {
		return answer, err
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return answer, err
	}
	defer out.Close()
	if err := cmd.Start(); err != nil {
		return answer, errors.New("could not start mj acp; check the configured executable")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var mu sync.Mutex
	var session acp.Session
	var text strings.Builder
	prompting := false
	conn := acp.Connect(out, in, func(context.Context, string, json.RawMessage) (any, error) {
		return nil, &acp.RPCError{Code: -32601, Message: "Mjolnir owns target tools; Town exposes none"}
	}, acp.SessionUpdates(func(update acp.Update) error {
		chunk := update.Update.AgentMessageChunk
		if chunk == nil || chunk.Content.Text == nil {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		if !prompting || update.SessionID != session.SessionID || text.Len()+len(chunk.Content.Text.Text) > 1<<20 {
			return errors.New("Mjolnir returned an unexpected or oversized ACP answer")
		}
		text.WriteString(chunk.Content.Text.Text)
		return nil
	}))
	defer func() {
		if ctx.Err() != nil && session.SessionID != "" {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.CancelSession(cancelCtx, session.SessionID)
			cancel()
		}
		_ = conn.Close() // EOF asks the adapter to interrupt any owned active turn.
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			kill()
			<-done
		}
	}()
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, initErr := conn.InitializeWithInfo(initCtx, acp.Capabilities{}, acp.ClientInfo{Name: "brokk-town", Version: "1"})
	cancel()
	if initErr != nil {
		return answer, errors.Join(errors.New("Mjolnir ACP initialization failed"), ctx.Err())
	}
	answer.Run, err = e.Runs.Prepare(ctx, plan, func(ctx context.Context, _ RunPlan) (string, error) {
		createCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		created, err := conn.NewSession(createCtx, "/") // Bundled targets never receive a Town filesystem path.
		mu.Lock()
		session = created
		mu.Unlock()
		return string(created.SessionID), err
	})
	if err != nil {
		return answer, err
	}
	if err := answer.Run.configure(ctx); err != nil {
		return answer, err
	}
	if err := answer.Run.MarkPrompting(); err != nil {
		return answer, err
	}
	mu.Lock()
	prompting = true
	mu.Unlock()
	stop, promptErr := conn.Prompt(ctx, session, prompt)
	if promptErr != nil || stop != schema.StopReasonEndTurn {
		return answer, errors.Join(errors.New("Mjolnir did not confirm a completed turn; retain its run for recovery"), ctx.Err())
	}
	mu.Lock()
	answer.Text = text.String()
	prompting = false
	mu.Unlock()
	if err := answer.Run.retainCompletedAnswer(answer.Text); err != nil {
		return answer, err
	}
	if err := answer.Run.awaitIdle(ctx); err != nil {
		return answer, err
	}
	answer.Artifacts, err = answer.Run.Collect(ctx, repair, answer.Text)
	return answer, err
}

// A live turn can finish before the controller publishes its idle snapshot.
// This only waits on reads after ACP confirmed EndTurn; Collect still checks
// the full launch receipt, unchanged runtime, revision and exact final answer.
func (r *Run) awaitIdle(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		s, err := r.owner.Catalog.ReadSession(ctx, r.identity())
		if err != nil {
			return err
		}
		if s.Lifecycle != "live" || s.HasError == nil || *s.HasError {
			return errors.New("Mjolnir session failed after its completed turn; inspect the retained run")
		}
		if s.Idle && s.ChatPhase == "idle" {
			return nil
		}
		if !waitSnapshot(ctx) {
			return errors.Join(errors.New("Mjolnir did not publish an idle snapshot after completion; retain the run"), ctx.Err())
		}
	}
}

func waitSnapshot(ctx context.Context) bool {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *Run) configure(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record.State != "ready" {
		return errors.New("Mjolnir session is not ready for configuration")
	}
	c := r.owner.Catalog
	for _, setting := range [][2]string{{"model", r.record.Plan.Model}, {"effort", r.record.Plan.Effort}} {
		if setting[1] == "" {
			continue
		}
		data, err := c.artifact(ctx, "GET", sessionPath(r.record.Session), "session configuration", "application/json", nil, maxBytes, 30*time.Second)
		if err != nil {
			return err
		}
		current, offered, err := configurationValue(data, setting[0], setting[1])
		if err != nil || !offered {
			return errors.New("selected model or effort is unavailable on the Mjolnir target; refresh its choices")
		}
		if current == setting[1] {
			continue
		}
		body, _ := json.Marshal(map[string]string{"key": setting[0], "value": setting[1]})
		data, err = c.artifact(ctx, "PATCH", sessionPath(r.record.Session)+"/config", "session configuration", "application/json", strings.NewReader(string(body)), maxBytes, 30*time.Second)
		if err != nil {
			return errors.New("Mjolnir model/effort change was not confirmed; retain this run before retrying")
		}
		current, _, err = configurationValue(data, setting[0], setting[1])
		if err != nil || current != setting[1] {
			return errors.New("Mjolnir did not confirm the selected model or effort")
		}
		// PATCH answers from live settings, whereas GET uses the asynchronously
		// published snapshot. Wait for this change before deciding the next one.
		if err := r.awaitConfiguration(ctx, [][2]string{setting}); err != nil {
			return err
		}
	}
	// Model selection may reset effort, and effort can reset other settings.
	if r.record.Plan.Model != "" || r.record.Plan.Effort != "" {
		if err := r.awaitConfiguration(ctx, [][2]string{{"model", r.record.Plan.Model}, {"effort", r.record.Plan.Effort}}); err != nil {
			return err
		}
	}
	if _, err := r.checkCurrentSession(ctx); err != nil {
		return err
	}
	diff, err := c.ReadDiff(ctx, r.record.Session, r.record.Plan.Checkout.Commit)
	if err != nil {
		return err
	}
	return diff.CheckReviewTree(r.record.Session, r.record.Plan.Checkout.Commit)
}

func (r *Run) awaitConfiguration(ctx context.Context, settings [][2]string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		data, err := r.owner.Catalog.artifact(ctx, "GET", sessionPath(r.record.Session), "session configuration", "application/json", nil, maxBytes, 30*time.Second)
		if err != nil {
			return err
		}
		matched := true
		for _, setting := range settings {
			if setting[1] != "" {
				current, offered, err := configurationValue(data, setting[0], setting[1])
				if err != nil || !offered {
					return errors.New("selected Mjolnir configuration became unavailable before prompting")
				}
				matched = matched && current == setting[1]
			}
		}
		if matched {
			return nil
		}
		if !waitSnapshot(ctx) {
			return errors.Join(errors.New("Mjolnir did not publish the selected configuration before prompting; retain the run"), ctx.Err())
		}
	}
}

func configurationValue(data []byte, key, wanted string) (string, bool, error) {
	var record struct {
		Options []struct {
			Key     string   `json:"key"`
			Current string   `json:"current"`
			Choices []Choice `json:"choices"`
		} `json:"config_options"`
	}
	if json.Unmarshal(data, &record) != nil {
		return "", false, errors.New("invalid session configuration")
	}
	current, found, offered := "", false, false
	for _, option := range record.Options {
		if option.Key == key {
			if found {
				return "", false, errors.New("duplicate session option")
			}
			found = true
			current = option.Current
			for _, choice := range option.Choices {
				offered = offered || choice.Value == wanted
			}
		}
	}
	if !found {
		return "", false, errors.New("missing session option")
	}
	return current, offered, nil
}

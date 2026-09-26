package town

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

type managedContextKey struct{}
type managedDispatch struct {
	executor  mjolnir.Executor
	config    Config
	role      Role
	placement mjolnir.Placement
	runtime   mjolnir.RuntimePin
	observe   func(Progress)
	runs      []*mjolnir.Run
	mu        sync.Mutex
	failed    bool
}

func managedSupported(t *Town, role Role) bool {
	return role == Review || (role == Issue && nextTask(t, Issue, "fixes", time.Now()) != nil)
}

func (b *BotWorkers) beginManaged(ctx context.Context, t *Town, role Role, observe func(Progress)) (*managedDispatch, error) {
	if !managedSupported(t, role) {
		return nil, errors.New("this bot duty does not yet support managed execution; select direct local execution")
	}
	if b.Mjolnir == nil || (b.Store != nil && b.Store.Snapshot().Demo) {
		return nil, errors.New("managed execution requires a configured Mjolnir connection outside demo mode")
	}
	config := t.Config.ForRole(role)
	pin := config.ExecutionRuntimeForRole(role)
	if pin == nil || pin.Validate() != nil {
		return nil, errors.New("select a known Mjolnir runtime before dispatching this bot")
	}
	rs := &mjolnir.Runs{Catalog: b.Mjolnir, Directory: filepath.Join(b.Root, "towns", Key(t.ID), "extensions", "mjolnir", string(role))}
	records, err := rs.Records()
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.State != "destroyed" {
			return nil, fmt.Errorf("Mjolnir run %s remains %s (session %s); inspect its retained evidence before another dispatch", record.Plan.ID, record.State, record.Session)
		}
	}
	placement, err := b.Mjolnir.ResolvePlacement(ctx, config.Repo, "Town "+Key(t.ID), "")
	if err != nil {
		return nil, err
	}
	command := append([]string{}, b.MjolnirCommand...)
	if len(command) == 0 {
		command = []string{"mj"}
	}
	return &managedDispatch{executor: mjolnir.Executor{Runs: rs, Command: command}, config: config, role: role, placement: placement, runtime: *pin, observe: observe}, nil
}

func (m *managedDispatch) execute(ctx context.Context, head, prompt string, repair bool) (mjolnir.Answer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed || len(m.runs) >= 32 {
		return mjolnir.Answer{}, errors.New("managed dispatch is held after a refusal or reached its session limit")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return mjolnir.Answer{}, err
	}
	id := "run-" + hex.EncodeToString(nonce[:])
	plan := mjolnir.RunPlan{ID: id, Repository: m.config.Repo, Selection: *m.config.Execution, Placement: m.placement, Checkout: mjolnir.ExactCheckout{Repository: m.placement.Repository, Commit: head, Branch: "town/" + id}, Runtime: m.runtime, Model: m.config.Agent.Model, Effort: m.config.Agent.Effort}
	if m.observe != nil {
		m.observe(Progress{Phase: "executing", Task: "Preparing Mjolnir run " + id})
	}
	answer, err := m.executor.Execute(ctx, plan, prompt, repair)
	if answer.Run != nil {
		m.runs = append(m.runs, answer.Run)
	}
	if err != nil {
		m.failed = true
		return answer, fmt.Errorf("Mjolnir run %s retained for recovery: %w", id, err)
	}
	return answer, nil
}

func (m *managedDispatch) finish(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, run := range m.runs {
		if err := run.Destroy(ctx); err != nil {
			return err
		}
	}
	return nil
}

// startRemoteAgent exposes only a single frozen review revision on a private
// socket. It cannot run arbitrary commands, switch targets, publish, or merge.
func startRemoteAgent(ctx context.Context, m *managedDispatch, head string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "bt-agent-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return "", nil, err
	}
	var gate sync.Mutex
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/agent" {
			http.NotFound(w, r)
			return
		}
		if !gate.TryLock() {
			http.Error(w, "another agent request is active", 409)
			return
		}
		defer gate.Unlock()
		var request struct {
			Protocol int    `json:"protocol"`
			Head     string `json:"head"`
			Prompt   string `json:"prompt"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.Protocol != 1 || request.Head != head || !SHA(head) || len(request.Prompt) == 0 || len(request.Prompt) > 4<<20 {
			http.Error(w, "remote agent requires the dispatched exact revision and bounded prompt", 400)
			return
		}
		call, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		defer cancel()
		answer, err := m.execute(call, head, request.Prompt, false)
		if err != nil {
			http.Error(w, "managed execution or evidence was refused; inspect the retained run", 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Protocol int    `json:"protocol"`
			Head     string `json:"head"`
			Text     string `json:"text"`
			Evidence string `json:"evidence"`
		}{1, answer.Artifacts.Head, answer.Text, answer.Run.Record().Plan.ID})
	})}
	go func() { _ = server.Serve(listener) }()
	close := func() { _ = server.Close(); _ = os.RemoveAll(dir) }
	return path, close, nil
}

func managedSetupError(err error) error { return &runner.SetupError{Err: err} }

func executionHold(t *Town, role Role) string {
	if !t.Config.ExecutionForRole(role).Managed() {
		return ""
	}
	if !managedSupported(t, role) {
		return executionPending
	}
	if t.Config.ExecutionRuntimeForRole(role) == nil {
		return "Select a known Mjolnir runtime before dispatching this bot."
	}
	return ""
}

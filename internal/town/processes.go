package town

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// workerProcess belongs to one Town lifetime. The pipe also cancels the worker
// when Town dies without a chance to send a shutdown request.
type WorkerInterruptedError struct{ Err error }

func (e *WorkerInterruptedError) Error() string {
	return "worker outcome is uncertain: " + e.Err.Error()
}
func (e *WorkerInterruptedError) Unwrap() error { return e.Err }

type workerProcess struct {
	bot       externalBot
	cmd       *exec.Cmd
	client    *http.Client
	dir       string
	parent    *os.File
	done      chan struct{}
	gate      chan struct{}
	closeOnce sync.Once
	output    *osrun.Tail
	info      workerInitialize
	next      time.Time
	failures  int
}

func startWorkerProcess(ctx context.Context, bot externalBot) (*workerProcess, error) {
	dir, err := os.MkdirTemp("", "bt-worker-")
	if err != nil {
		return nil, err
	}
	p := &workerProcess{bot: bot, dir: dir, done: make(chan struct{}), gate: make(chan struct{}, 1), output: &osrun.Tail{Capacity: 64 << 10}}
	read, write, err := os.Pipe()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	p.parent = write
	args := append(append([]string{}, bot.args...), "worker", "--socket", filepath.Join(dir, "worker.sock"))
	p.cmd = exec.Command(bot.command, args...)
	p.cmd.Env = append(os.Environ(), "BROKK_TOWN_PARENT_PIPE=1")
	p.cmd.ExtraFiles = []*os.File{read}
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.cmd.Stdout = p.output
	p.cmd.Stderr = p.output
	p.cmd.WaitDelay = time.Second
	if err = p.cmd.Start(); err != nil {
		read.Close()
		write.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	read.Close()
	go func() { _ = p.cmd.Wait(); close(p.done) }()
	p.client = workerClient(filepath.Join(dir, "worker.sock"))
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p.info, err = getWorkerInitialize(probe, p.client, 10*time.Second)
	if err == nil {
		err = validateWorkerInitialize(bot, p.info)
	}
	if err != nil {
		p.close()
		tail, _ := p.output.Text()
		return nil, fmt.Errorf("initialize %s: %w\n%s", bot.role, err, tail)
	}
	return p, nil
}
func (p *workerProcess) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *workerProcess) close() {
	p.closeOnce.Do(func() {
		if p.parent != nil {
			_ = p.parent.Close()
		}
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-p.done:
			case <-time.After(7 * time.Second):
				_ = osrun.KillGroup(p.cmd.Process.Pid)
				<-p.done
			}
		}
		if p.client != nil {
			p.client.CloseIdleConnections()
		}
		_ = os.RemoveAll(p.dir)
	})
}
func (p *workerProcess) run(ctx context.Context, request workerRequest, retry bool, deadline time.Time, observe func(Progress), started func(WorkerRun) error) (workerResult, error) {
	if request.Mode != "jobs" {
		select {
		case p.gate <- struct{}{}:
			defer func() { <-p.gate }()
		case <-ctx.Done():
			return workerResult{}, ctx.Err()
		}
	}
	if !p.alive() {
		return workerResult{}, errors.New("worker process exited")
	}
	if request.SupersededPR > 0 && !p.info.has(workerRequeueCapability) {
		return workerResult{}, errors.New("worker does not support requeue")
	}
	if err := p.bot.unchanged(); err != nil {
		return workerResult{}, err
	}
	if started != nil {
		if err := started(WorkerRun{Bot: p.info.Bot, Version: p.bot.version, Command: p.bot.command, Hash: p.bot.hash, PID: p.cmd.Process.Pid, Socket: filepath.Join(p.dir, "worker.sock"), Started: time.Now(), Deadline: deadline, Issue: request.Issue, PR: request.PR, BaseSHA: request.BaseSHA, HeadSHA: request.HeadSHA, Mode: request.Mode}); err != nil {
			return workerResult{}, err
		}
	}
	if retry {
		if !p.info.has("retry") {
			return workerResult{}, errors.New("worker does not support retry")
		}
		if err := postWorkerRetry(ctx, p.client, request); err != nil {
			return workerResult{}, err
		}
	}
	result, err := postWorkerRun(ctx, p.client, request, observe)
	result.retried = retry
	if ctx.Err() != nil {
		if request.Mode != "jobs" {
			p.close()
		}
		return result, &WorkerInterruptedError{ctx.Err()}
	}
	if e := p.bot.unchanged(); e != nil {
		err = errors.Join(err, e)
	}
	if err != nil && !result.terminal {
		err = &WorkerInterruptedError{err}
	}
	return result, err
}

// SyncProcesses starts every house, independently of whether work is enabled.
// It runs on the supervisor loop, never on an HTTP/input/render loop.
func (b *BotWorkers) SyncProcesses(ctx context.Context, state State) error {
	if state.Demo {
		return nil
	}
	b.poolMu.Lock()
	defer b.poolMu.Unlock()
	first := b.pool == nil
	if first {
		b.pool = map[string]*workerProcess{}
		b.poolContext = ctx
	}
	wanted := map[string]bool{}
	for _, t := range state.Towns {
		if t.Deleted {
			continue
		}
		for _, role := range AgentRoles {
			key := t.ID + ":" + string(role)
			wanted[key] = true
			old := b.pool[key]
			if old != nil && old.alive() {
				continue
			}
			if old != nil {
				old.close()
				if old.next.IsZero() {
					old.failures++
					old.next = time.Now().Add(time.Second << min(old.failures, 6))
				}
				if time.Now().Before(old.next) {
					continue
				}
			}
			bot, err := b.externalBot(ctx, t.Config, role)
			var p *workerProcess
			if err == nil {
				p, err = startWorkerProcess(ctx, bot)
			}
			if err != nil {
				if first {
					return fmt.Errorf("start %s: %w", key, err)
				}
				if old == nil {
					old = &workerProcess{done: make(chan struct{})}
					close(old.done)
					old.closeOnce.Do(func() {})
				}
				old.failures++
				delay := time.Second << min(old.failures, 6)
				old.next = time.Now().Add(delay)
				b.pool[key] = old
				if b.Store != nil {
					_ = b.Store.Update(func(s *State) error {
						if t := s.Towns[t.ID]; t != nil {
							t.Workers[role].Error = err.Error()
						}
						return nil
					})
				}
				continue
			}
			if old != nil {
				p.failures = old.failures
			}
			b.pool[key] = p
		}
	}
	for key, p := range b.pool {
		if !wanted[key] {
			p.close()
			delete(b.pool, key)
		}
	}
	return ctx.Err()
}
func (b *BotWorkers) Close() {
	b.poolMu.Lock()
	pool := b.pool
	b.pool = nil
	b.poolMu.Unlock()
	var wg sync.WaitGroup
	for _, p := range pool {
		wg.Add(1)
		go func() { defer wg.Done(); p.close() }()
	}
	wg.Wait()
}
func (b *BotWorkers) runPersistent(ctx context.Context, id string, bot externalBot, request workerRequest, retry bool, deadline time.Time, observe func(Progress), started func(WorkerRun) error) (workerResult, error) {
	b.poolMu.Lock()
	key := id + ":" + string(bot.role)
	p := b.pool[key]
	if p != nil && !p.alive() && (request.Mode == "jobs" || request.Mode == "retry-issue") && b.poolContext.Err() == nil {
		p.close()
		replacement, err := startWorkerProcess(ctx, bot)
		if err != nil {
			b.poolMu.Unlock()
			return workerResult{}, err
		}
		p = replacement
		b.pool[key] = p
	}
	managed := b.poolContext != nil
	b.poolMu.Unlock()
	if p == nil {
		if managed {
			return workerResult{}, errors.New("worker is not ready")
		}
		// Standalone internal calls (including protocol fixtures) own their process.
		return runWorker(ctx, bot, request, retry, deadline, observe, started)
	}
	return p.run(ctx, request, retry, deadline, observe, started)
}
func runWorker(ctx context.Context, bot externalBot, request workerRequest, retry bool, deadline time.Time, observe func(Progress), started func(WorkerRun) error) (workerResult, error) {
	p, err := startWorkerProcess(ctx, bot)
	if err != nil {
		return workerResult{}, err
	}
	defer p.close()
	return p.run(ctx, request, retry, deadline, observe, started)
}

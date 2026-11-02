// Package botlaunch starts the bot binaries on the service's own machine, the
// way the desktop app runs them: each bot listens on a loopback port with a
// token generated for that launch, is restarted if it exits, and is stopped
// when the service stops. The service still reaches each bot only over the
// network contract, through package botclient.
package botlaunch

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
	"github.com/BrokkAi/brokk-town/internal/botapi/botclient"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// Spec is one bot binary to run.
type Spec struct {
	Name, Role string
	// Executable is the bot binary's path.
	Executable string
	// Root is the bot's data root.
	Root string
	// Demo starts the bot in demo mode.
	Demo bool
}

// Process is one launched bot. It is a botapi.Bot: a call waits for the bot
// to be ready, then reaches it over loopback.
type Process struct {
	spec Spec
	log  *slog.Logger
	// raw receives the bot's own log lines, which already name the bot.
	raw *slog.Logger
	// ReadyTimeout bounds how long a call waits for a starting bot.
	ReadyTimeout time.Duration

	mu      sync.Mutex
	pid     int
	client  *botclient.Client
	ready   chan struct{} // closed while client is set
	failure error         // why the last launch failed, while none is ready
	stopped bool
	done    chan struct{}
	stop    func()
}

// Start launches a bot and keeps it running until Stop.
func Start(spec Spec, log *slog.Logger) *Process {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	p := &Process{spec: spec, log: log.With("bot", spec.Name), raw: log, ReadyTimeout: time.Minute, ready: make(chan struct{}), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	p.stop = cancel
	go p.supervise(ctx)
	return p
}

// Stop asks the bot to finish its runs and exit, then waits for it. A bot that
// does not exit within the grace period is killed.
func (p *Process) Stop() {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	p.stop()
	<-p.done
}

func (p *Process) supervise(ctx context.Context) {
	defer close(p.done)
	backoff := time.Second
	for {
		started := time.Now()
		err := p.launch(ctx)
		if ctx.Err() != nil {
			return
		}
		p.setFailure(err)
		p.log.Error("bot exited; restarting", "error", err, "in", backoff.String())
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (p *Process) setFailure(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		p.client = nil
		p.ready = make(chan struct{})
	}
	p.failure = err
}

// launch runs the bot once, until it exits or ctx ends.
func (p *Process) launch(ctx context.Context) error {
	if _, err := os.Stat(p.spec.Executable); err != nil {
		return fmt.Errorf("cannot find the %s binary at %s; install it next to bt or set BROKK_TOWN_BOT_DIR", p.spec.Name, p.spec.Executable)
	}
	token, err := newToken()
	if err != nil {
		return err
	}
	args := []string{"--listen", "127.0.0.1:0", "--root", p.spec.Root, "--exit-with-parent"}
	if p.spec.Demo {
		args = append(args, "--demo")
	}
	cmd := exec.Command(p.spec.Executable, args...)
	cmd.Env = append(childEnvironment(), botserver.EnvToken+"="+token)
	cmd.SysProcAttr = osrun.ProcessGroup()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", p.spec.Name, err)
	}
	p.mu.Lock()
	p.pid = cmd.Process.Pid
	p.mu.Unlock()
	go p.relay(stderr)
	exited := make(chan error, 1)
	readyLine := make(chan botserver.Ready, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, err := reader.ReadString('\n')
		var ready botserver.Ready
		if err == nil && json.Unmarshal([]byte(line), &ready) == nil {
			readyLine <- ready
		}
		p.relay(reader)
	}()
	go func() { exited <- cmd.Wait() }()

	var waitErr error
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	select {
	case ready := <-readyLine:
		client, err := p.connect(ready, token)
		if err != nil {
			_ = osrun.KillGroup(cmd.Process.Pid)
			<-exited
			return err
		}
		p.mu.Lock()
		p.client, p.failure = client, nil
		close(p.ready)
		p.mu.Unlock()
		p.log.Info("bot ready", "address", ready.Address, "version", ready.Version)
		select {
		case waitErr = <-exited:
			return fmt.Errorf("%s exited: %v", p.spec.Name, waitErr)
		case <-ctx.Done():
		}
	case waitErr = <-exited:
		return fmt.Errorf("%s exited before it was ready: %v", p.spec.Name, waitErr)
	case <-timeout.C:
		_ = osrun.KillGroup(cmd.Process.Pid)
		<-exited
		return fmt.Errorf("%s did not report ready within 30s", p.spec.Name)
	case <-ctx.Done():
	}
	// Closing standard input is the portable stop request: the bot cancels
	// its runs, reports what they completed and exits.
	_ = stdin.Close()
	select {
	case <-exited:
		return nil
	case <-time.After(45 * time.Second):
	}
	_ = osrun.TerminateGroup(cmd.Process.Pid)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = osrun.KillGroup(cmd.Process.Pid)
		<-exited
	}
	return nil
}

func (p *Process) connect(ready botserver.Ready, token string) (*botclient.Client, error) {
	host, _, err := net.SplitHostPort(ready.Address)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("%s reported a non-loopback address %q", p.spec.Name, ready.Address)
	}
	if ready.Role != p.spec.Role {
		return nil, fmt.Errorf("%s binary serves the %q role, not %q", p.spec.Name, ready.Role, p.spec.Role)
	}
	return botclient.New("http://"+ready.Address, token, p.spec.Name, p.spec.Role, "")
}

// relay copies the bot's own diagnostics into the service's log.
func (p *Process) relay(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		p.raw.Info(botapi.Truncate(scanner.Text(), 8<<10), "source", p.spec.Name)
	}
}

// current waits for a ready bot.
func (p *Process) current(ctx context.Context) (*botclient.Client, error) {
	timeout := p.ReadyTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		client, ready, failure, stopped := p.client, p.ready, p.failure, p.stopped
		p.mu.Unlock()
		if stopped {
			return nil, fmt.Errorf("%s: %w: the service is stopping", p.spec.Name, botapi.ErrUnavailable)
		}
		if client != nil {
			return client, nil
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			if failure == nil {
				failure = errors.New("it is still starting")
			}
			return nil, fmt.Errorf("%s: %w: %v", p.spec.Name, botapi.ErrUnavailable, failure)
		}
	}
}

func (p *Process) Run(ctx context.Context, request botapi.Request, emit func(botapi.Event)) (botapi.Result, error) {
	client, err := p.current(ctx)
	if err != nil {
		return botapi.Result{}, err
	}
	return client.Run(ctx, request, emit)
}

func (p *Process) Storage(ctx context.Context, request botapi.StorageRequest) (botapi.StorageResult, error) {
	client, err := p.current(ctx)
	if err != nil {
		return botapi.StorageResult{}, err
	}
	return client.Storage(ctx, request)
}

func (p *Process) Identity(ctx context.Context) (botapi.Identity, error) {
	client, err := p.current(ctx)
	if err != nil {
		return botapi.Identity{}, err
	}
	return client.Identity(ctx)
}

// childEnvironment is the service's environment without any bot settings of
// its own, so a launched bot takes only what its parent passes it.
func childEnvironment() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "BROKK_BOT_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

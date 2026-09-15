package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/BrokkAi/brokk-town/internal/daemon"
	"github.com/BrokkAi/brokk-town/internal/town"
)

// The service owns its own lifecycle: every client command brings it up when
// it is down, registers it with the login session so it survives crashes and
// reboots, and rolls it forward when this binary is newer. Nothing here is a
// step the operator has to remember.

const (
	startTimeout = 20 * time.Second
	stopTimeout  = 30 * time.Second
	pollInterval = 100 * time.Millisecond
)

// newManager builds the platform supervisor. Tests replace it.
var newManager = func(warn func(string)) (daemon.Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return daemon.New(daemon.Options{GOOS: runtime.GOOS, Home: home, ConfigHome: os.Getenv("XDG_CONFIG_HOME"), UID: os.Getuid(), Warn: warn})
}

// spawnService starts a detached service. Tests replace it.
var spawnService = spawnDetached

var warn = func(message string) { fmt.Fprintln(os.Stderr, "bt:", message) }

// defaultListen is the service address when nothing else was ever chosen.
const defaultListen = "127.0.0.1:8099"

// launchPreference holds the durable lifecycle settings: whether the tool may
// register the service with the login session (absent means yes) and the
// listen address the service was last started with, so a later command's
// default never rewrites a registration onto a different port.
type launchPreference struct {
	Registration string `json:"registration,omitempty"`
	Listen       string `json:"listen,omitempty"`
	DemoListen   string `json:"demo_listen,omitempty"`
}

func preferencePath(base string) string { return filepath.Join(base, "launch.json") }

func readPreference(base string) launchPreference {
	var p launchPreference
	if b, err := os.ReadFile(preferencePath(base)); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func writePreference(base string, p launchPreference) error {
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(p)
	return os.WriteFile(preferencePath(base), data, 0600)
}

func registrationEnabled(base string) bool {
	return readPreference(base).Registration != "off"
}

func setRegistration(base string, on bool) error {
	p := readPreference(base)
	p.Registration = "off"
	if on {
		p.Registration = "on"
	}
	return writePreference(base, p)
}

// resolveListen picks the address a started service binds: an explicit flag,
// which is remembered, else the remembered one, else the default.
func resolveListen(base string, demo bool, explicit string, set bool) (string, error) {
	p := readPreference(base)
	saved := &p.Listen
	if demo {
		saved = &p.DemoListen
	}
	if set {
		if err := loopbackAddress(explicit); err != nil {
			return "", err
		}
		if *saved != explicit {
			*saved = explicit
			if err := writePreference(base, p); err != nil {
				return "", err
			}
		}
		return explicit, nil
	}
	if *saved != "" {
		return *saved, nil
	}
	return defaultListen, nil
}

// flagSet reports whether the named flag was given on the command line.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// runtimeDir is where the service keeps connection.json and logs.
func runtimeDir(base string, demo bool) string {
	if demo {
		return filepath.Join(base, "demo")
	}
	return base
}

func logPaths(dir string) (string, string) {
	return filepath.Join(dir, "logs", "serve.log"), filepath.Join(dir, "logs", "serve.err.log")
}

// openLogs creates the log files owner-only before any supervisor writes to
// them: the service banner includes the local access key.
func openLogs(dir string) (*os.File, *os.File, error) {
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0700); err != nil {
		return nil, nil, err
	}
	stdout, stderr := logPaths(dir)
	out, err := os.OpenFile(stdout, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, err
	}
	errFile, err := os.OpenFile(stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		out.Close()
		return nil, nil, err
	}
	_ = os.Chmod(stdout, 0600)
	_ = os.Chmod(stderr, 0600)
	return out, errFile, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// serviceAlive reports a service that is running and answering.
func serviceAlive(ctx context.Context, dir string) (connection, bool) {
	conn, err := readConnection(dir)
	if err != nil || !processAlive(conn.PID) {
		return conn, false
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var state struct{}
	return conn, request(probe, conn, "GET", "/api/state", nil, &state) == nil
}

// serviceArgs is the command line every launcher uses. The base directory is
// absolute so the service never depends on the supervisor's environment.
func serviceArgs(base string, demo bool, listen string) []string {
	args := []string{"serve", "--state-dir", base, "--listen", listen}
	if demo {
		args = append(args, "--demo")
	}
	return args
}

// temporaryBinary recognizes go run and go test executables, which must not
// be registered or restarted because they disappear.
func temporaryBinary(exe string) bool {
	tmp := os.TempDir()
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved
	}
	return strings.HasPrefix(exe, tmp) || strings.Contains(exe, "/go-build")
}

func buildSpec(base, listen string) (daemon.Spec, error) {
	exe, err := executablePath()
	if err != nil {
		return daemon.Spec{}, err
	}
	if temporaryBinary(exe) {
		return daemon.Spec{}, fmt.Errorf("%s is a temporary build; install bt to register it", exe)
	}
	dir := runtimeDir(base, false)
	out, errFile, err := openLogs(dir)
	if err != nil {
		return daemon.Spec{}, err
	}
	out.Close()
	errFile.Close()
	stdout, stderr := logPaths(dir)
	home, _ := os.UserHomeDir()
	env := []daemon.EnvVar{{Key: "PATH", Value: os.Getenv("PATH")}, {Key: "HOME", Value: home}, {Key: "BROKK_TOWN_MANAGED", Value: "1"}}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		env = append(env, daemon.EnvVar{Key: "XDG_STATE_HOME", Value: xdg})
	}
	return daemon.Spec{Command: append([]string{exe}, serviceArgs(base, false, listen)...), WorkingDir: dir, Env: env, StdoutPath: stdout, StderrPath: stderr}, nil
}

func spawnDetached(base string, demo bool, listen string) error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	if temporaryBinary(exe) {
		return fmt.Errorf("%s is a temporary build that cannot run the town in the background; build bt or run bt serve in this terminal", exe)
	}
	dir := runtimeDir(base, demo)
	out, errFile, err := openLogs(dir)
	if err != nil {
		return err
	}
	defer out.Close()
	defer errFile.Close()
	cmd := exec.Command(exe, serviceArgs(base, demo, listen)...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// startService brings up a service that is not running: through the login
// supervisor when allowed, otherwise detached from this terminal.
func startService(ctx context.Context, base string, demo bool, listen string) error {
	if !demo && registrationEnabled(base) {
		if m, err := newManager(warn); err == nil {
			spec, err := buildSpec(base, listen)
			if err == nil {
				if err = m.Ensure(ctx, spec); err == nil {
					return nil
				}
			}
			warn("running the town without login registration: " + err.Error())
		}
	}
	return spawnService(base, demo, listen)
}

// waitReady polls for a service newer than `after` that answers requests.
func waitReady(ctx context.Context, dir string, after time.Time) (connection, error) {
	deadline := time.Now().Add(startTimeout)
	for {
		conn, alive := serviceAlive(ctx, dir)
		if alive && conn.Started.After(after) {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return conn, fmt.Errorf("town service did not start within %s%s", startTimeout, logTail(dir))
		}
		select {
		case <-ctx.Done():
			return conn, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func logTail(dir string) string {
	_, stderr := logPaths(dir)
	b, err := os.ReadFile(stderr)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return "; " + stderr + " ends with:\n" + strings.Join(lines, "\n")
}

// stopProcess terminates a service and waits for it to release the state
// lock, which it does only after its workers have stopped.
func stopProcess(ctx context.Context, dir string, conn connection) error {
	if !processAlive(conn.PID) {
		return nil
	}
	if err := syscall.Kill(conn.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(stopTimeout)
	for processAlive(conn.PID) {
		if time.Now().After(deadline) {
			return fmt.Errorf("town service pid %d did not stop within %s", conn.PID, stopTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	return nil
}

// driftReason explains why this binary should replace the running service:
// a newer release, or the same binary rebuilt since the service started. An
// older client never downgrades a service.
func driftReason(conn connection) (reason string, roll bool) {
	client := buildVersion()
	if town.NewerVersion(conn.Version, client) {
		return fmt.Sprintf("%s → %s", conn.Version, client), true
	}
	if town.NewerVersion(client, conn.Version) {
		return fmt.Sprintf("the running town service is %s; this bt is %s", conn.Version, client), false
	}
	exe, err := executablePath()
	if err != nil || exe == "" || exe != conn.Executable || conn.Started.IsZero() {
		return "", false
	}
	if info, err := os.Stat(exe); err == nil && info.ModTime().After(conn.Started) {
		return "rebuilt " + exe, true
	}
	return "", false
}

// rollService replaces a running service with this binary. The same path
// restarts in place; a different path re-registers or respawns.
func rollService(ctx context.Context, base string, demo bool, listen string, conn connection) (connection, error) {
	dir := runtimeDir(base, demo)
	exe, _ := executablePath()
	if exe == conn.Executable {
		var result struct{}
		if err := request(ctx, conn, "POST", "/api/restart", map[string]any{}, &result); err == nil {
			return waitReady(ctx, dir, conn.Started)
		}
	}
	if conn.Managed && !demo && registrationEnabled(base) {
		if m, err := newManager(warn); err == nil {
			spec, err := buildSpec(base, listen)
			if err == nil {
				if err = m.Ensure(ctx, spec); err == nil {
					if err = m.Restart(ctx); err == nil {
						return waitReady(ctx, dir, conn.Started)
					}
				}
			}
			warn("supervisor restart failed: " + err.Error())
		}
	}
	if err := stopProcess(ctx, dir, conn); err != nil {
		return conn, err
	}
	if err := startService(ctx, base, demo, listen); err != nil {
		return conn, err
	}
	return waitReady(ctx, dir, conn.Started)
}

// ensureService returns a connection to a running, current service, starting
// or replacing one as needed.
func ensureService(ctx context.Context, base string, demo bool, listen string) (connection, error) {
	dir := runtimeDir(base, demo)
	conn, alive := serviceAlive(ctx, dir)
	if !alive {
		warn("starting the town service (logs in " + filepath.Join(dir, "logs") + ")")
		if err := startService(ctx, base, demo, listen); err != nil {
			return conn, err
		}
		return waitReady(ctx, dir, time.Time{})
	}
	reason, roll := driftReason(conn)
	if reason == "" {
		return conn, nil
	}
	if !roll {
		warn(reason)
		return conn, nil
	}
	warn("restarting the town service (" + reason + ")")
	return rollService(ctx, base, demo, listen, conn)
}

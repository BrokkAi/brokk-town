package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	startTimeout = 60 * time.Second
	stopTimeout  = 30 * time.Second
	pollInterval = 100 * time.Millisecond
)

// defaultListen is the service address when nothing else was ever chosen.
const defaultListen = "127.0.0.1:8099"

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
// them: service output can name local paths and repository details. The banner
// no longer carries the access key, but logs from earlier versions did, so the
// stdout log is scrubbed before it is reopened for appending.
func openLogs(dir string) (*os.File, *os.File, error) {
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0700); err != nil {
		return nil, nil, err
	}
	stdout, stderr := logPaths(dir)
	if err := scrubAccessKeys(stdout); err != nil {
		return nil, nil, err
	}
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

// maxScrubbedLog bounds the work of scrubbing an old log. A larger log is
// truncated rather than read into memory.
const maxScrubbedLog = 8 << 20

var loggedAccessKey = regexp.MustCompile(`#token=[0-9a-f]{64}`)

// scrubAccessKeys redacts browser links written by earlier versions, which
// printed the long-lived access key into the service log.
func scrubAccessKeys(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() > maxScrubbedLog {
		return os.Truncate(path, 0)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !loggedAccessKey.Match(data) {
		return nil
	}
	data = loggedAccessKey.ReplaceAll(data, []byte("#token=[redacted]"))
	temp := path + ".scrub"
	if err = os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
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
// be registered or restarted because they disappear. Both live in a go-build
// work directory; the temp directory as a whole is not a signal, since on
// Linux every test's own directory is under /tmp.
func temporaryBinary(exe string) bool {
	for _, part := range strings.Split(filepath.ToSlash(exe), "/") {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
}

func spawnDetached(base string, demo bool, listen, config, repo string) (*exec.Cmd, error) {
	exe, err := executablePath()
	if err != nil {
		return nil, err
	}
	if temporaryBinary(exe) {
		return nil, fmt.Errorf("%s is a temporary build that cannot run the town in the background; build bt or run bt serve in this terminal", exe)
	}
	dir := runtimeDir(base, demo)
	out, errFile, err := openLogs(dir)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	defer errFile.Close()
	args := serviceArgs(base, demo, listen)
	if config != "" {
		args = append(args, "--config", config)
	}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
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
// lock after it has stopped its bot processes.
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

// requireService never starts or replaces a service.
func ensureService(ctx context.Context, base string, demo bool) (connection, error) {
	conn, alive := serviceAlive(ctx, runtimeDir(base, demo))
	if !alive {
		return conn, errors.New("Town is not running; start bt or bt -d")
	}
	return conn, nil
}

func startBackground(ctx context.Context, base string, demo bool, listen, config, repo string) error {
	if conn, alive := serviceAlive(ctx, runtimeDir(base, demo)); alive {
		return fmt.Errorf("Town is already running (pid %d at %s)", conn.PID, conn.URL)
	}
	if config != "" {
		var err error
		config, err = filepath.Abs(config)
		if err != nil {
			return err
		}
	}
	cmd, err := spawnDetached(base, demo, listen, config, repo)
	if err != nil {
		return err
	}
	conn, err := waitReady(ctx, runtimeDir(base, demo), time.Time{})
	if err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return err
	}
	if conn.PID != cmd.Process.Pid {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return fmt.Errorf("Town is already running (pid %d)", conn.PID)
	}
	_ = cmd.Process.Release()
	fmt.Print(backgroundBanner(conn, demo, filepath.Join(runtimeDir(base, demo), "logs")))
	return nil
}

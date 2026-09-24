package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
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

// openLogs creates the log files owner-only before the background service
// writes to them: its output can name local paths and repository details.
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

// temporaryBinary recognizes go run and go test executables, which cannot run
// in the background because they disappear. Both live in a go-build work
// directory; the temp directory as a whole is not a signal, since on
// Linux every test's own directory is under /tmp.
func temporaryBinary(exe string) bool {
	for _, part := range strings.Split(filepath.ToSlash(exe), "/") {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
}

func spawnDetached(base string, demo bool, listen, config string, notify *os.File) (*exec.Cmd, error) {
	exe, err := executablePath()
	if err != nil {
		return nil, err
	}
	if temporaryBinary(exe) {
		return nil, fmt.Errorf("%s is a temporary build that cannot run the town in the background; build bt or run it in this terminal", exe)
	}
	dir := runtimeDir(base, demo)
	out, errFile, err := openLogs(dir)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	defer errFile.Close()
	// Bare bt runs Town. The state directory is absolute so the child never
	// depends on this process's working directory.
	args := []string{"--state-dir", base, "--listen", listen}
	if demo {
		args = append(args, "--demo")
	}
	if config != "" {
		args = append(args, "--config", config)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// ExtraFiles[0] is descriptor 3 in the child.
	cmd.ExtraFiles = []*os.File{notify}
	cmd.Env = append(os.Environ(), readyEnv+"=3")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// logSize is the error log's length, so a failed start shows only what that
// start wrote to a log that earlier runs appended to.
func logSize(dir string) int64 {
	_, stderr := logPaths(dir)
	info, err := os.Stat(stderr)
	if err != nil {
		return 0
	}
	return info.Size()
}

// logTail returns the last lines written to the error log after offset.
func logTail(dir string, offset int64) string {
	_, stderr := logPaths(dir)
	b, err := os.ReadFile(stderr)
	if err != nil || int64(len(b)) < offset {
		return ""
	}
	b = b[offset:]
	if len(strings.TrimSpace(string(b))) == 0 {
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
func stopProcess(ctx context.Context, conn connection) error {
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

// requireService finds the running service; clients never start one.
func requireService(ctx context.Context, dir string) (connection, error) {
	conn, alive := serviceAlive(ctx, dir)
	if !alive {
		return conn, errors.New("Town is not running; start bt or bt -d")
	}
	return conn, nil
}

// readyEnv names the descriptor on which a Town started by bt -d reports that
// it is serving, in the manner of systemd's sd_notify or s6's notification-fd.
// The parent blocks on the other end: a ready line means Town is up, and end
// of file without one means it exited, so a failed start is reported at once.
const readyEnv = "BT_READY_FD"

// readyPipe is the notification descriptor this process was given, if any.
var readyPipe *os.File

// takeReadyPipe claims the notification descriptor before anything starts a
// subprocess: it is marked close-on-exec and dropped from the environment, so
// bots and agents never inherit it and a dead Town always closes the pipe.
func takeReadyPipe() *os.File {
	value, ok := os.LookupEnv(readyEnv)
	if !ok {
		return nil
	}
	os.Unsetenv(readyEnv)
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return nil
	}
	// Only a pipe is a notification descriptor; a stray variable must not make
	// Town write to whatever file happens to be open at that number.
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		return nil
	}
	syscall.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), "ready")
}

// signalReady tells a waiting bt -d that Town is serving.
func signalReady() {
	if readyPipe == nil {
		return
	}
	_, _ = readyPipe.WriteString("ready\n")
	_ = readyPipe.Close()
	readyPipe = nil
}

// awaitReady waits for the child's ready line. The parent's copy of the write
// end must already be closed, so the child exiting ends the read.
func awaitReady(ctx context.Context, cmd *exec.Cmd, ready *os.File) error {
	done := make(chan bool, 1)
	go func() {
		line, _ := bufio.NewReader(ready).ReadString('\n')
		done <- line == "ready\n"
	}()
	select {
	case ok := <-done:
		if ok {
			return nil
		}
		return fmt.Errorf("Town exited during startup: %v", cmd.Wait())
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return ctx.Err()
	}
}

func startBackground(ctx context.Context, base string, demo bool, listen, config string) error {
	dir := runtimeDir(base, demo)
	if conn, alive := serviceAlive(ctx, dir); alive {
		return fmt.Errorf("Town is already running (pid %d at %s)", conn.PID, conn.URL)
	}
	if config != "" {
		var err error
		config, err = filepath.Abs(config)
		if err != nil {
			return err
		}
	}
	ready, notify, err := os.Pipe()
	if err != nil {
		return err
	}
	defer ready.Close()
	offset := logSize(dir)
	cmd, err := spawnDetached(base, demo, listen, config, notify)
	notify.Close()
	if err != nil {
		return err
	}
	if err := awaitReady(ctx, cmd, ready); err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("%w%s", err, logTail(dir, offset))
	}
	conn, err := readConnection(dir)
	if err != nil {
		return err
	}
	_ = cmd.Process.Release()
	fmt.Print(backgroundBanner(conn, demo, filepath.Join(dir, "logs")))
	return nil
}

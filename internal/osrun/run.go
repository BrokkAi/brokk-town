// Package osrun supplies bounded command output for the Unix daemon.
package osrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

type Tail struct {
	sync.Mutex
	Bytes     []byte
	Capacity  int
	Truncated bool
}

func (t *Tail) Write(p []byte) (int, error) {
	t.Lock()
	defer t.Unlock()
	n := len(p)
	if len(p) > t.Capacity {
		p = p[len(p)-t.Capacity:]
		t.Truncated = true
	}
	if excess := len(t.Bytes) + len(p) - t.Capacity; excess > 0 {
		t.Bytes = t.Bytes[excess:]
		t.Truncated = true
	}
	t.Bytes = append(t.Bytes, p...)
	return n, nil
}
func (t *Tail) Text() (string, bool) {
	t.Lock()
	defer t.Unlock()
	b := t.Bytes
	for len(b) > 0 && !utf8.RuneStart(b[0]) {
		b = b[1:]
	}
	return strings.ToValidUTF8(string(b), "�"), t.Truncated
}
func StartCommand(ctx context.Context, dir string, args []string, env map[string]string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error { return Kill(cmd) }
	return cmd
}
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return KillGroup(cmd.Process.Pid)
}

// KillGroup ends every process in the group led by pid.
func KillGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// StartDetached prepares a long-lived worker that must outlive this process.
// The child leads a new session, so terminal hangups and the parent's exit never
// reach it, and its output goes to a file instead of a pipe that would break when
// the parent disappears. Nothing cancels it implicitly: callers end it with
// KillGroup or the worker's own shutdown request.
func StartDetached(dir string, args []string, env map[string]string, output *os.File) *exec.Cmd {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = output, output
	return cmd
}

// Alive reports whether pid still exists. Permission errors count as alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// TailFile returns up to limit bytes from the end of a file, valid UTF-8, and
// whether earlier content was omitted. A missing file reads as empty.
func TailFile(path string, limit int) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false
	}
	size := info.Size()
	truncated := size > int64(limit)
	if truncated {
		if _, err = f.Seek(size-int64(limit), 0); err != nil {
			return "", false
		}
	}
	b := make([]byte, min(size, int64(limit)))
	n, _ := f.Read(b)
	b = b[:n]
	for len(b) > 0 && !utf8.RuneStart(b[0]) {
		b = b[1:]
	}
	return strings.ToValidUTF8(string(b), "\ufffd"), truncated
}

// Run separates stdout from diagnostic stderr. JSON callers fail if truncated.
func Run(ctx context.Context, dir string, env map[string]string, args ...string) (string, error) {
	text, err := RunRaw(ctx, dir, env, args...)
	return strings.TrimSpace(text), err
}

// RunRaw preserves source and diff whitespace while retaining output bounds.
func RunRaw(ctx context.Context, dir string, env map[string]string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("empty command")
	}
	cmd := StartCommand(ctx, dir, args, env)
	stdout := &Tail{Capacity: 8 << 20}
	stderr := &Tail{Capacity: 64 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	text, cut := stdout.Text()
	detail, _ := stderr.Text()
	if cut {
		// A tail is not a complete response, even when the process also failed.
		// In particular, callers must not interpret a body fragment as headers.
		return "", errors.Join(fmt.Errorf("%s output exceeded 8 MiB", args[0]), err)
	}
	if err != nil {
		// The message is persisted and pushed to every client, so it carries
		// bounded tails. Callers that need the whole output read the returned
		// text or the worker log.
		return text, fmt.Errorf("%s: %w\n%s\n%s", args[0], err, Clip(detail, ErrorTextLimit), Clip(text, ErrorTextLimit))
	}
	return text, nil
}

// ErrorTextLimit bounds each captured stream embedded in a command failure.
const ErrorTextLimit = 4 << 10

// Clip returns at most limit bytes from the end of v, cut on a rune boundary,
// marked when earlier content was omitted. The tail is kept because a command's
// last output explains its failure.
func Clip(v string, limit int) string {
	if len(v) <= limit {
		return v
	}
	b := v[len(v)-limit:]
	for len(b) > 0 && !utf8.RuneStart(b[0]) {
		b = b[1:]
	}
	return "…(earlier output omitted)\n" + b
}

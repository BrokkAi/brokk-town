//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// detachedProcess starts Town in its own session, away from the terminal.
func detachedProcess() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// passReady hands the notification pipe to the child and names its descriptor.
func passReady(cmd *exec.Cmd, notify *os.File) string {
	// ExtraFiles[0] is descriptor 3 in the child.
	cmd.ExtraFiles = []*os.File{notify}
	return "3"
}

// inheritedReady opens the descriptor a parent sct -d passed as value.
func inheritedReady(value string) *os.File {
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

// requestStop asks the running service to shut down.
func requestStop(_ context.Context, conn connection) error { return osrun.Terminate(conn.PID) }

// interruptStarting asks a Town that has not yet reported ready to stop.
func interruptStarting(cmd *exec.Cmd) { _ = cmd.Process.Signal(syscall.SIGTERM) }

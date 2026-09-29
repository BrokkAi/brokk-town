//go:build unix

package osrun

import (
	"errors"
	"os"
	"syscall"
)

// ProcessGroup starts a command as the leader of its own process group, so
// KillGroup and TerminateGroup reach every descendant.
func ProcessGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// KillGroup ends every process in the group led by pid.
func KillGroup(pid int) error { return signalGroup(pid, syscall.SIGKILL) }

// TerminateGroup asks every process in the group led by pid to stop.
func TerminateGroup(pid int) error { return signalGroup(pid, syscall.SIGTERM) }

func signalGroup(pid int, signal syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// Terminate asks the single process pid to stop.
func Terminate(pid int) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// Alive reports whether pid still exists. Permission errors count as alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

//go:build windows

package osrun

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	ctrlBreakEvent                 = 1
)

var generateConsoleCtrlEvent = syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")

// ProcessGroup starts a command in its own console process group, so
// TerminateGroup can deliver Ctrl+Break to it without reaching Town.
func ProcessGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// KillGroup ends pid and its descendants. Windows has no process groups to
// signal, so taskkill walks the tree; a descendant whose parent already exited
// is no longer reachable from pid.
func KillGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	if !Alive(pid) {
		return os.ErrProcessDone
	}
	if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run() == nil {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return os.ErrProcessDone
	}
	defer p.Release()
	return p.Kill()
}

// TerminateGroup sends Ctrl+Break to the console process group led by pid.
// It reaches only processes attached to Town's console; callers follow it with
// KillGroup after a grace period.
func TerminateGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	if !Alive(pid) {
		return os.ErrProcessDone
	}
	if ok, _, err := generateConsoleCtrlEvent.Call(ctrlBreakEvent, uintptr(pid)); ok == 0 {
		return err
	}
	return nil
}

// Terminate has no graceful equivalent for an unrelated Windows process.
func Terminate(pid int) error {
	return errors.New("windows cannot signal another process to stop; ask it over its own API")
}

// Alive reports whether pid still exists. Access denial counts as alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if syscall.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == stillActive
}

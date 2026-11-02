//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

const createNoWindow = 0x08000000

// detachedProcess starts Town with a hidden console of its own. Closing the
// terminal does not reach it, and console bots it starts share that hidden
// console instead of each opening a window.
func detachedProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP}
}

// passReady marks the notification pipe inheritable, lists it as the only
// extra handle the child receives, and names it by handle value.
func passReady(cmd *exec.Cmd, notify *os.File) string {
	handle := syscall.Handle(notify.Fd())
	_ = syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, handle)
	return strconv.FormatUint(uint64(handle), 10)
}

// inheritedReady opens the handle a parent bt -d passed as value.
func inheritedReady(value string) *os.File {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n == 0 {
		return nil
	}
	handle := syscall.Handle(n)
	// Only a pipe is a notification handle; a stray variable must not make
	// Town write to whatever happens to be open at that value.
	if kind, err := syscall.GetFileType(handle); err != nil || kind != syscall.FILE_TYPE_PIPE {
		return nil
	}
	syscall.CloseOnExec(handle)
	return os.NewFile(uintptr(handle), "ready")
}

// requestStop asks the running service to shut down over its local API:
// Windows cannot signal a process that has its own hidden console.
func requestStop(ctx context.Context, conn connection) error {
	var reply struct{}
	return request(ctx, conn, "POST", "/api/shutdown", nil, &reply)
}

// interruptStarting ends a Town that has not yet reported ready. It has no
// API to ask yet, so its whole process tree is stopped.
func interruptStarting(cmd *exec.Cmd) { _ = osrun.KillGroup(cmd.Process.Pid) }

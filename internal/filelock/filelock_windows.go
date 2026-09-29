//go:build windows

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2
	errorLockViolation      = syscall.Errno(33)
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32.NewProc("LockFileEx")
	unlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// TryLock locks f exclusively or fails at once with ErrLocked. Windows locks
// are mandatory, so the lock covers one byte past any content a lock file has.
func TryLock(f *os.File) error {
	var overlapped syscall.Overlapped
	ok, _, err := lockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if ok != 0 {
		return nil
	}
	if errors.Is(err, errorLockViolation) || errors.Is(err, syscall.ERROR_IO_PENDING) {
		return fmt.Errorf("%w: %w", ErrLocked, err)
	}
	return err
}

// Unlock releases a lock taken by TryLock.
func Unlock(f *os.File) error {
	var overlapped syscall.Overlapped
	if ok, _, err := unlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped))); ok == 0 {
		return err
	}
	return nil
}

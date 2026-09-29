//go:build unix

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// TryLock locks f exclusively or fails at once with ErrLocked.
func TryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return fmt.Errorf("%w: %w", ErrLocked, err)
	}
	return err
}

// Unlock releases a lock taken by TryLock.
func Unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

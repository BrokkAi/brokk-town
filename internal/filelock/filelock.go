// Package filelock takes exclusive, non-blocking locks on open files. A lock
// lasts until Unlock or until the file is closed, including by process exit.
package filelock

import "errors"

// ErrLocked means another open file description holds the lock.
var ErrLocked = errors.New("file is locked")

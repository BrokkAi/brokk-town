//go:build windows

package main

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

// duplicate returns a raw handle for f with no os.File owner, as a child
// inherits it, and a function that closes it.
func duplicate(t *testing.T, f *os.File) (string, func()) {
	self, _ := syscall.GetCurrentProcess()
	var handle syscall.Handle
	if err := syscall.DuplicateHandle(self, syscall.Handle(f.Fd()), self, &handle, 0, false, syscall.DUPLICATE_SAME_ACCESS); err != nil {
		t.Fatal(err)
	}
	return strconv.FormatUint(uint64(handle), 10), func() { _ = syscall.CloseHandle(handle) }
}

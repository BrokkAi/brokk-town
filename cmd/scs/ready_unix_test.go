//go:build unix

package main

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

// duplicate returns a raw descriptor for f with no os.File owner, as a child
// inherits it, and a function that closes it.
func duplicate(t *testing.T, f *os.File) (string, func()) {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(fd), func() { _ = syscall.Close(fd) }
}

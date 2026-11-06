// Package testplatform supplies the fixture commands shared by tests.
package testplatform

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Python returns the name of a Python 3 interpreter on PATH, skipping the test
// when none is installed.
func Python(t testing.TB) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	t.Skip("test fixture needs a Python 3 interpreter")
	return ""
}

// WriteStubExecutable writes a no-op command that PATH lookup finds.
func WriteStubExecutable(t testing.TB, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

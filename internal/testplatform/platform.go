// Package testplatform supplies the platform allowances shared by tests.
package testplatform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// SkipPOSIXFakes skips a test whose fake executables are shebang scripts,
// which Windows cannot run directly.
func SkipPOSIXFakes(t testing.TB) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are POSIX shebang scripts")
	}
}

// RequirePOSIXShell skips a test whose fixture runs a POSIX shell command.
func RequirePOSIXShell(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("fixture runs a POSIX shell command")
	}
}

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

// RequireSymlinks skips a test when this account cannot create symbolic
// links, as on Windows without Developer Mode.
func RequireSymlinks(t testing.TB) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink(dir, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
}

// OwnerOnly reports whether mode is private to its owner. Windows has no
// permission bits to check; its private state relies on the profile's ACLs.
func OwnerOnly(mode os.FileMode) bool {
	return runtime.GOOS == "windows" || mode.Perm() == 0600
}

// WriteStubExecutable writes a no-op command that PATH lookup finds on this
// platform. Windows resolves only names with an executable extension.
func WriteStubExecutable(t testing.TB, dir, name string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		writeStub(t, filepath.Join(dir, name+".cmd"), "@exit /b 0\r\n")
		return
	}
	writeStub(t, filepath.Join(dir, name), "#!/bin/sh\nexit 0\n")
}

func writeStub(t testing.TB, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

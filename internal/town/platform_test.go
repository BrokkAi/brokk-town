package town

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// skipPOSIXFakes skips a test whose fake executables are shebang scripts,
// which Windows cannot run directly.
func skipPOSIXFakes(t testing.TB) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are POSIX shebang scripts")
	}
}

// requireSymlinks skips a test when this account cannot create symbolic
// links, as on Windows without Developer Mode.
func requireSymlinks(t testing.TB) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink(dir, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
}

// ownerOnly reports whether mode is private to its owner. Windows has no
// permission bits to check; its private state relies on the profile's ACLs.
func ownerOnly(mode os.FileMode) bool {
	return runtime.GOOS == "windows" || mode.Perm() == 0600
}

// exeName is the file name PATH lookup finds for a stub command. Windows
// resolves only names with an executable extension.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

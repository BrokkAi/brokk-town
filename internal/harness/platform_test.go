package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// exeName is the file name PATH lookup finds for a stub command. Windows
// resolves only names with an executable extension.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
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

package durable

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncDirAfterRename(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".tmp"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, ".tmp"), filepath.Join(dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if err := SyncDir(dir); err != nil {
		t.Fatal(err)
	}
}

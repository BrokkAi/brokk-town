package osrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailFileBoundsAndMissing(t *testing.T) {
	if text, cut := TailFile(filepath.Join(t.TempDir(), "missing"), 16); text != "" || cut {
		t.Fatalf("missing file should read empty: %q %v", text, cut)
	}
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 40)+"tail"), 0600); err != nil {
		t.Fatal(err)
	}
	text, cut := TailFile(path, 8)
	if text != "aaaatail" || !cut {
		t.Fatalf("tail = %q cut=%v", text, cut)
	}
}

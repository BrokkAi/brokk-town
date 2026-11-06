package pathcmp

import (
	"path/filepath"
	"testing"
)

func TestSameCleansPaths(t *testing.T) {
	if !Same(filepath.Join("a", "b"), filepath.Join("a", "b")) {
		t.Fatal("identical paths differ")
	}
	if !Same(filepath.Join("a", "b"), filepath.Join("a", ".", "b")) {
		t.Fatal("uncleaned paths differ")
	}
	if Same("/a", "/b") || Same("/state", "/State") {
		t.Fatal("different paths match")
	}
}

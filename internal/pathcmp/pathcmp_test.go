package pathcmp

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestSameCleansSeparatorsAndCase(t *testing.T) {
	if !Same(filepath.Join("a", "b"), filepath.Join("a", "b")) {
		t.Fatal("identical paths differ")
	}
	if !Same(filepath.Join("a", "b"), filepath.Join("a", ".", "b")) {
		t.Fatal("uncleaned paths differ")
	}
	if runtime.GOOS != "windows" {
		return
	}
	if !Same(`C:/Users/ryan/state`, `c:\users\ryan\state`) {
		t.Fatal("git-style Windows path differs from a native one")
	}
	if Same(`C:\a`, `C:\b`) {
		t.Fatal("different paths match")
	}
}

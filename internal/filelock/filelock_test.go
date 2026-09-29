package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockExcludesAnotherOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	open := func() *os.File {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	first, second := open(), open()
	if err := TryLock(first); err != nil {
		t.Fatal(err)
	}
	if err := TryLock(second); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: got %v, want ErrLocked", err)
	}
	if err := Unlock(first); err != nil {
		t.Fatal(err)
	}
	if err := TryLock(second); err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second.Close()
	third := open()
	if err := TryLock(third); err != nil {
		t.Fatalf("lock after holder closed: %v", err)
	}
}

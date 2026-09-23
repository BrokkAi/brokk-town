package town

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The merge gate runs on the supervisor's own context, outside any worker
// attempt, so a stalled gh must not hold the review house indefinitely.
func TestGateGivesUpOnAStalledGitHub(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous := githubTimeout
	githubTimeout = 200 * time.Millisecond
	t.Cleanup(func() { githubTimeout = previous })

	started := time.Now()
	_, err := GitHubClient{}.Gate(context.Background(), "o/r", 1)
	if err == nil {
		t.Fatal("a stalled gh produced a merge gate")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("gate waited %s for a stalled gh", elapsed)
	}
}

package town

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stallingGH puts a gh on PATH that never answers.
func stallingGH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The merge gate runs on the supervisor's own context, outside any worker
// attempt, so a stalled gh must not hold the review house indefinitely, and
// the operator must be told it timed out rather than that gh was killed.
func TestGateGivesUpOnAStalledGitHub(t *testing.T) {
	stallingGH(t)
	started := time.Now()
	_, err := GitHubClient{Timeout: 200 * time.Millisecond}.Gate(context.Background(), "o/r", 1)
	if err == nil {
		t.Fatal("a stalled gh produced a merge gate")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("gate waited %s for a stalled gh", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Fatalf("timeout error = %q", err)
	}
}

func TestAPIReportsItsTimeout(t *testing.T) {
	stallingGH(t)
	_, err := GitHubClient{Timeout: 200 * time.Millisecond}.Pull(context.Background(), "o/r", 1)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "gh api timed out after 200ms") {
		t.Fatalf("timeout error = %v", err)
	}
}

// A caller's own cancellation is not reported as the client's timeout.
func TestGitHubCancellationIsNotATimeout(t *testing.T) {
	stallingGH(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := GitHubClient{}.Gate(ctx, "o/r", 1)
	if err == nil || strings.Contains(err.Error(), "timed out after") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller cancellation error = %v", err)
	}
}

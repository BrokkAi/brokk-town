package featurebot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Prune previews completed scan workspaces older than olderThan; apply removes
// them through Git, including untracked and ignored research artifacts. It
// never fetches, contacts GitHub or runs an agent.
func Prune(ctx context.Context, cfg Config, olderThan time.Duration, apply bool, out io.Writer) error {
	return prune(ctx, cfg, olderThan, apply, out, time.Now())
}

func prune(ctx context.Context, cfg Config, age time.Duration, apply bool, out io.Writer, now time.Time) error {
	if age <= 0 {
		return errors.New("--older-than must be a positive duration")
	}
	// Preview also takes the research locks so it reads a consistent lifecycle
	// snapshot; both modes refuse to run while research holds them.
	unlock, err := lockConfig(cfg)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := ReadState(cfg)
	if err != nil {
		return err
	}
	if s == nil {
		s = newState(cfg)
	}
	activeDirectory := ""
	if s.Scan != nil {
		activeDirectory, err = canonical(s.Scan.Directory)
		if err != nil {
			return fmt.Errorf("resolve active workspace: %w", err)
		}
		if _, err := fmt.Fprintf(out, "skip %q: active scan (including exhausted retries and unresolved publication)\n", s.Scan.Directory); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out, "Workspaces without completion records are ineligible and remain externally managed."); err != nil {
		return err
	}
	if len(s.Workspaces) == 0 {
		return nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return err
	}
	g := checkout{cfg}
	// Unlike open, this validates only local identity and never fetches or clones.
	root, err := g.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if root != cfg.Directory {
		return errors.New("directory must be the root of a managed clone")
	}
	remote, err := g.git(ctx, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if remote != cfg.Remote {
		return errors.New("managed clone origin differs from configuration")
	}
	cutoff := now.Add(-age)
	var failures []error
	// Each retirement is saved atomically on its own. If a save is lost, the
	// retained record is retired on a later run once path and registration are gone.
	for i := 0; i < len(s.Workspaces); {
		w := s.Workspaces[i]
		reason := ""
		if s.Scan != nil && (s.Scan.Directory == w.Directory || activeDirectory == w.Directory) {
			reason = "active scan"
		} else if !w.CompletedAt.Before(cutoff) {
			reason = "not older than requested duration"
		}
		gone := false
		if reason == "" {
			if gone, err = g.pruneCheck(ctx, w); err != nil {
				reason = err.Error()
			}
		}
		if reason != "" {
			if _, err := fmt.Fprintf(out, "skip %q: %s\n", w.Directory, reason); err != nil {
				return err
			}
			i++
			continue
		}
		if !apply {
			action := "eligible"
			if gone {
				action = "already removed"
			}
			if _, err := fmt.Fprintf(out, "%s %q (commit %s, completed %s)\n", action, w.Directory, w.Commit, w.CompletedAt.Format(time.RFC3339)); err != nil {
				return err
			}
			i++
			continue
		}
		if !gone {
			// A single --force removes research artifacts but never overrides a Git lock.
			if _, err := g.git(ctx, "worktree", "remove", "--force", "--", w.Directory); err != nil {
				failures = append(failures, fmt.Errorf("remove %q: %w", w.Directory, err))
				if _, outputErr := fmt.Fprintf(out, "failed %q: %s\n", w.Directory, err); outputErr != nil {
					return outputErr
				}
				i++
				continue
			}
		}
		s.Workspaces = append(s.Workspaces[:i], s.Workspaces[i+1:]...)
		if err := writeState(cfg, s); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "removed %q\n", w.Directory); err != nil {
			return err
		}
	}
	return errors.Join(failures...)
}

type pruneWorktree struct {
	path, head       string
	detached, locked bool
}

// pruneCheck returns nil when w may be removed, or reports that both its
// directory and registration are already gone.
func (g checkout) pruneCheck(ctx context.Context, w CompletedWorkspace) (gone bool, err error) {
	if !validScanDirectory(g.config, w.Directory) {
		return false, errors.New("workspace is outside the scan directory")
	}
	resolved, err := canonical(w.Directory)
	if err != nil {
		return false, err
	}
	if resolved != w.Directory {
		return false, errors.New("workspace path contains a symlink")
	}
	listing, err := g.git(ctx, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return false, err
	}
	var registered *pruneWorktree
	for _, block := range strings.Split(listing, "\x00\x00") {
		var entry pruneWorktree
		for _, field := range strings.Split(block, "\x00") {
			switch {
			case strings.HasPrefix(field, "worktree "):
				entry.path = strings.TrimPrefix(field, "worktree ")
			case strings.HasPrefix(field, "HEAD "):
				entry.head = strings.TrimPrefix(field, "HEAD ")
			case field == "detached":
				entry.detached = true
			case field == "locked" || strings.HasPrefix(field, "locked "):
				entry.locked = true
			}
		}
		if entry.path == w.Directory {
			registered = &entry
			break
		}
	}
	_, statErr := os.Lstat(w.Directory)
	missing := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !missing {
		return false, statErr
	}
	if registered == nil {
		if missing {
			return true, nil
		}
		return false, errors.New("not a registered worktree of the managed clone")
	}
	if registered.locked {
		return false, errors.New("Git worktree is locked")
	}
	if !registered.detached {
		return false, errors.New("worktree HEAD is not detached")
	}
	if registered.head != w.Commit {
		return false, errors.New("worktree HEAD differs from recorded commit")
	}
	common, err := g.git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false, err
	}
	// A missing directory can still hold staged data in its private index.
	if missing {
		return false, checkMissingIndex(ctx, g, common, w)
	}
	work := g
	work.config.Directory = w.Directory
	actual, err := work.git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false, err
	}
	if actual != common {
		return false, errors.New("worktree belongs to another repository")
	}
	gitdir, err := work.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return false, err
	}
	back, err := os.ReadFile(filepath.Join(gitdir, "gitdir"))
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(back)) != filepath.Join(w.Directory, ".git") {
		return false, errors.New("worktree registration points to another directory")
	}
	root, err := work.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return false, err
	}
	if root != w.Directory {
		return false, errors.New("scan directory is not its worktree root")
	}
	// Index flags can conceal tracked changes from diff. Conservatively retain
	// these worktrees, including sparse checkouts, until the flags are cleared.
	files, err := work.git(ctx, "ls-files", "-v", "-z")
	if err != nil {
		return false, err
	}
	for _, file := range strings.Split(files, "\x00") {
		if len(file) > 0 && (file[0] == 'S' || file[0] >= 'a' && file[0] <= 'z') {
			return false, errors.New("worktree index flags can hide tracked modifications")
		}
	}
	if _, err := work.git(ctx, "diff", "--no-ext-diff", "--ignore-submodules=none", "--exit-code", "HEAD", "--"); err != nil {
		return false, fmt.Errorf("worktree has tracked modifications: %w", err)
	}
	// Staged and unstaged changes could cancel out, so check the index separately.
	if _, err := work.git(ctx, "diff", "--no-ext-diff", "--ignore-submodules=none", "--cached", "--exit-code", "HEAD", "--"); err != nil {
		return false, fmt.Errorf("worktree has staged modifications: %w", err)
	}
	return false, nil
}

func checkMissingIndex(ctx context.Context, g checkout, common string, w CompletedWorkspace) error {
	entries, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		admin := filepath.Join(common, "worktrees", entry.Name())
		back, err := os.ReadFile(filepath.Join(admin, "gitdir"))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(back)) != filepath.Join(w.Directory, ".git") {
			continue
		}
		if _, err := g.git(ctx, "--git-dir="+admin, "diff", "--no-ext-diff", "--ignore-submodules=none", "--cached", "--exit-code", w.Commit, "--"); err != nil {
			return fmt.Errorf("missing worktree has staged modifications or an unreadable index: %w", err)
		}
		return nil
	}
	return errors.New("missing worktree registration could not be verified")
}

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

	"github.com/BrokkAi/feature-bot/internal/osrun"
)

// Prune previews completed scan workspaces older than olderThan; apply removes
// them through Git, including untracked and ignored research artifacts. It
// never fetches, contacts GitHub or runs an agent. Policy skips are reported;
// Git or filesystem errors also make it fail.
func Prune(ctx context.Context, cfg Config, olderThan time.Duration, apply bool, out io.Writer) error {
	return prune(ctx, cfg, olderThan, apply, out, time.Now())
}

// gitRepositoryEnv are inherited variables that could redirect prune's Git
// calls to another repository, index or work tree (git rev-parse --local-env-vars).
var gitRepositoryEnv = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_COUNT",
	"GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_GRAFT_FILE", "GIT_IMPLICIT_WORK_TREE",
	"GIT_INDEX_FILE", "GIT_NAMESPACE", "GIT_NO_REPLACE_OBJECTS", "GIT_OBJECT_DIRECTORY",
	"GIT_PREFIX", "GIT_REPLACE_REF_BASE", "GIT_SHALLOW_FILE", "GIT_WORK_TREE",
}

func (g checkout) pruneGit(ctx context.Context, args ...string) (string, error) {
	return osrun.RunWithout(ctx, g.config.Directory, map[string]string{"GIT_TERMINAL_PROMPT": "0"}, gitRepositoryEnv, append([]string{"git"}, args...)...)
}

// pruneSkip is a policy decision to keep a workspace, not an operational failure.
type pruneSkip string

func (s pruneSkip) Error() string { return string(s) }

// skipOnExit1 treats Git's "differences found" exit status as a policy skip
// and any other failure as an error.
func skipOnExit1(err error, reason string) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return pruneSkip(reason)
	}
	return fmt.Errorf("%s check failed: %w", reason, err)
}

type pruneAction int

const (
	pruneRemove      pruneAction = iota // registered worktree, possibly with its directory gone
	pruneInterrupted                    // directory left without .git by an interrupted removal
	pruneRetire                         // directory and registration are both gone
)

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
	root, err := g.pruneGit(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if root != cfg.Directory {
		return errors.New("directory must be the root of a managed clone")
	}
	remote, err := g.pruneGit(ctx, "remote", "get-url", "origin")
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
		var check error
		action := pruneRemove
		if s.Scan != nil && (s.Scan.Directory == w.Directory || activeDirectory == w.Directory) {
			check = pruneSkip("active scan")
		} else if !w.CompletedAt.Before(cutoff) {
			check = pruneSkip("not older than requested duration")
		} else {
			action, check = g.pruneCheck(ctx, w)
		}
		if check != nil {
			var policy pruneSkip
			if !errors.As(check, &policy) {
				failures = append(failures, fmt.Errorf("check %q: %w", w.Directory, check))
			}
			if _, err := fmt.Fprintf(out, "skip %q: %s\n", w.Directory, check); err != nil {
				return err
			}
			i++
			continue
		}
		if !apply {
			label := map[pruneAction]string{pruneRemove: "eligible", pruneInterrupted: "eligible (interrupted removal)", pruneRetire: "already removed"}[action]
			if _, err := fmt.Fprintf(out, "%s %q (commit %s, completed %s)\n", label, w.Directory, w.Commit, w.CompletedAt.Format(time.RFC3339)); err != nil {
				return err
			}
			i++
			continue
		}
		var removeErr error
		switch action {
		case pruneInterrupted:
			// Git cannot remove a registered directory that lost its .git file,
			// so finish deleting the validated directory, then its registration.
			removeErr = os.RemoveAll(w.Directory)
			if removeErr == nil {
				_, removeErr = g.pruneGit(ctx, "worktree", "remove", "--force", "--", w.Directory)
			}
		case pruneRemove:
			// A single --force removes research artifacts but never overrides a Git lock.
			_, removeErr = g.pruneGit(ctx, "worktree", "remove", "--force", "--", w.Directory)
		}
		if removeErr != nil {
			failures = append(failures, fmt.Errorf("remove %q: %w", w.Directory, removeErr))
			if _, err := fmt.Fprintf(out, "failed %q: %s\n", w.Directory, removeErr); err != nil {
				return err
			}
			i++
			continue
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

// pruneCheck decides how w may be removed. A pruneSkip error is a policy
// decision; any other error is an operational failure.
func (g checkout) pruneCheck(ctx context.Context, w CompletedWorkspace) (pruneAction, error) {
	if !validScanDirectory(g.config, w.Directory) {
		return 0, pruneSkip("workspace is outside the scan directory")
	}
	resolved, err := canonical(w.Directory)
	if err != nil {
		return 0, err
	}
	if resolved != w.Directory {
		return 0, pruneSkip("workspace path contains a symlink")
	}
	// -z requires Git 2.36 or newer.
	listing, err := g.pruneGit(ctx, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return 0, err
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
		return 0, statErr
	}
	if registered == nil {
		if missing {
			return pruneRetire, nil
		}
		return 0, pruneSkip("not a registered worktree of the managed clone")
	}
	if registered.locked {
		return 0, pruneSkip("Git worktree is locked")
	}
	if !registered.detached {
		return 0, pruneSkip("worktree HEAD is not detached")
	}
	if registered.head != w.Commit {
		return 0, pruneSkip("worktree HEAD differs from recorded commit")
	}
	common, err := g.pruneGit(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return 0, err
	}
	if missing {
		// A missing directory can still hold staged data in its private index.
		return pruneRemove, g.checkAdminIndex(ctx, common, w, false)
	}
	if _, err := os.Lstat(filepath.Join(w.Directory, ".git")); errors.Is(err, os.ErrNotExist) {
		return pruneInterrupted, g.checkAdminIndex(ctx, common, w, true)
	} else if err != nil {
		return 0, err
	}
	work := g
	work.config.Directory = w.Directory
	actual, err := work.pruneGit(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return 0, err
	}
	if actual != common {
		return 0, pruneSkip("worktree belongs to another repository")
	}
	gitdir, err := work.pruneGit(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return 0, err
	}
	back, err := os.ReadFile(filepath.Join(gitdir, "gitdir"))
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(string(back)) != filepath.Join(w.Directory, ".git") {
		return 0, pruneSkip("worktree registration points to another directory")
	}
	root, err := work.pruneGit(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return 0, err
	}
	if root != w.Directory {
		return 0, pruneSkip("scan directory is not its worktree root")
	}
	if err := checkIndexFlags(ctx, work); err != nil {
		return 0, err
	}
	if _, err := work.pruneGit(ctx, "diff", "--no-ext-diff", "--ignore-submodules=none", "--exit-code", "HEAD", "--"); err != nil {
		return 0, skipOnExit1(err, "worktree has tracked modifications")
	}
	// Staged and unstaged changes could cancel out, so check the index separately.
	if _, err := work.pruneGit(ctx, "diff", "--no-ext-diff", "--ignore-submodules=none", "--cached", "--exit-code", "HEAD", "--"); err != nil {
		return 0, skipOnExit1(err, "worktree has staged modifications")
	}
	return pruneRemove, nil
}

// checkIndexFlags retains worktrees whose index flags (assume-unchanged,
// skip-worktree, sparse) can conceal tracked changes from diff.
func checkIndexFlags(ctx context.Context, g checkout, gitArgs ...string) error {
	files, err := g.pruneGit(ctx, append(gitArgs, "ls-files", "-v", "-z")...)
	if err != nil {
		return err
	}
	for _, file := range strings.Split(files, "\x00") {
		if len(file) > 0 && (file[0] == 'S' || file[0] >= 'a' && file[0] <= 'z') {
			return pruneSkip("worktree index flags can hide tracked modifications")
		}
	}
	return nil
}

// checkAdminIndex validates a registration whose work tree is gone or lost its
// .git file, using the private index in the clone's worktree admin directory.
// With files, the remaining directory must hold no modified or added tracked
// files; deletions are what an interrupted removal leaves behind.
func (g checkout) checkAdminIndex(ctx context.Context, common string, w CompletedWorkspace, files bool) error {
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
		if _, err := g.pruneGit(ctx, "--git-dir="+admin, "diff", "--no-ext-diff", "--ignore-submodules=none", "--cached", "--exit-code", w.Commit, "--"); err != nil {
			return skipOnExit1(err, "worktree index has staged modifications")
		}
		if !files {
			return nil
		}
		work := g
		work.config.Directory = w.Directory
		tree := []string{"--git-dir=" + admin, "--work-tree=" + w.Directory}
		if err := checkIndexFlags(ctx, work, tree...); err != nil {
			return err
		}
		if _, err := work.pruneGit(ctx, append(tree, "diff", "--no-ext-diff", "--ignore-submodules=none", "--exit-code", "--diff-filter=d", "--")...); err != nil {
			return skipOnExit1(err, "interrupted removal left tracked modifications; see README to recover")
		}
		return nil
	}
	return errors.New("worktree registration could not be verified")
}

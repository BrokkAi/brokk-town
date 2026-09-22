package mayorbot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BrokkAi/mayor-bot/internal/osrun"
)

type checkout struct{ config Config }

func (g checkout) git(ctx context.Context, args ...string) (string, error) {
	return osrun.Run(ctx, g.config.Directory, map[string]string{"GIT_TERMINAL_PROMPT": "0"}, append([]string{"git"}, args...)...)
}
func (g checkout) head(ctx context.Context) (string, error) {
	return g.git(ctx, "rev-parse", "refs/remotes/origin/"+g.config.Branch)
}
func (g checkout) open(ctx context.Context) error {
	if _, err := os.Stat(g.config.Directory); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(g.config.Directory), 0700); err != nil {
			return err
		}
		if _, err := osrun.Run(ctx, "", map[string]string{"GIT_TERMINAL_PROMPT": "0"}, "git", "clone", "--branch", g.config.Branch, "--", g.config.Remote, g.config.Directory); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	root, err := g.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if root != g.config.Directory {
		return errors.New("directory must be the root of a managed clone")
	}
	remote, err := g.git(ctx, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if remote != g.config.Remote {
		return errors.New("managed clone origin differs from configuration")
	}
	if _, err := g.git(ctx, "check-ref-format", "refs/heads/"+g.config.Branch); err != nil {
		return err
	}
	_, err = g.git(ctx, "fetch", "--prune", "origin", "+refs/heads/"+g.config.Branch+":refs/remotes/origin/"+g.config.Branch)
	return err
}

// fetchItem retrieves a ref outside the managed branch refspec, such as a
// pull-request head, and reports the revision it resolves to. The ref names a
// refspec on origin, never a repository, so origin must precede it.
func (g checkout) fetchItem(ctx context.Context, ref, expected string) (string, error) {
	if _, err := g.git(ctx, "fetch", "--no-tags", "--", "origin", ref); err != nil {
		return "", err
	}
	rev, err := g.git(ctx, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", err
	}
	if rev != expected {
		return "", fmt.Errorf("GitHub revision moved: expected %s, fetched %s", expected, rev)
	}
	return rev, nil
}

// itemWorktree provides an immutable checkout at one exact revision. Issue
// judgments and bulletins use the branch head; PR judgments use the revision
// fetchItem resolved from GitHub's pull-request ref. A worktree an earlier run
// left behind is reused only when it is still pristine at that revision.
// Anything else is discarded rather than reported: these directories belong to
// the bot, and one stale worktree would otherwise fail every later run on the
// same item.
func (g checkout) itemWorktree(ctx context.Context, name, rev string) (checkout, error) {
	w := g
	w.config.Directory = filepath.Join(g.config.Directory+"-items", name)
	if w.pristine(ctx, rev) {
		return w, nil
	}
	if err := g.discard(ctx, w.config.Directory); err != nil {
		return checkout{}, err
	}
	if err := os.MkdirAll(filepath.Dir(w.config.Directory), 0700); err != nil {
		return checkout{}, err
	}
	if _, err := g.git(ctx, "worktree", "add", "--detach", "--", w.config.Directory, rev); err != nil {
		return checkout{}, err
	}
	return w, w.verify(ctx, rev)
}

// pristine reports whether an existing item worktree is exactly the checkout a
// fresh one would be. Untracked leftovers count against it: the agent would
// see them, and verify only inspects tracked source.
func (g checkout) pristine(ctx context.Context, rev string) bool {
	root, err := g.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil || root != g.config.Directory {
		return false
	}
	head, err := g.git(ctx, "rev-parse", "HEAD")
	if err != nil || head != rev {
		return false
	}
	status, err := g.git(ctx, "status", "--porcelain")
	return err == nil && status == ""
}

// discard clears both halves of a stale item worktree. The directory and git's
// registration of it can each outlive the other, and either survivor fails the
// next `worktree add` at that path.
func (g checkout) discard(ctx context.Context, dir string) error {
	if _, err := os.Stat(dir); err == nil {
		if _, err := g.git(ctx, "worktree", "remove", "--force", "--", dir); err != nil {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := g.git(ctx, "worktree", "prune")
	return err
}
func (g checkout) verify(ctx context.Context, expected string) error {
	root, err := g.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if root != g.config.Directory {
		return errors.New("item directory is not its worktree root")
	}
	head, err := g.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != expected {
		return errors.New("agent changed the item commit")
	}
	if _, err := g.git(ctx, "diff", "--exit-code", "HEAD", "--"); err != nil {
		return fmt.Errorf("agent modified tracked source: %w", err)
	}
	return nil
}

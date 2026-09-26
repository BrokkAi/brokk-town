package mjolnir

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// ImportRepair imports only a verified advertised commit into a caller-owned
// private checkout. It never pushes, updates a contributor branch, or treats an
// export as a review. Town's ownership, independent review and GitHub write gates
// still apply. Verification runs on the imported commit and may not change it.
func ImportRepair(ctx context.Context, directory, privateBranch, base, head string, bundle []byte, verify []string) error {
	if !filepath.IsAbs(directory) || !validCheckoutBranch(privateBranch) || (!strings.HasPrefix(privateBranch, "town-repair-") && !strings.HasPrefix(privateBranch, "town/")) || !exactCommit(base) || !exactCommit(head) || base == head || len(bundle) == 0 || len(bundle) > maxBundleBytes {
		return errors.New("repair import requires a private checkout, exact changed commit and bounded bundle")
	}
	git := func(args ...string) (string, error) {
		out, err := osrun.Run(ctx, directory, map[string]string{"GIT_TERMINAL_PROMPT": "0"}, append([]string{"git", "-c", "core.hooksPath=/dev/null"}, args...)...)
		if err != nil {
			return "", errors.New("repair bundle Git verification failed; retain the private checkout and Mjolnir session")
		}
		return out, nil
	}
	check := func(expected string) error {
		branch, err := git("symbolic-ref", "--short", "HEAD")
		if err != nil || branch != privateBranch {
			return errors.New("repair import left its private branch")
		}
		current, err := git("rev-parse", "HEAD")
		if err != nil || current != expected {
			return errors.New("repair import or verification changed the expected HEAD")
		}
		status, err := git("status", "--porcelain", "--untracked-files=all")
		if err != nil || status != "" {
			return errors.New("repair import or verification left uncommitted files")
		}
		return nil
	}
	if err := check(base); err != nil {
		return err
	}
	// Keep transport bytes outside the checkout so status checks include every
	// file the agent or verification command could have left behind.
	file, err := os.CreateTemp(filepath.Dir(directory), ".town-import-*.bundle")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	_, writeErr := file.Write(bundle)
	syncErr, closeErr := file.Sync(), file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if _, err := git("bundle", "verify", path); err != nil {
		return err
	}
	refs, err := git("bundle", "list-heads", path)
	if err != nil {
		return err
	}
	ref := ""
	for _, line := range strings.Split(refs, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == head && (fields[1] == "HEAD" || strings.HasPrefix(fields[1], "refs/heads/")) {
			ref = fields[1]
			break
		}
	}
	if ref == "" {
		return errors.New("repair bundle does not advertise the recorded exact commit")
	}
	// Fetch objects only. No remote-tracking or contributor branch is updated.
	if _, err := git("-c", "protocol.file.allow=always", "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--", path, ref); err != nil {
		return err
	}
	if _, err := git("merge-base", "--is-ancestor", base, head); err != nil {
		return errors.New("repair bundle rewrites the starting history")
	}
	diff, err := git("diff", "--stat", base, head)
	if err != nil || diff == "" {
		return errors.New("repair bundle contains no committed code change")
	}
	if err := check(base); err != nil {
		return err
	}
	if _, err := git("merge", "--ff-only", "--no-edit", head); err != nil {
		return err
	}
	if err := check(head); err != nil {
		return err
	}
	if len(verify) > 0 {
		if _, err := osrun.Run(ctx, directory, nil, verify...); err != nil {
			return errors.New("operator verification failed on the imported repair commit; retain the checkout")
		}
	}
	return check(head)
}

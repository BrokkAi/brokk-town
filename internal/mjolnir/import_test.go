package mjolnir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

func TestRepairBundleImportVerifiesHistoryExactCommitAndOperatorChecks(t *testing.T) {
	// The importer also starts Git, so isolate every child process, including
	// those outside the fixture helper. Detached maintenance can otherwise keep
	// writing .git/objects/pack while t.TempDir removes the private repository.
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "maintenance.auto")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	t.Setenv("GIT_CONFIG_KEY_1", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_1", "0")
	for _, scenario := range []string{"success", "wrong head", "corrupt", "rewritten", "verification dirty", "verification head", "verification failure", "contributor branch"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			remote, local := filepath.Join(root, "source"), filepath.Join(root, "private")
			run := func(dir string, args ...string) string {
				t.Helper()
				out, err := osrun.Run(t.Context(), dir, map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null"}, append([]string{"git", "-c", "user.name=Test", "-c", "user.email=test@example.test"}, args...)...)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			run(root, "init", remote)
			if err := os.WriteFile(filepath.Join(remote, "code"), []byte("before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			run(remote, "add", "code")
			run(remote, "commit", "-m", "base")
			base := run(remote, "rev-parse", "HEAD")
			run(root, "clone", "--no-local", remote, local)
			branch := "town-repair-fixture"
			if scenario == "contributor branch" {
				branch = "contributor-work"
			}
			run(local, "checkout", "-b", branch)
			if scenario == "rewritten" {
				run(remote, "checkout", "--orphan", "other")
			}
			if err := os.WriteFile(filepath.Join(remote, "code"), []byte("after\n"), 0600); err != nil {
				t.Fatal(err)
			}
			run(remote, "add", "code")
			run(remote, "commit", "-m", "repair")
			head := run(remote, "rev-parse", "HEAD")
			sourceHead := head
			path := filepath.Join(root, "repair.bundle")
			run(remote, "bundle", "create", path, "HEAD")
			bundle, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			verify := []string{"sh", "-c", "test \"$(cat code)\" = after"}
			switch scenario {
			case "wrong head":
				head = evidenceHead
			case "corrupt":
				bundle = []byte("not a bundle")
			case "verification dirty":
				verify = []string{"sh", "-c", "echo altered > code"}
			case "verification head":
				verify = []string{"git", "reset", "--hard", base}
			case "verification failure":
				verify = []string{"sh", "-c", "exit 1"}
			}
			err = ImportRepair(t.Context(), local, branch, base, head, bundle, verify)
			if scenario == "success" {
				if err != nil || run(local, "rev-parse", "HEAD") != head {
					t.Fatalf("verified import: %v", err)
				}
			} else if err == nil {
				t.Fatal("accepted invalid import")
			}
			if run(remote, "rev-parse", "HEAD") != sourceHead {
				t.Fatal("modified source repository")
			}
		})
	}
}

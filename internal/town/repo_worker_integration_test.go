package town

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

// Exercise the actual standalone worker: a fake worker cannot catch a mismatch
// between Town's optional branch and Repo Bot's configuration validation.
func TestRepoWorkerResolvesTheDefaultBranch(t *testing.T) {
	bin := t.TempDir()
	worker := filepath.Join(bin, "brp")
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags", "-X main.version=0.0.0", "-o", worker, "./cmd/brp")
	build.Dir = "../../bots/repo-bot"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build repo worker: %v\n%s", err, out)
	}
	// Only these reads are allowed. Git and agents must never run in this test.
	writeFakeWorker(t, filepath.Join(bin, "gh"), `#!/bin/sh
[ "$1" = api ] && [ "$4" = --method ] && [ "$5" = GET ] || exit 1
case "$6" in
  repos/acme/orchard) echo '{"default_branch":"main"}' ;;
  repos/acme/orchard/branches/main|repos/acme/orchard/branches/stable)
    echo '{"commit":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}' ;;
  repos/acme/orchard/issues\?*|repos/acme/orchard/pulls\?*|repos/acme/orchard/releases\?*) echo '[]' ;;
  repos/acme/orchard/commits/*/check-runs\?*) echo '{"total_count":0,"check_runs":[]}' ;;
  repos/acme/orchard/commits/*/status) echo '{"state":"pending","statuses":[]}' ;;
  *) echo "unexpected GitHub request: $6" >&2; exit 1 ;;
esac
`)
	writeFakeWorker(t, filepath.Join(bin, "git"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name, configured, observed, want string
		recovery, managed                bool
	}{
		{"first inventory", "", "", "main", false, false},
		{"changed default", "", "old-default", "main", false, false},
		{"explicit branch", "stable", "main", "stable", false, false},
		{"recovery inventory never starts a repair agent", "", "", "main", true, false},
		{"managed placement keeps inventory local without repair", "", "", "main", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testStore(t, false)
			x := addTown(t, store)
			update(t, store, func(st *State) {
				st.Towns[x.ID].Config.Branch = tc.configured
				st.Towns[x.ID].DefaultBranch = tc.observed
				if tc.managed {
					st.Towns[x.ID].Config.BotExecution = map[Role]mjolnir.Selection{Repo: {Target: "builder", Profile: "coder"}}
					st.Towns[x.ID].Config.Agent.Command = []string{"agent-must-not-start"}
				}
				if tc.recovery {
					st.Towns[x.ID].Workers[Repo].Recovery = &WorkerRecovery{Detail: recoveryDetail(Repo, "")}
					st.Towns[x.ID].Config.Agent.Command = []string{"agent-must-not-start"}
				}
			})
			x = store.Snapshot().Towns[x.ID]
			workers := &BotWorkers{Root: t.TempDir(), Store: store, botCommands: map[Role]string{Repo: worker}}
			t.Cleanup(workers.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			result, err := workers.Observe(ctx, x, InventoryRequest{Health: tc.recovery || tc.managed}, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if result.Inventory == nil || result.Inventory.Branch != tc.want || result.Inventory.Head != baseSHA {
				t.Fatalf("inventory = %+v", result.Inventory)
			}
			if tc.name == "first inventory" {
				since := time.Now().Add(-time.Hour)
				delta, e := workers.Observe(ctx, x, InventoryRequest{Since: &since, Branch: "main"}, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
				if e != nil || delta.Inventory == nil || !delta.Inventory.Incremental || delta.Inventory.StartedAt.IsZero() {
					t.Fatalf("incremental protocol result: %+v, %v", delta, e)
				}
			}
			update(t, store, func(st *State) { Reconcile(st, st.Towns[x.ID], *result.Inventory, time.Now()) })
			x = store.Snapshot().Towns[x.ID]
			if !x.Initialized || x.Branch() != tc.want || x.Config.Branch != tc.configured {
				t.Fatalf("inventory did not initialize the town with the selected branch: %+v", x.Config)
			}
			if tc.managed && (x.Workers[Repo].Run.Mode != "inventory" || x.Workers[Repo].Run.Execution != nil) {
				t.Fatal("managed selection changed read-only inventory", x.Workers[Repo].Run)
			}
			if tc.recovery && (x.Workers[Repo].Run.Mode != "inventory" || x.Workers[Repo].Recovery == nil) {
				t.Fatalf("recovery changed repair authority: %+v", x.Workers[Repo])
			}
		})
	}
}

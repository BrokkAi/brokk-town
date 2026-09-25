package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/harness"
	"github.com/BrokkAi/brokk-town/internal/town"
)

// checkFlags rejects any flag the command does not take, so a mistyped or
// misplaced flag fails instead of being silently ignored.
func checkFlags(fs *flag.FlagSet, cmd *commandInfo, explicit bool) error {
	var stray []string
	fs.Visit(func(f *flag.Flag) {
		if isGlobalFlag(f.Name) {
			return
		}
		for _, name := range cmd.flags {
			if name == f.Name {
				return
			}
		}
		if len(f.Name) == 1 {
			stray = append(stray, "-"+f.Name)
		} else {
			stray = append(stray, "--"+f.Name)
		}
	})
	if len(stray) == 0 {
		return nil
	}
	where, help := "bt "+cmd.name, "bt "+cmd.name+" --help"
	if !explicit {
		where, help = "bt", "bt --help"
	}
	return fmt.Errorf("%s: not a flag of %s\nRun '%s' for usage", strings.Join(stray, ", "), where, help)
}

// statusView is the part of the town state the status summary reads.
type statusView struct {
	Demo     bool `json:"demo"`
	Capacity *struct {
		Active int `json:"active"`
		Limit  int `json:"limit"`
	} `json:"capacity"`
	Towns map[string]struct {
		Deleted     bool                   `json:"deleted"`
		Workers     map[string]town.Worker `json:"workers"`
		Error       string                 `json:"error"`
		Diagnostics *town.DiagnosticReport `json:"diagnostics"`
		Tasks       map[string]*town.Task  `json:"tasks"`
	} `json:"towns"`
}

// printStatus summarizes a running Town, or says it is stopped. With asJSON it
// prints the full state, which needs a running Town.
func printStatus(ctx context.Context, dir string, asJSON bool) error {
	conn, alive := serviceAlive(ctx, dir)
	if asJSON {
		if !alive {
			return errors.New("Town is not running; start bt or bt -d")
		}
		var state any
		if err := request(ctx, conn, "GET", "/api/state", nil, &state); err != nil {
			return err
		}
		b, _ := json.MarshalIndent(state, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if !alive {
		fmt.Println("Town is stopped")
		return nil
	}
	var state statusView
	if err := request(ctx, conn, "GET", "/api/state", nil, &state); err != nil {
		return err
	}
	fmt.Printf("Town %s running (pid %d at %s)\n", conn.Version, conn.PID, conn.URL)
	if state.Demo {
		fmt.Println("Demo: simulated events only")
	}
	if state.Capacity != nil {
		fmt.Printf("Capacity: %d active · limit %d\n", state.Capacity.Active, state.Capacity.Limit)
	}
	var ids []string
	for id, t := range state.Towns {
		if !t.Deleted {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		fmt.Println("No towns; add one with bt add --repo OWNER/REPO")
	}
	for _, id := range ids {
		t := state.Towns[id]
		on := 0
		for _, w := range t.Workers {
			if w.Enabled {
				on++
			}
		}
		line := fmt.Sprintf("  %s  %d/%d houses on", id, on, len(t.Workers))
		repo := t.Workers["repo"]
		if repo.Recovery != nil {
			line += "  recovery: " + repo.Recovery.Detail
			if repo.Error != "" {
				line += "  error: " + repo.Error
			}
		} else if repo.Status == "working" {
			line += "  Reading repository…"
		} else if t.Error != "" {
			line += "  error: " + t.Error
		}
		fmt.Println(line)
		if t.Diagnostics != nil {
			printDiagnostics(id, t.Diagnostics)
		}
		taskIDs := make([]string, 0, len(t.Tasks))
		for taskID, task := range t.Tasks {
			if task.MergeWait != nil && task.Stage == "ready" {
				taskIDs = append(taskIDs, taskID)
			}
		}
		sort.Strings(taskIDs)
		for _, taskID := range taskIDs {
			printDiagnostics(id+" "+taskID, t.Tasks[taskID].MergeWait)
		}
	}
	if logs := filepath.Join(dir, "logs"); dirExists(logs) {
		fmt.Println("Logs:", logs)
	}
	return nil
}

func printDiagnostics(label string, report *town.DiagnosticReport) {
	fmt.Printf("  %s · checked %s\n", label, report.At.Format(time.RFC3339))
	if report.Head != "" {
		fmt.Printf("    Head %s · base %s\n", report.Head, report.Base)
	}
	for _, check := range report.Checks {
		role := ""
		if check.Role != "" {
			role = string(check.Role) + " / "
		}
		fmt.Printf("    %s%s [%s] %s\n", role, check.Code, check.Status, check.Detail)
		if check.Action != "" {
			fmt.Printf("      Next: %s\n", check.Action)
		}
		if check.URL != "" {
			fmt.Printf("      %s\n", check.URL)
		}
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// listHarnesses prints the ACP registry catalog. A running Town answers, so a
// refresh updates the catalog it uses; otherwise the CLI reads, and refreshes,
// the same cache the next Town start loads.
func listHarnesses(ctx context.Context, dir string, demo, refresh bool) error {
	var catalog harness.Listing
	if conn, alive := serviceAlive(ctx, dir); alive {
		method, path := "GET", "/api/harnesses"
		var body any
		if refresh {
			method, path, body = "POST", "/api/harnesses/refresh", map[string]any{}
		}
		if err := request(ctx, conn, method, path, body, &catalog); err != nil {
			return err
		}
	} else {
		local := harness.New(filepath.Join(dir, "harnesses"), demo)
		if refresh {
			if err := local.Refresh(ctx); err != nil {
				return err
			}
		}
		catalog = local.List()
	}
	fmt.Println("Official ACP registry:", catalog.Source)
	if catalog.Demo {
		fmt.Println("Demo uses the bundled registry offline.")
	} else if catalog.Stale {
		fmt.Println("Using a bundled or cached catalog; run bt harnesses --refresh to update.")
	}
	for _, a := range catalog.Agents {
		availability := ""
		if !a.Available {
			availability = " [unavailable on this platform]"
		}
		fmt.Printf("%-25s %-16s %s (%s)%s\n", a.ID, a.Version, a.Name, a.Source, availability)
	}
	fmt.Println("custom — supply an ACP command with --agent-command")
	return nil
}

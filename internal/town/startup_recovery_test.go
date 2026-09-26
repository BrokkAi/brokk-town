package town

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Matches the saved failure found locally: the optional branch was rejected,
// and later canceled inventories left that error beside a repair recovery hold.
func legacyBranchFailure(t *testing.T, dir string, enabled, recovery bool) *Town {
	t.Helper()
	st := NewState(false)
	x, err := st.Add(DefaultConfig("acme/orchard"))
	if err != nil {
		t.Fatal(err)
	}
	x.Error = "invalid branch"
	w := x.Workers[Repo]
	w.Enabled, w.Status, w.Phase = enabled, "failed", "inventorying"
	w.Error, w.Task = "invalid branch", workPausedPrefix+"invalid branch"
	w.Next = time.Now().Add(time.Hour)
	w.Logs = []Log{{At: time.Now().Add(-48 * time.Hour).Round(0), Level: "ERROR", Text: "worker attempt failed · error=invalid branch"}}
	if recovery {
		w.Error = ""
		w.Recovery = &WorkerRecovery{Started: time.Now().Add(-24 * time.Hour).Round(0), Detail: recoveryDetail(Repo, "")}
		w.Task = w.Recovery.Detail
	}
	return writeStartupState(t, dir, st).Towns[x.ID]
}

func writeStartupState(t *testing.T, dir string, st State) State {
	t.Helper()
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// Compare recovery against the persisted evidence. JSON timestamps retain
	// the instant and offset, but not Go's local-zone or monotonic metadata.
	// In particular, a UTC host's time.Local decodes as time.UTC.
	var saved State
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestOpenRetiresLegacyDefaultBranchFailure(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, recovery := range []bool{true, false} {
			name := map[bool]string{true: "enabled", false: "paused"}[enabled] + "/" + map[bool]string{true: "repair hold", false: "failed validation"}[recovery]
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				before := legacyBranchFailure(t, dir, enabled, recovery)
				s, err := Open(dir, false)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				check := func(x *Town) {
					t.Helper()
					w := x.Workers[Repo]
					if x.Error != "" || w.Error != "" {
						t.Fatalf("upgrade retained obsolete failure: town=%q worker=%q", x.Error, w.Error)
					}
					if x.Initialized || x.Branch() != "" || x.Head != "" {
						t.Fatal("startup repair invented a successful inventory")
					}
					if !reflect.DeepEqual(w.Recovery, before.Workers[Repo].Recovery) || !reflect.DeepEqual(w.Logs, before.Workers[Repo].Logs) {
						t.Fatal("startup repair changed recovery or historical evidence")
					}
					if w.Enabled != enabled || (!enabled && w.Status != "paused") || (enabled && !w.Next.IsZero()) {
						t.Fatalf("startup repair changed pause or retained retry delay: %+v", w)
					}
					if !recovery && w.Task == workPausedPrefix+"invalid branch" {
						t.Fatal("obsolete failure still describes current work")
					}
					for role, worker := range x.Workers {
						if role != Repo && worker.Enabled {
							t.Fatal("startup repair enabled agent work")
						}
					}
				}
				check(s.Snapshot().Towns[before.ID])
				// No scheduler or network has run: the startup repair is already durable.
				data, err := os.ReadFile(s.path)
				if err != nil {
					t.Fatal(err)
				}
				var saved State
				if err := json.Unmarshal(data, &saved); err != nil {
					t.Fatal(err)
				}
				check(saved.Towns[before.ID])
				if eligible, _ := s.dispatchEligibility(before.ID, Repo, time.Now()); eligible != enabled {
					t.Fatal("inventory is not immediately eligible after upgrade")
				}
				s.Close()
				reopened, err := Open(dir, false)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				check(reopened.Snapshot().Towns[before.ID])
				again, err := os.ReadFile(reopened.path)
				if err != nil || !bytes.Equal(data, again) {
					t.Fatal("second startup repeated the migration", err)
				}
			})
		}
	}
}

func TestOpenKeepsOtherRepositoryFailures(t *testing.T) {
	for _, scenario := range []string{"pinned branch", "initialized", "known default", "different error", "deleted", "invalid configured branch"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			before := legacyBranchFailure(t, dir, true, false)
			st := NewState(false)
			st.Towns[before.ID] = before
			switch scenario {
			case "pinned branch":
				before.Config.Branch = "main"
			case "initialized":
				before.Initialized = true
			case "known default":
				before.DefaultBranch = "main"
			case "different error":
				before.Error, before.Workers[Repo].Error = "GitHub unavailable", "GitHub unavailable"
			case "deleted":
				before.Deleted = true
			case "invalid configured branch":
				before.Config.Branch = "../bad"
			}
			writeStartupState(t, dir, st)
			s, err := Open(dir, false)
			if scenario == "invalid configured branch" {
				if err == nil {
					s.Close()
					t.Fatal("invalid configuration was silently repaired")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			x := s.Snapshot().Towns[before.ID]
			if x.Error != before.Error || x.Workers[Repo].Error != before.Workers[Repo].Error {
				t.Fatal("startup repair hid a different failure")
			}
		})
	}
}

type startupInventoryFailure struct {
	workerFunc
	err error
}

func (w startupInventoryFailure) Observe(context.Context, *Town, InventoryRequest, func(Progress), *slog.Logger) (RunResult, error) {
	return RunResult{}, w.err
}

func TestStartupRecoverySurvivesCanceledInventoryAndReportsNewFailure(t *testing.T) {
	for _, failure := range []error{context.Canceled, errors.New("GitHub unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			dir := t.TempDir()
			before := legacyBranchFailure(t, dir, true, true)
			s, err := Open(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			sup := NewSupervisor(s, newGH(1), startupInventoryFailure{err: failure})
			sup.schedule(t.Context())
			sup.wg.Wait()
			wantError := ""
			if !errors.Is(failure, context.Canceled) {
				wantError = failure.Error()
			}
			check := func(x *Town) {
				t.Helper()
				if x.Initialized || x.Error != wantError || x.Workers[Repo].Error != wantError {
					t.Fatalf("failed inventory: initialized=%v error=%q worker error=%q", x.Initialized, x.Error, x.Workers[Repo].Error)
				}
				if !reflect.DeepEqual(x.Workers[Repo].Recovery, before.Workers[Repo].Recovery) {
					t.Fatal("failed inventory changed repair uncertainty")
				}
			}
			check(s.Snapshot().Towns[before.ID])
			s.Close()
			reopened, err := Open(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			check(reopened.Snapshot().Towns[before.ID])
		})
	}
}

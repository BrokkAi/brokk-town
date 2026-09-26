package mjolnir

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func runPlan(t *testing.T) RunPlan {
	t.Helper()
	data, _ := json.Marshal(runtimeFields())
	runtime, err := readRuntimeReceipt(data)
	if err != nil {
		t.Fatal(err)
	}
	return RunPlan{ID: "run-123", Repository: "acme/app", Selection: Selection{"target", "profile"}, Placement: Placement{Workspace{"workspace", "Town acme/app"}, "bundle", "project"}, Checkout: launchCheckout(), Runtime: RuntimePin{launchSession(), *runtime}}
}

type runCalls struct {
	create, destroy, workspace atomic.Int32
	deleted                    atomic.Bool
	runtimeChanged             atomic.Bool
}

func runsFixture(t *testing.T, scenario string) (*Runs, RunPlan, *runCalls) {
	t.Helper()
	plan, calls := runPlan(t), &runCalls{}
	directory := t.TempDir()
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		artifactHeaders(w, "application/json")
		switch r.URL.Path {
		case "/api/v1/options":
			fmt.Fprint(w, `{"revision":1,"profiles":[{"id":"profile","harness":"codex"}],"targets":[{"id":"target","kind":"ssh-bare","availability":"ready"}],"bundles":[{"id":"bundle","primary_repository":"project","repositories":[{"id":"project","github":"acme/app","destination":"app"}]}]}`)
		case "/api/v1/workspaces":
			if r.Method == "POST" {
				calls.workspace.Add(1)
				t.Error("created a per-run workspace")
			}
			json.NewEncoder(w).Encode(map[string]any{"workspaces": []Workspace{plan.Placement.Workspace}})
		case "/api/v1/sessions":
			calls.create.Add(1)
			data, err := os.ReadFile(filepath.Join(directory, plan.ID, "run.json"))
			var intent RunRecord
			if err != nil || json.Unmarshal(data, &intent) != nil || intent.State != "creating" || intent.Plan.Checkout != plan.Checkout {
				t.Error("session created before durable exact-revision intent")
			}
			var request map[string]json.RawMessage
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&request) != nil || len(request["prompt"]) != 0 || len(request["project_directory"]) != 0 || len(request["launch_base"]) != 0 {
				t.Error("unsafe creation request")
			}
			var checkout ExactCheckout
			if json.Unmarshal(request["checkout"], &checkout) != nil || checkout != plan.Checkout || string(request["expected_runtime_identity"]) != `"`+selectedRuntime+`"` {
				t.Error("lost checkout or runtime launch constraint")
			}
			w.WriteHeader(201)
			if scenario == "lost create" {
				fmt.Fprint(w, `{"session_id":`)
				return
			}
			fmt.Fprint(w, `{"session_id":"session","turn_id":null}`)
		case "/api/v1/sessions/session":
			if calls.deleted.Load() {
				w.WriteHeader(404)
				return
			}
			fields := launchFields()
			if calls.runtimeChanged.Load() {
				fields["runtime"].(map[string]any)["id"] = "changed-after-evidence"
			}
			switch scenario {
			case "wrong checkout":
				fields["checkout"] = ExactCheckout{"project", evidenceHead, "town/run-123"}
			case "wrong runtime":
				fields["runtime"].(map[string]any)["id"] = "upgraded"
			case "missing guard":
				delete(fields, "expected_runtime_identity")
			case "failed":
				fields["lifecycle"] = "failed"
			}
			json.NewEncoder(w).Encode(fields)
		case "/api/v1/sessions/session/diff":
			if r.URL.Query().Get("base") != evidenceBase {
				t.Error("used mutable checkout revision")
			}
			head, patch := evidenceBase, ""
			if scenario == "advanced head" {
				head = evidenceHead
			}
			if scenario == "dirty tree" {
				patch = "private-patch"
			}
			json.NewEncoder(w).Encode(map[string]any{"base": evidenceBase, "head": head, "diff": patch})
		case "/api/v1/sessions/session/destroy":
			calls.destroy.Add(1)
			data, err := os.ReadFile(filepath.Join(directory, plan.ID, "run.json"))
			var intent RunRecord
			if err != nil || json.Unmarshal(data, &intent) != nil || intent.State != "destroying" || intent.Evidence == "" {
				t.Error("cleanup started before durable evidence and intent")
			}
			calls.deleted.Store(true)
			if scenario == "lost cleanup" {
				w.WriteHeader(500)
				fmt.Fprint(w, "private-token")
				return
			}
			w.WriteHeader(202)
		default:
			t.Errorf("unexpected operation: %s %s", r.Method, r.URL)
		}
	})
	return &Runs{c, directory}, plan, calls
}

func TestManagedCheckoutDurableIntentExactEvidenceAndConfirmedCleanup(t *testing.T) {
	rs, plan, calls := runsFixture(t, "success")
	run, err := rs.Prepare(t.Context(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Record().State != "ready" || run.Record().Receipt.Runtime.ID != selectedRuntime {
		t.Fatal("missing ready receipt")
	}
	*plan.Runtime.Runtime.Components[0].Version = "changed-by-settings"
	if *run.Record().Plan.Runtime.Runtime.Components[0].Version == "changed-by-settings" {
		t.Fatal("dispatch retained mutable configuration")
	}
	if err := run.Destroy(t.Context()); err == nil || calls.destroy.Load() != 0 {
		t.Fatal("cleaned up without evidence")
	}
	if err := run.SaveEvidence([]byte(`{"result":"private-evidence"}`)); err == nil {
		t.Fatal("accepted evidence without a prompt")
	}
	if err := run.MarkPrompting(); err != nil {
		t.Fatal(err)
	}
	if err := run.MarkPrompting(); err == nil {
		t.Fatal("allowed a duplicate prompt")
	}
	if err := run.SaveEvidence([]byte(`{"result":"private-evidence"}`)); err != nil {
		t.Fatal(err)
	}
	if err := run.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted := Runs{rs.Catalog, rs.Directory}
	records, err := restarted.Records()
	if err != nil || len(records) != 1 || records[0].State != "destroyed" || calls.create.Load() != 1 || calls.destroy.Load() != 1 || calls.workspace.Load() != 0 {
		t.Fatalf("wrong retained lifecycle: %+v %v", records, err)
	}
	data, _ := json.Marshal(records)
	if strings.Contains(string(data), "private-") {
		t.Fatal("private evidence entered run projection")
	}
	if _, err := restarted.Prepare(t.Context(), plan, nil); err == nil || calls.create.Load() != 1 {
		t.Fatal("resubmitted a retained run")
	}
}

func TestManagedCheckoutRefusesUnavailableOrChangedEvidenceWithoutRetry(t *testing.T) {
	for _, scenario := range []string{"lost create", "wrong checkout", "wrong runtime", "missing guard", "failed", "advanced head", "dirty tree"} {
		t.Run(scenario, func(t *testing.T) {
			rs, plan, calls := runsFixture(t, scenario)
			run, err := rs.Prepare(t.Context(), plan, nil)
			if err == nil || run == nil || (run.Record().State != "uncertain" && run.Record().State != "held") {
				t.Fatalf("accepted uncertain checkout: %v", err)
			}
			if run.MarkPrompting() == nil || run.Destroy(t.Context()) == nil {
				t.Fatal("held checkout permitted work or cleanup")
			}
			restarted := Runs{rs.Catalog, rs.Directory}
			records, err := restarted.Records()
			if err != nil || len(records) != 1 {
				t.Fatalf("lost interrupted evidence: %v", err)
			}
			if scenario == "lost create" && records[0].Session != "" {
				t.Fatal("invented session identity")
			}
			if _, err := restarted.Prepare(t.Context(), plan, nil); err == nil || calls.create.Load() != 1 || calls.destroy.Load() != 0 {
				t.Fatal("retried an uncertain creation or cleaned up evidence")
			}
		})
	}
}

func TestLostManagedCleanupReconcilesOnlyByReadingItsSavedSession(t *testing.T) {
	rs, plan, calls := runsFixture(t, "lost cleanup")
	run, err := rs.Prepare(t.Context(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.MarkPrompting(); err != nil {
		t.Fatal(err)
	}
	if err := run.SaveEvidence([]byte(`{"complete":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := run.Destroy(t.Context()); err == nil || strings.Contains(err.Error(), "private-token") || run.Record().State != "uncertain_cleanup" {
		t.Fatalf("lost cleanup was accepted: %v", err)
	}
	if err := run.Destroy(t.Context()); err == nil || calls.destroy.Load() != 1 {
		t.Fatal("retried an uncertain destroy")
	}
	restarted := Runs{rs.Catalog, rs.Directory}
	if err := restarted.ReconcileCleanup(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	records, err := restarted.Records()
	if err != nil || records[0].State != "destroyed" || calls.destroy.Load() != 1 {
		t.Fatalf("cleanup was not read-only: %v", err)
	}
}

func TestManagedCheckoutCancellationAndACPUseSameIntent(t *testing.T) {
	rs, plan, calls := runsFixture(t, "success")
	ctx, cancel := context.WithCancel(t.Context())
	run, err := rs.Prepare(ctx, plan, func(ctx context.Context, got RunPlan) (string, error) {
		if got.Checkout != plan.Checkout {
			t.Fatal("ACP received a different revision")
		}
		data, err := os.ReadFile(filepath.Join(rs.Directory, plan.ID, "run.json"))
		if err != nil || !strings.Contains(string(data), `"state":"creating"`) {
			t.Fatal("ACP ran without a saved intent")
		}
		cancel()
		return "", ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || run == nil || run.Record().State != "uncertain" || calls.create.Load() != 0 {
		t.Fatal("lost ACP creation treated as retryable")
	}
	if _, err := rs.Prepare(t.Context(), plan, nil); err == nil || calls.create.Load() != 0 {
		t.Fatal("retried canceled ACP session")
	}
}

func TestManagedCheckoutRetainsEvidenceWhenFilesOrConnectionChange(t *testing.T) {
	rs, plan, calls := runsFixture(t, "success")
	run, err := rs.Prepare(t.Context(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.MarkPrompting(); err != nil {
		t.Fatal(err)
	}
	if err := run.SaveEvidence([]byte(`{"complete":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rs.Directory, plan.ID, "evidence.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if run.Destroy(t.Context()) == nil || calls.destroy.Load() != 0 {
		t.Fatal("cleaned up changed evidence")
	}
	if _, err := rs.Records(); err == nil {
		t.Fatal("accepted changed evidence after restart")
	}
	other := New(t.TempDir(), false, Connection{URL: rs.Catalog.connection.URL, TokenFile: rs.Catalog.connection.TokenFile + "-other"})
	if _, err := (&Runs{other, rs.Directory}).Records(); err == nil {
		t.Fatal("adopted another daemon's session")
	}
}

func TestManagedCleanupRefusesChangedRuntimeEvenWithSameOrdinal(t *testing.T) {
	rs, plan, calls := runsFixture(t, "success")
	run, err := rs.Prepare(t.Context(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.MarkPrompting(); err != nil {
		t.Fatal(err)
	}
	if err := run.SaveEvidence([]byte(`{"complete":true}`)); err != nil {
		t.Fatal(err)
	}
	calls.runtimeChanged.Store(true)
	if err := run.Destroy(t.Context()); err == nil || calls.destroy.Load() != 0 {
		t.Fatal("destroyed a session with changed runtime identity")
	}
}

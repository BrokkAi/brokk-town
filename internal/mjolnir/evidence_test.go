package mjolnir

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func evidenceRun(t *testing.T, repair bool, scenario string) (*Run, *atomic.Int32) {
	t.Helper()
	exports, reads := &atomic.Int32{}, &atomic.Int32{}
	plan := runPlan(t)
	dir := t.TempDir()
	c, _ := catalogFixture(t, func(w http.ResponseWriter, req *http.Request) {
		artifactHeaders(w, "application/json")
		switch req.URL.Path {
		case "/api/v1/sessions/session":
			f := launchFields()
			if scenario == "reinitialized" {
				f["runtime"].(map[string]any)["event_ordinal"] = 100
			}
			json.NewEncoder(w).Encode(f)
		case "/api/v1/sessions/session/diff":
			base, head, patch, ancestry := req.URL.Query().Get("base"), evidenceBase, "", true
			if repair {
				head = evidenceHead
				if base == evidenceBase {
					patch = "private-patch"
				}
			}
			if scenario == "dirty" {
				patch = "uncommitted"
			}
			if scenario == "rewritten" {
				ancestry = false
			}
			if scenario == "head changed" && exports.Load() > 0 {
				head = strings.Repeat("c", 40)
			}
			if scenario == "missing diff" {
				w.WriteHeader(409)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"base": base, "head": head, "diff": patch, "head_descends_from_base": ancestry})
		case "/api/v1/sessions/session/transcript":
			reads.Add(1)
			text := "REVIEW_RESULT private-answer"
			if scenario == "wrong answer" {
				text = "different"
			}
			items := []map[string]any{}
			if req.URL.Query().Get("after_seq") == "0" {
				items = append(items, map[string]any{"stable_id": "answer", "seq": 2, "position": 1, "role": "agent", "text": text})
			}
			latest := 2
			if scenario == "advanced transcript" && reads.Load() > 1 {
				latest = 3
				items = append(items, map[string]any{"stable_id": "extra", "seq": 3, "position": 2, "role": "user", "text": "new turn"})
			}
			json.NewEncoder(w).Encode(map[string]any{"session_id": "session", "latest_seq": latest, "next_after_seq": latest, "items": items})
		case "/api/v1/sessions/session/export":
			exports.Add(1)
			data, err := os.ReadFile(filepath.Join(dir, "run.json"))
			if err != nil || !strings.Contains(string(data), `"state":"exporting"`) {
				t.Error("export before durable checkpoint intent")
			}
			if scenario == "lost export" {
				w.WriteHeader(500)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "private-bundle")
		default:
			t.Errorf("unexpected evidence operation: %s", req.URL.Path)
		}
	})
	r := &Run{owner: &Runs{Catalog: c, Directory: filepath.Dir(dir)}, directory: dir, record: RunRecord{Version: 1, Connection: c.identity, Plan: plan, State: "ready", Session: "session"}}
	// Use the launch fixture's original initialization as the saved receipt.
	data, _ := json.Marshal(runtimeFields())
	runtime, _ := readRuntimeReceipt(data)
	no := false
	r.record.Receipt = &SessionState{Identity: launchSession(), State: "running", Idle: true, Lifecycle: "live", ChatPhase: "idle", HasError: &no, Checkout: &plan.Checkout, Runtime: runtime, ExpectedRuntimeIdentity: selectedRuntime}
	if err := r.MarkPrompting(); err != nil {
		t.Fatal(err)
	}
	return r, exports
}

func TestRemoteEvidenceBindsAnswerExactTreeAndDurableRepairExport(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(fmt.Sprint(repair), func(t *testing.T) {
			r, exports := evidenceRun(t, repair, "success")
			got, err := r.Collect(t.Context(), repair, "REVIEW_RESULT private-answer")
			if err != nil {
				t.Fatal(err)
			}
			if r.Record().State != "evidence" || got.Head == "" {
				t.Fatal("missing saved evidence")
			}
			if (exports.Load() == 1) != repair || (len(got.Bundle) > 0) != repair {
				t.Fatal("wrong export policy")
			}
			data, err := os.ReadFile(filepath.Join(r.directory, "evidence.json"))
			if err != nil || !strings.Contains(string(data), `"text":"REVIEW_RESULT private-answer"`) {
				t.Fatal("transcript text lost from private evidence")
			}
			public, _ := json.Marshal(r.Record())
			if strings.Contains(string(public), "private-") {
				t.Fatal("private content entered public record")
			}
			if _, err := r.Collect(t.Context(), repair, "REVIEW_RESULT private-answer"); err == nil {
				t.Fatal("collected/exported twice")
			}
			if repair {
				if err := verifySavedEvidence(r.directory, r.Record().Evidence); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(r.directory, "repair.bundle"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if verifySavedEvidence(r.directory, r.Record().Evidence) == nil {
					t.Fatal("accepted changed repair bundle")
				}
			}
		})
	}
}

func TestRemoteEvidenceRefusesMissingChangedAndUncertainArtifacts(t *testing.T) {
	for _, scenario := range []string{"missing diff", "wrong answer", "advanced transcript", "reinitialized", "dirty", "rewritten", "head changed", "lost export"} {
		t.Run(scenario, func(t *testing.T) {
			r, exports := evidenceRun(t, true, scenario)
			if _, err := r.Collect(t.Context(), true, "REVIEW_RESULT private-answer"); err == nil {
				t.Fatal("accepted invalid evidence")
			}
			if r.Record().State == "evidence" || r.Destroy(t.Context()) == nil {
				t.Fatal("allowed cleanup of refused evidence")
			}
			if scenario == "lost export" || scenario == "head changed" {
				if r.Record().State != "exporting" {
					t.Fatal("lost export intent")
				}
				if _, err := r.Collect(t.Context(), true, "REVIEW_RESULT private-answer"); err == nil || exports.Load() != 1 {
					t.Fatal("retried uncertain export")
				}
			}
		})
	}
}

package mjolnir

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const selectedRuntime = "mj-runtime-v1:opaque-comparison-id"

func launchSession() SessionIdentity {
	return SessionIdentity{"session", "workspace", "bundle", Selection{"target", "profile"}}
}

func launchCheckout() ExactCheckout {
	return ExactCheckout{"project", evidenceBase, "town/run-123"}
}

func runtimeFields() map[string]any {
	return map[string]any{
		"id": selectedRuntime, "harness": "codex", "platform": "linux-x86_64", "provenance": "managed_installation",
		"components": []any{
			map[string]any{"name": "acp_bridge", "version": "1.13.3", "sha256": strings.Repeat("a", 64)},
			map[string]any{"name": "provider_cli", "version": "0.156.1", "sha256": nil},
		},
		"unavailable_reason": nil, "event_ordinal": 7, "observed_at_ms": int64(1788000000000),
		"home": "private-path", "environment": "private-token",
	}
}

func launchFields() map[string]any {
	return map[string]any{
		"id": "session", "workspace_id": "workspace", "bundle_id": "bundle", "target_id": "target", "profile_id": "profile",
		"state": "Running", "lifecycle": "live", "chat_phase": "idle", "is_idle": true, "has_error": false,
		"checkout": launchCheckout(), "expected_runtime_identity": selectedRuntime, "runtime": runtimeFields(),
		"error": "private-token", "config_options": "private-path",
	}
}

func TestLaunchReceiptReadsActualVersionsAndRetainsPriorInitialization(t *testing.T) {
	var calls atomic.Int32
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/sessions/session" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Errorf("unexpected launch evidence request: %s %s", r.Method, r.URL)
		}
		fields := launchFields()
		if calls.Add(1) > 1 {
			runtime := fields["runtime"].(map[string]any)
			runtime["id"] = "mj-runtime-v1:replacement"
			runtime["event_ordinal"] = 19
			runtime["observed_at_ms"] = int64(1788000001000)
		}
		artifactHeaders(w, "application/json")
		json.NewEncoder(w).Encode(fields)
	})
	first, err := c.ReadSession(t.Context(), launchSession())
	if err != nil || first.CheckLaunchReceipt(launchCheckout(), selectedRuntime) != nil {
		t.Fatalf("launch receipt: %+v %v", first, err)
	}
	if first.Runtime.Components[0].Version == nil || *first.Runtime.Components[0].Version != "1.13.3" || first.Runtime.Components[1].Version == nil || *first.Runtime.Components[1].Version != "0.156.1" || first.Runtime.Components[1].SHA256 != nil {
		t.Fatal("lost target component versions or converted unknown digest to known")
	}
	saved, err := json.Marshal(first)
	if err != nil || strings.Contains(string(saved), "private-") {
		t.Fatalf("unsafe receipt projection: %s %v", saved, err)
	}
	var retained SessionState
	if err := json.Unmarshal(saved, &retained); err != nil || !reflect.DeepEqual(first, retained) {
		t.Fatalf("receipt cannot be retained across a JSON round trip: %v", err)
	}
	later, err := c.ReadSession(t.Context(), launchSession())
	if err != nil || later.Runtime.EventOrdinal != 19 || later.CheckLaunchReceipt(launchCheckout(), selectedRuntime) == nil {
		t.Fatalf("replacement was accepted against the saved identity: %v", err)
	}
	if first.Runtime.ID != selectedRuntime || first.Runtime.EventOrdinal != 7 || retained.Runtime.ID != selectedRuntime || retained.Runtime.ObservedAtMS != 1788000000000 {
		t.Fatal("later initialization rewrote the earlier receipt")
	}
	// Merely selecting the observed replacement cannot remove the launch guard.
	if later.CheckLaunchReceipt(launchCheckout(), later.Runtime.ID) == nil {
		t.Fatal("accepted replacement without the daemon's matching constraint")
	}
}

func TestLaunchReceiptNeverConfusesIntentOrIdleWithReadiness(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"older daemon", func(m map[string]any) { delete(m, "checkout") }, "checkout selection"},
		{"wrong repository", func(m map[string]any) { v := launchCheckout(); v.Repository = "sibling"; m["checkout"] = v }, "checkout selection"},
		{"wrong revision", func(m map[string]any) { v := launchCheckout(); v.Commit = evidenceHead; m["checkout"] = v }, "checkout selection"},
		{"wrong branch", func(m map[string]any) { v := launchCheckout(); v.Branch = "town/other"; m["checkout"] = v }, "checkout selection"},
		{"unprepared intent", func(m map[string]any) { m["lifecycle"] = "starting" }, "not confirmed ready"},
		{"suspended", func(m map[string]any) { m["lifecycle"] = "suspended" }, "not confirmed ready"},
		{"failed", func(m map[string]any) { m["lifecycle"] = "failed" }, "not confirmed ready"},
		{"unknown lifecycle", func(m map[string]any) { delete(m, "lifecycle") }, "not confirmed ready"},
		{"busy", func(m map[string]any) { m["is_idle"] = false }, "not confirmed ready"},
		{"running chat", func(m map[string]any) { m["chat_phase"] = "running" }, "not confirmed ready"},
		{"unknown chat", func(m map[string]any) { delete(m, "chat_phase") }, "not confirmed ready"},
		{"preparation error", func(m map[string]any) { m["has_error"] = true }, "not confirmed ready"},
		{"unknown error state", func(m map[string]any) { delete(m, "has_error") }, "not confirmed ready"},
		{"uninitialized", func(m map[string]any) { m["runtime"] = nil }, "receipt is missing"},
		{"older worker", func(m map[string]any) { delete(m, "runtime") }, "receipt is missing"},
		{"unknown installation", func(m map[string]any) {
			r := m["runtime"].(map[string]any)
			r["id"] = nil
			r["unavailable_reason"] = "private-path private-token"
			r["provenance"] = "target_installation"
		}, "identity is unavailable"},
		{"changed runtime", func(m map[string]any) { m["runtime"].(map[string]any)["id"] = "replacement" }, "runtime changed"},
		{"unguarded discovery", func(m map[string]any) { delete(m, "expected_runtime_identity") }, "does not enforce"},
		{"wrong guard", func(m map[string]any) { m["expected_runtime_identity"] = "other" }, "does not enforce"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				fields := launchFields()
				scenario.change(fields)
				artifactHeaders(w, "application/json")
				json.NewEncoder(w).Encode(fields)
			})
			got, err := c.ReadSession(t.Context(), launchSession())
			if err != nil {
				t.Fatalf("lost explainable incomplete evidence: %v", err)
			}
			if err := got.CheckLaunchReceipt(launchCheckout(), selectedRuntime); err == nil || !strings.Contains(err.Error(), scenario.want) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("wrong refusal: %v", err)
			}
			data, _ := json.Marshal(got)
			if strings.Contains(string(data), "private-") {
				t.Fatal("private diagnostics enter retained evidence")
			}
		})
	}
}

func TestMalformedLaunchReceiptsReturnNoPartialEvidence(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing ordinal", func(m map[string]any) { delete(m, "event_ordinal") }},
		{"missing time", func(m map[string]any) { delete(m, "observed_at_ms") }},
		{"negative time", func(m map[string]any) { m["observed_at_ms"] = -1 }},
		{"ordinal overflow", func(m map[string]any) { m["event_ordinal"] = json.Number("18446744073709551616") }},
		{"missing components", func(m map[string]any) { delete(m, "components") }},
		{"empty known components", func(m map[string]any) { m["components"] = []any{} }},
		{"unknown provenance", func(m map[string]any) { m["provenance"] = "controller_version" }},
		{"known and unavailable", func(m map[string]any) { m["unavailable_reason"] = "private-token" }},
		{"unexplained unknown", func(m map[string]any) { m["id"] = nil }},
		{"empty identity", func(m map[string]any) { m["id"] = "" }},
		{"unsafe identity", func(m map[string]any) { m["id"] = "private-token\n" }},
		{"nonascii identity", func(m map[string]any) { m["id"] = "é" }},
		{"oversized identity", func(m map[string]any) { m["id"] = strings.Repeat("a", 257) }},
		{"unsafe version", func(m map[string]any) { m["components"].([]any)[0].(map[string]any)["version"] = "private-token\n" }},
		{"invalid digest", func(m map[string]any) { m["components"].([]any)[0].(map[string]any)["sha256"] = "abc" }},
		{"duplicate component", func(m map[string]any) { c := m["components"].([]any); m["components"] = append(c, c[0]) }},
		{"too many components", func(m map[string]any) { m["components"] = make([]RuntimeComponent, 65) }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				fields := launchFields()
				scenario.change(fields["runtime"].(map[string]any))
				artifactHeaders(w, "application/json")
				json.NewEncoder(w).Encode(fields)
			})
			got, err := c.ReadSession(t.Context(), launchSession())
			if err == nil || got != (SessionState{}) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("accepted partial or private evidence: %+v %v", got, err)
			}
		})
	}
}

func TestExactCheckoutRequiresImmutableRevisionAndSafeBranch(t *testing.T) {
	for _, commit := range []string{"HEAD", "main", "abc123", strings.Repeat("0", 40), strings.Repeat("a", 39), strings.Repeat("A", 40)} {
		c := launchCheckout()
		c.Commit = commit
		if c.Validate() == nil {
			t.Errorf("accepted commit %q", commit)
		}
	}
	for _, branch := range []string{"HEAD", "-option", "a..b", "a//b", "a/", "/a", ".hidden/a", "a/.hidden", "a.lock/b", "a.lock", "a.", "a@{1}", "a b", "a:b", "a?b", "a[b", "a\\b", "a\nb"} {
		c := launchCheckout()
		c.Branch = branch
		if c.Validate() == nil {
			t.Errorf("accepted branch %q", branch)
		}
	}
	for _, branch := range []string{"", "town/run-123", "town/review_1"} {
		c := launchCheckout()
		c.Branch = branch
		if err := c.Validate(); err != nil {
			t.Errorf("refused branch %q: %v", branch, err)
		}
	}
	c := launchCheckout()
	c.Commit = strings.Repeat("a", 64)
	if err := c.Validate(); err != nil {
		t.Fatal("refused SHA-256 commit", err)
	}
}

func TestLaunchEvidenceRemainsSeparateFromCurrentCheckout(t *testing.T) {
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		artifactHeaders(w, "application/json")
		if strings.HasSuffix(r.URL.Path, "/diff") {
			json.NewEncoder(w).Encode(map[string]any{"base": evidenceBase, "head": evidenceHead, "diff": "private-patch", "head_descends_from_base": true})
			return
		}
		json.NewEncoder(w).Encode(launchFields())
	})
	session, err := c.ReadSession(t.Context(), launchSession())
	if err != nil || session.CheckLaunchReceipt(launchCheckout(), selectedRuntime) != nil {
		t.Fatalf("launch declarations: %v", err)
	}
	diff, err := c.ReadDiff(t.Context(), "session", evidenceBase)
	if err != nil || diff.CheckReviewTree("session", evidenceBase) == nil {
		t.Fatal("accepted launch intent as proof of current checkout")
	}
}

func TestSessionReceiptReadHonorsCancellationAndDemoIsolation(t *testing.T) {
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.ReadSession(ctx, launchSession()); done <- err }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	demo := New(t.TempDir(), true, c.connection)
	got, err := demo.ReadSession(t.Context(), launchSession())
	if err == nil || got != (SessionState{}) || calls.Load() != 1 {
		t.Fatal("demo read receipt or contacted daemon")
	}
}

func TestSessionReceiptRejectsTransportAndCheckoutFailures(t *testing.T) {
	for _, scenario := range []string{"missing", "refused", "truncated", "oversized", "bad checkout", "bad guard"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				artifactHeaders(w, "application/json")
				fields := launchFields()
				switch scenario {
				case "missing":
					w.WriteHeader(404)
				case "refused":
					w.WriteHeader(409)
				case "truncated":
					w.Header().Set("Content-Length", "100000")
				case "oversized":
					fmt.Fprint(w, strings.Repeat(" ", maxBytes))
				case "bad checkout":
					fields["checkout"] = ExactCheckout{"project", "main", "town/run"}
				case "bad guard":
					fields["expected_runtime_identity"] = "private-token\n"
				}
				json.NewEncoder(w).Encode(fields)
			})
			got, err := c.ReadSession(t.Context(), launchSession())
			if err == nil || got != (SessionState{}) || calls.Load() != 1 || strings.Contains(err.Error(), "private-") {
				t.Fatalf("unsafe receipt failure: %v", err)
			}
		})
	}
}

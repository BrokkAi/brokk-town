package mjolnir

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDiscoverRuntimeRejectsWrongUnknownOrUnreadySession(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong target", "wrong profile", "busy", "failed", "missing", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				fields := launchFields()
				delete(fields, "expected_runtime_identity") // Initial discovery needs no pin.
				switch scenario {
				case "wrong target":
					fields["target_id"] = "other"
				case "wrong profile":
					fields["profile_id"] = "other"
				case "busy":
					fields["is_idle"] = false
				case "failed":
					fields["has_error"] = true
				case "missing":
					delete(fields, "runtime")
				case "unknown":
					fields["runtime"].(map[string]any)["id"] = nil
					fields["runtime"].(map[string]any)["unavailable_reason"] = "missing provenance"
				}
				artifactHeaders(w, "application/json")
				json.NewEncoder(w).Encode(fields)
			})
			pin, err := c.DiscoverRuntime(t.Context(), Selection{"target", "profile"}, "session")
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("discovery: %v", err)
			}
			if err == nil && (pin.Validate() != nil || pin.Runtime.ID != selectedRuntime) {
				t.Fatal("lost runtime evidence")
			}
		})
	}
}

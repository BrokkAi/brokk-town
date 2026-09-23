package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestQuietHoursAPIsValidatePersistAndProject(t *testing.T) {
	s, h := fixture(t)
	if r := call(t, h.URL, "POST", "/api/quiet-hours", `{"windows":[]}`, "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing quiet-hours authorization: %d", r.StatusCode)
	}
	for _, body := range []string{
		`{"windows":[{"days":["mon"],"start":"18:00","end":"18:00"}]}`,
		`{"windows":[{"days":["someday"],"start":"18:00","end":"08:00"}]}`,
		`{"windows":[{"days":["mon"],"start":"18:00","end":"08:00","tz":"UTC"}]}`,
		`{"windows":[],"injected":true}`,
		`{}`,
		`{"windows":null}`,
	} {
		if r := call(t, h.URL, "POST", "/api/quiet-hours", body, "test-key", ""); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("quiet-hours body %s got %d", body, r.StatusCode)
		}
	}
	r := call(t, h.URL, "POST", "/api/quiet-hours", `{"windows":[{"days":["mon","tue"],"start":"18:00","end":"08:00"}]}`, "test-key", "")
	if r.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(r.Body)
		t.Fatalf("valid service default rejected: %s", data)
	}
	if got := s.Store.Snapshot().ServiceConfig.QuietHours; len(got) != 1 || got[0].End != "08:00" {
		t.Fatalf("service default not saved: %+v", got)
	}
	// Capacity edits leave the quiet schedule alone and refuse to carry one.
	if r := call(t, h.URL, "POST", "/api/capacity", `{"max_workers":3,"quiet_hours":[]}`, "test-key", ""); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("capacity accepted a quiet schedule: %d", r.StatusCode)
	}
	if r := call(t, h.URL, "POST", "/api/capacity", `{"max_workers":3}`, "test-key", ""); r.StatusCode != http.StatusOK {
		t.Fatal(r.Status)
	}
	if len(s.Store.Snapshot().ServiceConfig.QuietHours) != 1 {
		t.Fatal("a capacity edit cleared the quiet schedule")
	}

	if r := call(t, h.URL, "POST", "/api/towns", `{"repo":"acme/quiet","agent":{"harness":"custom","command":["fake"]}}`, "test-key", ""); r.StatusCode != http.StatusCreated {
		t.Fatal(r.Status)
	}
	r = call(t, h.URL, "POST", "/api/settings", `{"town":"acme/quiet","agent":{},"quiet_hours":{"windows":[{"days":["sat"],"start":"25:00","end":"01:00"}]}}`, "test-key", "")
	data, _ := io.ReadAll(r.Body)
	if r.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "quiet window 1: start") {
		t.Fatalf("invalid town window: %d %s", r.StatusCode, data)
	}
	if r := call(t, h.URL, "POST", "/api/settings", `{"town":"acme/quiet","agent":{},"quiet_hours":{"windows":[]}}`, "test-key", ""); r.StatusCode != http.StatusOK {
		t.Fatal(r.Status)
	}
	if got := s.Store.Snapshot().Towns["acme/quiet"].Config.QuietHours; got == nil || len(*got) != 0 {
		t.Fatalf("opt-out not saved: %v", got)
	}
	r = call(t, h.URL, "GET", "/api/state", "", "test-key", "")
	var state struct {
		ServiceConfig struct {
			QuietHours []town.QuietWindow `json:"quiet_hours"`
		} `json:"service_config"`
		Towns map[string]struct {
			Quiet  town.QuietState `json:"quiet_hours"`
			Config struct {
				QuietHours *[]town.QuietWindow `json:"quiet_hours"`
			} `json:"config"`
		} `json:"towns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	projected := state.Towns["acme/quiet"]
	if len(state.ServiceConfig.QuietHours) != 1 || projected.Config.QuietHours == nil || projected.Quiet.Source != "" || projected.Quiet.Active {
		t.Fatalf("public quiet hours = %+v", state)
	}
	if r := call(t, h.URL, "POST", "/api/settings", `{"town":"acme/quiet","agent":{},"quiet_hours":{"windows":null}}`, "test-key", ""); r.StatusCode != http.StatusOK {
		t.Fatal(r.Status)
	}
	if s.Store.Snapshot().Towns["acme/quiet"].Config.QuietHours != nil {
		t.Fatal("a null schedule did not restore the service default")
	}
}

package web

import (
	"encoding/json"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestDiagnosticsAPIIsAuthenticatedStrictAndSavesDemoResult(t *testing.T) {
	s, h := fixtureMode(t, true)
	if err := s.Store.Update(func(st *town.State) error { _, err := st.Add(town.DefaultConfig("acme/app")); return err }); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{`{"town":"acme/app"}`, "", 401},
		{`{"town":"missing/app"}`, "test-key", 400},
		{`{"town":"acme/app","execute":true}`, "test-key", 400},
		{`{"town":"acme/app"}`, "test-key", 200},
	} {
		resp := call(t, h.URL, "POST", "/api/diagnostics", tc.body, tc.token, "")
		if resp.StatusCode != tc.status {
			t.Fatalf("got %d want %d", resp.StatusCode, tc.status)
		}
		if tc.status == 200 {
			var report town.DiagnosticReport
			if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
				t.Fatal(err)
			}
			if len(report.Checks) != 1 || report.Checks[0].Code != "demo" || report.Checks[0].Status != "unknown" {
				t.Fatal(report)
			}
		}
		resp.Body.Close()
	}
	if s.Store.Snapshot().Towns["acme/app"].Diagnostics == nil {
		t.Fatal("diagnostic not in shared state")
	}
}

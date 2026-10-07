package main

import (
	"strings"
	"testing"
)

func TestConfigFileCarriesQuietHours(t *testing.T) {
	entries, service, err := decodeConfigFile([]byte(`{"quiet_hours":[{"days":["sat","sun"],"start":"00:00","end":"24:00"}],"towns":[{"repo":"acme/team","harness":"custom","agent":{"command":["fake"]},"merge_policy":"bot","poll_seconds":60,"report_seconds":60,"max_cycles":1,"quiet_hours":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if service.QuietHours == nil || len(*service.QuietHours) != 1 || service.MaxWorkers != nil {
		t.Fatalf("service quiet hours = %+v", service)
	}
	if q := entries[0].Config.QuietHours; q == nil || len(*q) != 0 {
		t.Fatalf("a town's opt-out was lost: %v", q)
	}
	for raw, want := range map[string]string{
		`{"quiet_hours":null,"towns":[]}`:                                                          "cannot be null",
		`{"quiet_hours":[{"days":["mon"],"start":"18:00","end":"18:00"}],"towns":[]}`:              "start and end are both",
		`{"quiet_hours":[{"days":["mon"],"start":"18:00","end":"19:00","zone":"UTC"}],"towns":[]}`: "unknown field",
	} {
		if _, _, err := decodeConfigFile([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", raw, err, want)
		}
	}
	// A town window is checked by the same validation as every other setting.
	bad, _, err := decodeConfigFile([]byte(`{"towns":[{"repo":"acme/team","harness":"custom","agent":{"command":["fake"]},"merge_policy":"bot","poll_seconds":60,"report_seconds":60,"max_cycles":1,"quiet_hours":[{"days":["mon"],"start":"9:00","end":"10:00"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := bad[0].Config.Validate(); err == nil || !strings.Contains(err.Error(), "quiet window 1") {
		t.Fatalf("an invalid town window validated: %v", err)
	}
}

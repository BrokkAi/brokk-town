package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type quietCall struct {
	path string
	body map[string]any
}

func quietService(t *testing.T) (string, chan quietCall) {
	t.Helper()
	received := make(chan quietCall, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" && r.Method == "GET" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- quietCall{r.URL.Path, body}
		if r.URL.Path == "/api/quiet-hours" {
			_, _ = w.Write([]byte(`{"max_workers":4,"quiet_hours":[{"days":["mon","tue","wed","thu","fri"],"start":"18:00","end":"08:00"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(h.Close)
	dir := t.TempDir()
	conn, _ := json.Marshal(connection{URL: h.URL, Token: "test-key", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), conn, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, received
}

func TestQuietHoursCLIEditsATownOrTheServiceDefault(t *testing.T) {
	dir, received := quietService(t)
	town := []string{"settings", "--state-dir", dir, "--repo", "Acme/Team", "--quiet-hours"}

	if err := run(context.Background(), append(append([]string{}, town...), "weekdays 18:00-08:00; weekends 00:00-24:00")); err != nil {
		t.Fatal(err)
	}
	call := <-received
	windows := call.body["quiet_hours"].(map[string]any)["windows"].([]any)
	if call.path != "/api/settings" || len(windows) != 2 || windows[0].(map[string]any)["start"] != "18:00" || len(windows[1].(map[string]any)["days"].([]any)) != 2 {
		t.Fatalf("town schedule payload = %+v", call)
	}

	if err := run(context.Background(), append(append([]string{}, town...), "none")); err != nil {
		t.Fatal(err)
	}
	if got := (<-received).body["quiet_hours"].(map[string]any)["windows"].([]any); len(got) != 0 {
		t.Fatalf("none did not send an empty schedule: %v", got)
	}

	if err := run(context.Background(), append(append([]string{}, town...), "default")); err != nil {
		t.Fatal(err)
	}
	edit := (<-received).body["quiet_hours"].(map[string]any)
	if value, present := edit["windows"]; !present || value != nil {
		t.Fatalf("default did not send a null schedule: %+v", edit)
	}

	service := []string{"settings", "--state-dir", dir, "--quiet-hours"}
	if err := run(context.Background(), append(append([]string{}, service...), "mon-fri 18:00-08:00")); err != nil {
		t.Fatal(err)
	}
	call = <-received
	if call.path != "/api/quiet-hours" || len(call.body["windows"].([]any)) != 1 {
		t.Fatalf("service default payload = %+v", call)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{append(append([]string{}, town...), "mon 25:00-01:00"), `start "25:00" must be HH:MM`},
		{append(append([]string{}, town...), "someday 18:00-19:00"), `day "someday"`},
		{append(append([]string{}, town...), ""), "--quiet-hours needs windows"},
		{append(append([]string{}, town...), ";"), "no quiet windows given"},
		{append(append([]string{}, service...), " ; "), "no quiet windows given"},
		{append(append([]string{}, service...), "default"), "applies to a town"},
		{append(append([]string{}, service...), "none", "--model", "x"), "--model needs --repo"},
		{[]string{"settings", "--state-dir", dir, "--repo", "Acme/Team", "--role", "issue", "--quiet-hours", "none"}, "omit --role"},
		{[]string{"settings", "--state-dir", dir}, "--repo OWNER/REPO is required"},
	} {
		err := run(context.Background(), tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: error %v, want %q", tc.args, err, tc.want)
		}
	}
	select {
	case got := <-received:
		t.Fatalf("an invalid schedule reached the service: %+v", got)
	default:
	}
}

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

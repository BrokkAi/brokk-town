package web

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestExecutionAPIValidatesIdentityAndKeepsDemoOffline(t *testing.T) {
	s, h := fixtureMode(t, true)
	if err := s.Store.Update(func(st *town.State) error { _, err := st.Add(town.DefaultConfig("acme/app")); return err }); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, method, body, token string
		status                    int
	}{
		{"/api/execution-options", "GET", "", "", 401},
		{"/api/execution", "POST", `{"town":"acme/app","selection":null}`, "", 401},
		{"/api/execution-options", "GET", "", "test-key", 200},
		{"/api/execution-options/refresh", "POST", `{}`, "test-key", 200},
		{"/api/execution-options/refresh", "POST", `{"url":"http://elsewhere"}`, "test-key", 400},
		{"/api/execution", "POST", `{"town":"acme/app"}`, "test-key", 400},
		{"/api/execution", "POST", `{"town":"acme/app","selection":[]}`, "test-key", 400},
		{"/api/execution", "POST", `{"town":"acme/app","selection":{"command":"agent"}}`, "test-key", 400},
		{"/api/execution", "POST", `{"town":"acme/app","selection":{"target_id":"remote","profile_id":"coder"}}`, "test-key", 400},
		{"/api/execution", "POST", `{"town":"acme/app","role":"review","selection":{"target_id":"","profile_id":""}}`, "test-key", 200},
		{"/api/execution", "POST", `{"town":"acme/app","role":"all","selection":null}`, "test-key", 400},
		{"/api/execution", "POST", `{"town":"acme/app","role":"review","selection":null}`, "test-key", 200},
	} {
		resp := call(t, h.URL, tc.method, tc.path, tc.body, tc.token, "")
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s %s: %d, %s", tc.path, tc.body, resp.StatusCode, data)
		}
		if tc.status == 200 && strings.Contains(tc.path, "options") {
			var listing map[string]any
			if err := json.Unmarshal(data, &listing); err != nil {
				t.Fatal(err)
			}
			if listing["demo"] != true || listing["configured"] != false {
				t.Fatal(listing)
			}
		}
	}
	if len(s.Store.Snapshot().Towns["acme/app"].Config.BotExecution) != 0 {
		t.Fatal("inherit did not clear override")
	}
}

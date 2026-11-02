package reviewbot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseRemoteAgentAcceptsSocketsAndNetworkURLs(t *testing.T) {
	for _, tc := range []struct {
		value, url, socket string
	}{
		{"/run/bt/agent.sock", "http://town/v1/agent", "/run/bt/agent.sock"},
		{"unix:///run/bt/agent.sock", "http://town/v1/agent", "/run/bt/agent.sock"},
		{"http://town:8080/v1/agent", "http://town:8080/v1/agent", ""},
		{"https://town.example/v1/agent", "https://town.example/v1/agent", ""},
	} {
		got, err := parseRemoteAgent(tc.value)
		if err != nil || got.url != tc.url || got.socket != tc.socket {
			t.Fatalf("parseRemoteAgent(%q) = %+v, %v", tc.value, got, err)
		}
	}
	for _, bad := range []string{"relative.sock", "unix://host/agent.sock", "unix:relative", "http:///v1/agent", "ftp://town/v1/agent", "http://user:pass@town/v1/agent", "http://town/v1/agent?x=1"} {
		if _, err := parseRemoteAgent(bad); err == nil {
			t.Fatalf("parseRemoteAgent(%q) accepted an endpoint it cannot use", bad)
		}
	}
}

// A parent reached over the network gets the same request a socket parent
// does, at the URL it named.
func TestRemoteAgentReachesBothTransports(t *testing.T) {
	head := strings.Repeat("a", 40)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/agent" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"protocol": 1, "head": head, "text": "reviewed", "evidence": "run-1"})
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	socket := remoteFixture(t, handler)
	for _, endpoint := range []string{server.URL + "/v1/agent", "unix://" + socket, socket} {
		text, err := executeRemote(t.Context(), Config{RemoteAgent: endpoint, RemoteHead: head}, "review")
		if err != nil || text != "reviewed" {
			t.Fatalf("%s: text %q, err %v", endpoint, text, err)
		}
	}
}

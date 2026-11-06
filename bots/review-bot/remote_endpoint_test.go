package reviewbot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// socketURL names a socket the way the parent does: unix:///tmp/x.sock.
func socketURL(path string) string {
	return "unix:///" + strings.TrimPrefix(filepath.ToSlash(path), "/")
}

func TestParseRemoteAgentAcceptsSocketsAndNetworkURLs(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	for _, tc := range []struct {
		value, url, socket string
	}{
		{socket, "http://town/v1/agent", socket},
		{socketURL(socket), "http://town/v1/agent", socket},
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
		if r.Header.Get("Authorization") != "Bearer dispatch-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"protocol": 1, "head": head, "text": "reviewed", "evidence": "run-1"})
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	socket := remoteFixture(t, handler)
	for _, endpoint := range []string{server.URL + "/v1/agent", socketURL(socket), socket} {
		text, err := executeRemote(t.Context(), Config{RemoteAgent: endpoint, RemoteAgentToken: "dispatch-token", RemoteHead: head}, "review")
		if err != nil || text != "reviewed" {
			t.Fatalf("%s: text %q, err %v", endpoint, text, err)
		}
	}
	// The token travels in the request, and only there.
	if _, err := executeRemote(t.Context(), Config{RemoteAgent: server.URL + "/v1/agent", RemoteHead: head}, "review"); err == nil {
		t.Fatal("a request without the dispatch token was answered")
	}
	if data, _ := json.Marshal(Config{RemoteAgentToken: "dispatch-token"}); strings.Contains(string(data), "dispatch-token") {
		t.Fatal("the dispatch token would be saved with the configuration")
	}
}

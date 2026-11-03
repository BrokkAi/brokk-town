package town

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// AgentCallback is the address bots reach Town at when a managed dispatch
// serves them its agent. Each dispatch registers one route, named by a random
// identifier and guarded by its own token, for as long as it runs.
type AgentCallback struct {
	// URL is the base address bots use, such as http://bt:8091. It must be
	// reachable from wherever the bots run.
	URL string

	mu     sync.Mutex
	routes map[string]http.Handler
}

// NewAgentCallback checks the address bots will use.
func NewAgentCallback(base string) (*AgentCallback, error) {
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("the agent callback URL must be http(s)://host[:port] with no path, credentials, query or fragment")
	}
	return &AgentCallback{URL: strings.TrimRight(parsed.String(), "/")}, nil
}

// Handler serves POST /v1/agent/{id}.
func (c *AgentCallback) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/{id}", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		route := c.routes[r.PathValue("id")]
		c.mu.Unlock()
		if route == nil {
			http.NotFound(w, r)
			return
		}
		route.ServeHTTP(w, r)
	})
	return mux
}

func (c *AgentCallback) register(h http.Handler) (string, func(), error) {
	id, err := botapi.NewToken()
	if err != nil {
		return "", nil, err
	}
	c.mu.Lock()
	if c.routes == nil {
		c.routes = map[string]http.Handler{}
	}
	c.routes[id] = h
	c.mu.Unlock()
	return c.URL + "/v1/agent/" + id, func() {
		c.mu.Lock()
		delete(c.routes, id)
		c.mu.Unlock()
	}, nil
}

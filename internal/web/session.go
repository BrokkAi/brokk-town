package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/auth"
)

// caller is who sent an API request: the holder of the operator's access
// key, or a person who signed in.
type caller struct {
	// Operator is true for the access key: the bt CLI in Town's container, or
	// a browser opened from bt web.
	Operator bool
	Identity auth.Identity
}

type callerKey struct{}

func callerOf(r *http.Request) caller {
	c, _ := r.Context().Value(callerKey{}).(caller)
	return c
}

// authenticate identifies the caller by the access key or a session cookie.
func (s *Server) authenticate(r *http.Request) (caller, bool) {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		provided := strings.TrimPrefix(header, "Bearer ")
		if s.Token != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(s.Token)) == 1 {
			return caller{Operator: true}, true
		}
		return caller{}, false
	}
	identity, ok := s.Auth.Identity(r)
	return caller{Identity: identity}, ok
}

// attributed lists the API writes that record operator events or ask the
// Assistant, so Activity can name who made them. They run through Store.As one
// at a time, so none of them may wait on the network: runtime selection does
// its Mjolnir read first and takes the turn itself (see executionRuntime).
var attributed = map[string]bool{
	"/api/control":   true,
	"/api/guide":     true,
	"/api/towns":     true,
	"/api/settings":  true,
	"/api/execution": true,
	"/api/requests":  true,
}

// operatorOnly lists the API routes a signed-in person cannot use: they run a
// command of the caller's choosing on Town's host.
var operatorOnly = map[string]string{
	"/api/attention-hook": "The attention hook runs a command on Town's host; the operator sets it with the access key.",
}

// serveAPI authenticates an API request, guards a session's writes against
// cross-site use, and attributes the writes listed in attributed.
func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request, next http.Handler) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/session" && r.Method == http.MethodGet {
		s.session(w, r)
		return
	}
	who, ok := s.authenticate(r)
	if !ok {
		problem(w, "sign in required", http.StatusUnauthorized)
		return
	}
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead
	if !who.Operator {
		// A cookie travels with any request the browser makes, so a session's
		// write must prove it came from this page.
		if !safe && r.Header.Get("Origin") != s.Origin {
			problem(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		if reason, blocked := operatorOnly[r.URL.Path]; blocked {
			problem(w, reason, http.StatusForbidden)
			return
		}
	}
	r = r.WithContext(context.WithValue(r.Context(), callerKey{}, who))
	if safe || !attributed[r.URL.Path] {
		next.ServeHTTP(w, r)
		return
	}
	// Answer into a buffer and send it after the turn ends, so a slow client
	// reading a long conversation cannot hold up everyone else's changes.
	buffered := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	_ = s.Store.As(who.Identity.Display(), func() error {
		next.ServeHTTP(buffered, r)
		return nil
	})
	for key, values := range buffered.header {
		w.Header()[key] = values
	}
	w.WriteHeader(buffered.status)
	_, _ = w.Write(buffered.body.Bytes())
}

// bufferedResponse holds a handler's answer in memory. API answers are small
// JSON documents bounded by the state they describe.
type bufferedResponse struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(status int) {
	if !b.wroteHeader {
		b.status, b.wroteHeader = status, true
	}
}
func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.wroteHeader = true
	return b.body.Write(p)
}

// session tells the browser how to sign in and who it is signed in as. It
// needs no credentials and reveals nothing beyond the caller's own identity.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"providers": s.Auth.Providers(), "user": nil}
	if who, ok := s.authenticate(r); ok && !who.Operator {
		out["user"] = who.Identity.Public()
	}
	respond(w, out)
}

// hostCommandRefused explains why a signed-in person may not name a command
// for Town or a bot to run. Choosing a harness from the catalog is fine; an
// arbitrary command is the operator's decision.
const hostCommandRefused = "A custom agent command runs on Town's hosts, so only the operator sets it (the config file or the access key). Choose a harness instead."

func (s *Server) refusesCommand(r *http.Request, command *[]string) bool {
	return command != nil && !callerOf(r).Operator
}

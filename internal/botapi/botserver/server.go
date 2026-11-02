// Package botserver serves one bot over the network contract described in
// package botapi. Each bot binary is a thin main around Main; the service
// reaches it with package botclient.
package botserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Server is the HTTP side of one bot.
type Server struct {
	Bot      botapi.Bot
	Identity botapi.Identity
	// Token is the bearer credential every request but healthz must carry.
	// An empty token refuses every such request.
	Token string
	// Heartbeat is how often a quiet run stream says the bot is still there.
	// Zero means 10 seconds.
	Heartbeat time.Duration
	// Log receives the server's own diagnostics. Nil discards them.
	Log *slog.Logger

	mu   sync.Mutex
	runs map[string]context.CancelCauseFunc
}

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// errCanceledByService is the cause of a run the service asked to stop.
var errCanceledByService = errors.New("the service canceled the run")

// Handler returns the bot's HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`+"\n")
	})
	mux.HandleFunc("GET /v1/identity", s.authorized(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.Identity)
	}))
	mux.HandleFunc("POST /v1/run", s.authorized(s.run))
	mux.HandleFunc("POST /v1/cancel", s.authorized(s.cancel))
	mux.HandleFunc("POST /v1/storage", s.authorized(s.storage))
	return mux
}

func (s *Server) logger() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

// authorized admits a request that carries the bot's token. Both sides are
// hashed first so the comparison takes the same time whatever the length.
func (s *Server) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		want := sha256.Sum256([]byte(s.Token))
		got := sha256.Sum256([]byte(presented))
		if !ok || s.Token == "" || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or wrong bot token")
			return
		}
		next(w, r)
	}
}

// decode reads exactly one JSON object no larger than limit, refusing fields
// the contract does not define.
func decode(w http.ResponseWriter, r *http.Request, limit int64, into any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("the body must hold one JSON object")
	}
	return nil
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get(botapi.RunHeader)
	if !runIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "a run needs a "+botapi.RunHeader+" header of 8 to 128 letters, digits, _ or -")
		return
	}
	var request botapi.Request
	if err := decode(w, r, botapi.MaxRequestBytes, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid run request: "+err.Error())
		return
	}
	if request.Role != s.Identity.Role {
		writeError(w, http.StatusBadRequest, s.Identity.Name+" cannot run the "+request.Role+" role")
		return
	}
	if _, _, err := botapi.Dirs("/", request.Town, request.Role); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	s.mu.Lock()
	if s.runs == nil {
		s.runs = map[string]context.CancelCauseFunc{}
	}
	if _, busy := s.runs[id]; busy {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "a run with that identifier is already active")
		return
	}
	s.runs[id] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.runs, id)
		s.mu.Unlock()
	}()

	out := &frameWriter{w: w, flusher: flusherOf(w), cancel: cancel}
	w.Header().Set("Content-Type", botapi.StreamType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	identity := s.Identity
	out.send(botapi.Frame{Hello: &identity})

	interval := s.Heartbeat
	if interval <= 0 {
		interval = 10 * time.Second
	}
	stopBeat := make(chan struct{})
	beatDone := make(chan struct{})
	go func() {
		defer close(beatDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopBeat:
				return
			case <-ticker.C:
				out.send(botapi.Frame{Heartbeat: true})
			}
		}
	}()
	s.logger().Info("run started", "run", id, "town", request.Town, "mode", request.Mode, "issue", request.Issue, "pr", request.PR)
	result, err := s.Bot.Run(ctx, request, func(e botapi.Event) { out.send(botapi.Frame{Event: boundEvent(e)}) })
	close(stopBeat)
	<-beatDone
	done := &botapi.Done{Result: result}
	if err != nil {
		done.Error = botapi.Truncate(err.Error(), 64<<10)
		if done.Error == "" {
			done.Error = "the run failed without a reason"
		}
	}
	if !out.sendDone(done) {
		s.logger().Warn("run result exceeded the contract's bound", "run", id)
		out.sendDone(&botapi.Done{Error: "the run's result exceeded the contract's size bound"})
	}
	if cause := context.Cause(ctx); cause != nil {
		s.logger().Info("run stopped", "run", id, "cause", cause.Error())
	} else {
		s.logger().Info("run finished", "run", id, "failed", err != nil)
	}
}

// cancel stops a run the service no longer wants. The run still ends with its
// terminal frame, so the service learns what it completed.
func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cancel := s.runs[r.Header.Get(botapi.RunHeader)]
	s.mu.Unlock()
	if cancel == nil {
		writeError(w, http.StatusNotFound, "no such run is active")
		return
	}
	cancel(errCanceledByService)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	var request botapi.StorageRequest
	if err := decode(w, r, botapi.MaxRequestBytes, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid storage request: "+err.Error())
		return
	}
	if request.Role != s.Identity.Role {
		writeError(w, http.StatusBadRequest, s.Identity.Name+" does not keep the "+request.Role+" role's storage")
		return
	}
	result, err := s.Bot.Storage(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// boundEvent keeps one event inside the contract's frame bound. Progress is
// already bounded by the worker; a log record loses its attributes, then its
// message, before it may break the stream.
func boundEvent(e botapi.Event) *botapi.Event {
	if e.Log == nil {
		return &e
	}
	l := *e.Log
	data, _ := json.Marshal(l)
	if len(data) <= botapi.MaxEventBytes-256 {
		return &botapi.Event{Log: &l}
	}
	l.Attrs = map[string]string{"truncated_attrs": "true"}
	l.Message = botapi.Truncate(l.Message, botapi.MaxEventBytes/2)
	return &botapi.Event{Log: &l}
}

// frameWriter writes numbered frames, one per line, from any goroutine. A
// write that fails means the service went away, so it cancels the run.
type frameWriter struct {
	mu      sync.Mutex
	w       io.Writer
	flusher http.Flusher
	cancel  context.CancelCauseFunc
	seq     int
	broken  bool
	done    bool
}

var errServiceGone = errors.New("the service closed the run's stream")

func (f *frameWriter) send(frame botapi.Frame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return
	}
	f.write(frame)
}

// sendDone writes the terminal frame unless it exceeds the result bound, in
// which case nothing is written and the caller sends a smaller one.
func (f *frameWriter) sendDone(done *botapi.Done) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return true
	}
	frame := botapi.Frame{Seq: f.seq, Done: done}
	data, err := marshalLine(frame)
	if err != nil || len(data) > botapi.MaxResultBytes {
		return false
	}
	f.done = true
	f.writeLine(data)
	return true
}

func (f *frameWriter) write(frame botapi.Frame) {
	frame.Seq = f.seq
	data, err := marshalLine(frame)
	if err != nil {
		return
	}
	f.writeLine(data)
}

func (f *frameWriter) writeLine(data []byte) {
	f.seq++
	if f.broken {
		return
	}
	if _, err := f.w.Write(data); err != nil {
		f.broken = true
		f.cancel(errServiceGone)
		return
	}
	if f.flusher != nil {
		f.flusher.Flush()
	}
}

func marshalLine(frame botapi.Frame) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(frame); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func flusherOf(w http.ResponseWriter) http.Flusher {
	flusher, _ := w.(http.Flusher)
	return flusher
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{botapi.Truncate(message, 4096)})
}

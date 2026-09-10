package web

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
)

//go:embed index.html style.css app.js town.js tools.js manage.js scenery.js assets/*
var files embed.FS

type Server struct {
	Store      *town.Store
	Supervisor *town.Supervisor
	Token      string
	Origin     string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { respond(w, s.Store.Snapshot().Public()) })
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/control", s.control)
	mux.HandleFunc("POST /api/towns", s.add)
	mux.HandleFunc("POST /api/settings", s.settings)
	mux.HandleFunc("POST /api/choices", s.choices)
	mux.HandleFunc("GET /api/harnesses", func(w http.ResponseWriter, r *http.Request) { respond(w, s.Supervisor.Harnesses.List()) })
	mux.HandleFunc("POST /api/harnesses/refresh", s.refreshHarnesses)
	mux.HandleFunc("POST /api/requests", s.submitRequest)
	mux.HandleFunc("POST /api/requests/check", s.checkRequest)
	mux.Handle("/", http.FileServerFS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if "http://"+r.Host != s.Origin {
			problem(w, "unrecognized local host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != s.Origin {
			problem(w, "cross-origin requests are not allowed", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(provided), []byte(s.Token)) != 1 || s.Token == "" {
				problem(w, "local access key required", http.StatusUnauthorized)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return fmt.Errorf("JSON content type required")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, "streaming unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		changed := s.Store.Watch()
		snapshot := s.Store.Snapshot()
		data, err := json.Marshal(snapshot.Public())
		if err != nil {
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", snapshot.Seq, data); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-heartbeat.C:
		}
	}
}
func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Town   string    `json:"town"`
		Role   town.Role `json:"role"`
		Action string    `json:"action"`
		Task   string    `json:"task"`
	}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	if err := s.Supervisor.Control(input.Town, input.Role, input.Action, input.Task); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Repo        string             `json:"repo"`
		MergePolicy string             `json:"merge_policy"`
		Agent       town.AgentSettings `json:"agent"`
	}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	if s.Store.Snapshot().Demo {
		problem(w, "Use a live service to add real repositories", 400)
		return
	}
	cfg := town.DefaultConfig(input.Repo)
	if input.MergePolicy != "" {
		cfg.MergePolicy = input.MergePolicy
	}
	cfg, err := s.Supervisor.Prepare(cfg, input.Agent)
	if err != nil {
		problem(w, err.Error(), 400)
		return
	}
	id, err := s.Supervisor.Add(cfg)
	if err != nil {
		problem(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	respond(w, map[string]string{"id": id})
}

func (s *Server) refreshHarnesses(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	if err := s.Supervisor.Harnesses.Refresh(r.Context()); err != nil {
		problem(w, err.Error(), 502)
		return
	}
	respond(w, s.Supervisor.Harnesses.List())
}

type settingsInput struct {
	Town  string             `json:"town"`
	Role  town.Role          `json:"role,omitempty"`
	Agent town.AgentSettings `json:"agent"`
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var input settingsInput
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	if err := s.Supervisor.SettingsForRole(input.Town, input.Role, input.Agent); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *Server) choices(w http.ResponseWriter, r *http.Request) {
	var input settingsInput
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	choices, err := s.Supervisor.ChoicesForRole(r.Context(), input.Town, input.Role, input.Agent)
	if err != nil {
		problem(w, err.Error(), 400)
		return
	}
	respond(w, choices)
}
func (s *Server) submitRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Town  string `json:"town"`
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	result, err := s.Supervisor.SubmitRequest(input.Town, town.IssueRequest{ID: input.ID, Kind: input.Kind, Title: input.Title, Body: input.Body})
	if err != nil {
		problem(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	respond(w, result)
}
func (s *Server) checkRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Town string `json:"town"`
		ID   string `json:"id"`
	}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	if err := s.Supervisor.RecheckRequest(input.Town, input.ID); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	respond(w, map[string]bool{"ok": true})
}

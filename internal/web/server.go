package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BrokkAi/brokk-town/internal/town"
)

//go:embed index.html style.css app.js town.js tools.js manage.js scenery.js assets/*
var files embed.FS

type Server struct {
	Store      *town.Store
	Supervisor *town.Supervisor
	Token      string
	Origin     string
	Version    string
	// TaskGitHub reads one live issue or PR for the inspector. It is a
	// narrow slice of town.GitHub so tests fake two methods, not the whole
	// interface. A nil handle means live details are unavailable.
	TaskGitHub taskGitHub
	Update     func() *town.UpdateNotice
	Upgrade    func(context.Context) error
	// Restart asks the service to replace itself with the binary at its own
	// path. Clients call it after an upgrade or when they are newer.
	Restart func()
}

// taskGitHub fetches a single live issue or pull request on demand. Bodies
// stay out of town state; the inspector reads them only while open.
type taskGitHub interface {
	Issue(ctx context.Context, repository string, number int) (town.GitHubIssue, error)
	Pull(ctx context.Context, repo string, n int) (town.Pull, error)
}

func (s *Server) publicState() map[string]any {
	return s.decorate(s.Store.Snapshot())
}

// decorate adds service-level facts to a snapshot's public view. Clients use
// the version to roll a stale service forward and to reload their own assets.
func (s *Server) decorate(snapshot town.State) map[string]any {
	state := snapshot.Public()
	if s.Version != "" {
		state["version"] = s.Version
	}
	if s.Update != nil {
		if update := s.Update(); update != nil {
			state["update"] = update
		}
	}
	return state
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { respond(w, s.publicState()) })
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/control", s.control)
	mux.HandleFunc("POST /api/towns", s.add)
	mux.HandleFunc("POST /api/settings", s.settings)
	mux.HandleFunc("GET /api/bot-versions", s.botVersions)
	mux.HandleFunc("POST /api/capacity", s.capacity)
	mux.HandleFunc("POST /api/choices", s.choices)
	mux.HandleFunc("GET /api/harnesses", func(w http.ResponseWriter, r *http.Request) { respond(w, s.Supervisor.Harnesses.List()) })
	mux.HandleFunc("POST /api/harnesses/refresh", s.refreshHarnesses)
	mux.HandleFunc("POST /api/requests", s.submitRequest)
	mux.HandleFunc("POST /api/requests/check", s.checkRequest)
	mux.HandleFunc("POST /api/task-detail", s.taskDetail)
	mux.HandleFunc("GET /api/outcomes", s.outcomes)
	mux.HandleFunc("POST /api/outcomes/judgment", s.outcomeJudgment)
	mux.HandleFunc("POST /api/update", s.upgrade)
	mux.HandleFunc("POST /api/restart", s.restart)
	mux.Handle("/", http.FileServerFS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Embedded assets change with every service binary; a reload after a
		// restart must always fetch the current ones.
		w.Header().Set("Cache-Control", "no-cache")
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

func (s *Server) outcomes(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	to := now.Add(time.Nanosecond)
	from := now.Add(-7 * 24 * time.Hour)
	var err error
	if value := r.URL.Query().Get("from"); value != "" {
		from, err = time.Parse(time.RFC3339, value)
	}
	if err == nil {
		if value := r.URL.Query().Get("to"); value != "" {
			to, err = time.Parse(time.RFC3339, value)
		}
	}
	if err != nil {
		problem(w, "from and to must be RFC3339 timestamps", http.StatusBadRequest)
		return
	}
	report, err := town.BuildOutcomeReport(s.Store.Snapshot(), strings.ToLower(r.URL.Query().Get("town")), from, to, now)
	if err != nil {
		problem(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("format") != "csv" {
		respond(w, report)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="town-outcomes.csv"`)
	csvRow(w, []string{"town", "id", "at", "class", "kind", "status", "role", "task_id", "related_task_id", "revision", "url", "detail", "elapsed_ms", "input_tokens", "output_tokens", "cost_usd", "judgment", "explanation"})
	for _, record := range report.Records {
		elapsed, input, output, cost := "unknown", "unknown", "unknown", "unknown"
		if record.ElapsedMS != nil {
			elapsed = strconv.FormatInt(*record.ElapsedMS, 10)
		}
		if record.Usage != nil {
			input, output = strconv.FormatInt(record.Usage.InputTokens, 10), strconv.FormatInt(record.Usage.OutputTokens, 10)
		}
		if record.CostUSD != nil {
			cost = strconv.FormatFloat(*record.CostUSD, 'f', -1, 64)
		}
		judgment, explanation := "unjudged", ""
		if record.Judgment != nil {
			judgment, explanation = record.Judgment.Value, record.Judgment.Explanation
		}
		csvRow(w, []string{record.Town, record.ID, record.At.Format(time.RFC3339Nano), record.Class, record.Kind, record.Status, string(record.Role), record.TaskID, record.RelatedTaskID, record.Revision, record.URL, record.Detail, elapsed, input, output, cost, judgment, explanation})
	}
}

func csvRow(w io.Writer, fields []string) {
	for i, field := range fields {
		if i > 0 {
			_, _ = io.WriteString(w, ",")
		}
		if strings.ContainsAny(field, ",\"\r\n") {
			field = `"` + strings.ReplaceAll(field, `"`, `""`) + `"`
		}
		_, _ = io.WriteString(w, field)
	}
	_, _ = io.WriteString(w, "\n")
}

func (s *Server) outcomeJudgment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Town        string `json:"town"`
		Outcome     string `json:"outcome"`
		Value       string `json:"value"`
		Explanation string `json:"explanation"`
	}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), http.StatusBadRequest)
		return
	}
	err := s.Store.Update(func(state *town.State) error {
		current := state.Towns[strings.ToLower(input.Town)]
		if current == nil || current.Deleted {
			return fmt.Errorf("unknown town")
		}
		return current.JudgeOutcome(input.Outcome, input.Value, input.Explanation, time.Now())
	})
	if err != nil {
		problem(w, err.Error(), http.StatusBadRequest)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *Server) upgrade(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.Upgrade == nil {
		problem(w, "no Town update is available", http.StatusConflict)
		return
	}
	if err := s.Upgrade(r.Context()); err != nil {
		problem(w, err.Error(), http.StatusBadGateway)
		return
	}
	if s.Restart == nil {
		respond(w, map[string]any{"ok": true, "restart_required": true})
		return
	}
	respond(w, map[string]any{"ok": true, "restarting": true})
	s.Restart()
}
func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.Restart == nil {
		problem(w, "this service cannot restart itself", http.StatusConflict)
		return
	}
	respond(w, map[string]any{"ok": true, "restarting": true})
	s.Restart()
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
		data, err := json.Marshal(s.decorate(snapshot))
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
	Town                 string             `json:"town"`
	Role                 town.Role          `json:"role,omitempty"`
	Agent                town.AgentSettings `json:"agent"`
	MergePolicy          *string            `json:"merge_policy,omitempty"`
	MayoralFeatureReview *bool              `json:"mayoral_feature_review,omitempty"`
	BotVersion           *string            `json:"bot_version,omitempty"`
	AutoUpdateBots       *bool              `json:"auto_update_bots,omitempty"`
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var input settingsInput
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	if err := s.Supervisor.SettingsForRoleAndPolicy(input.Town, input.Role, input.Agent, input.MergePolicy, input.MayoralFeatureReview, input.BotVersion, input.AutoUpdateBots); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func (s *Server) botVersions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	versions, err := town.CheckBotVersions(ctx, http.DefaultClient)
	if err != nil {
		problem(w, err.Error(), http.StatusBadGateway)
		return
	}
	respond(w, versions)
}

// maxTaskDetailBody caps one live body so a pasted log cannot bloat the
// inspector payload. Longer bodies stay fully available at the source link.
const maxTaskDetailBody = 10000

type taskDetailResponse struct {
	Kind           string    `json:"kind"`
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	Truncated      bool      `json:"truncated,omitempty"`
	Author         string    `json:"author,omitempty"`
	Comments       int       `json:"comments"`
	ReviewComments int       `json:"review_comments,omitempty"`
	State          string    `json:"state"`
	UpdatedAt      time.Time `json:"updated_at"`
	URL            string    `json:"url"`
}

func truncateTaskDetailBody(body string) (string, bool) {
	if len(body) <= maxTaskDetailBody {
		return body, false
	}
	cut := body[:maxTaskDetailBody]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

func (s *Server) taskDetail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Town string `json:"town"`
		Task string `json:"task"`
	}
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	if input.Town == "" || input.Task == "" {
		problem(w, "town and task are required", 400)
		return
	}
	snapshot := s.Store.Snapshot()
	if snapshot.Demo {
		problem(w, "demo towns have no live source", http.StatusConflict)
		return
	}
	t := snapshot.Towns[strings.ToLower(input.Town)]
	if t == nil {
		problem(w, "unknown town", 404)
		return
	}
	task := t.Tasks[input.Task]
	if task == nil || task.Number < 1 || (task.Kind != "issue" && task.Kind != "pr") {
		problem(w, "live details need a GitHub issue or PR number", 400)
		return
	}
	if s.TaskGitHub == nil {
		problem(w, "live GitHub details are unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if task.Kind == "pr" {
		p, err := s.TaskGitHub.Pull(ctx, t.Config.Repo, task.Number)
		if err != nil {
			problem(w, err.Error(), http.StatusBadGateway)
			return
		}
		body, truncated := truncateTaskDetailBody(p.Body)
		respond(w, taskDetailResponse{Kind: "pr", Number: p.Number, Title: p.Title, Body: body, Truncated: truncated, Author: p.User.Login, Comments: p.Comments, ReviewComments: p.ReviewComments, State: p.State, UpdatedAt: p.Updated, URL: p.URL})
		return
	}
	issue, err := s.TaskGitHub.Issue(ctx, t.Config.Repo, task.Number)
	if err != nil {
		problem(w, err.Error(), http.StatusBadGateway)
		return
	}
	body, truncated := truncateTaskDetailBody(issue.Body)
	respond(w, taskDetailResponse{Kind: "issue", Number: issue.Number, Title: issue.Title, Body: body, Truncated: truncated, Author: issue.Author, Comments: issue.Comments, State: issue.State, UpdatedAt: issue.UpdatedAt, URL: issue.URL})
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

func (s *Server) capacity(w http.ResponseWriter, r *http.Request) {
	var input town.ServiceConfig
	if err := decode(w, r, &input); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	if err := s.Supervisor.SetCapacity(input.MaxWorkers); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	respond(w, s.Store.Snapshot().Capacity)
}

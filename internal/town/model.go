package town

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

type Role string

const (
	Bug     Role = "bug"
	Issue   Role = "issue"
	Review  Role = "review"
	Release Role = "release"
	Repo    Role = "repo"
)

var Roles = []Role{Bug, Issue, Review, Release, Repo}

func ValidRole(r Role) bool {
	for _, v := range Roles {
		if r == v {
			return true
		}
	}
	return false
}

var branchName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)
var slug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func ValidRepo(r string) bool { return slug.MatchString(r) && !strings.Contains(r, "..") }
func Key(v string) string     { return fmt.Sprintf("%x", sha256.Sum256([]byte(v)))[:24] }
func SHA(v string) bool {
	return (len(v) == 40 || len(v) == 64) && strings.Trim(v, "0123456789abcdef") == ""
}

type Config struct {
	Repo          string             `json:"repo"`
	Branch        string             `json:"branch,omitempty"`
	Harness       string             `json:"harness,omitempty"`
	Agent         runner.AgentConfig `json:"agent"`
	Verify        []string           `json:"verify,omitempty"`
	MergePolicy   string             `json:"merge_policy"`
	PollSeconds   int                `json:"poll_seconds"`
	ReportSeconds int                `json:"report_seconds"`
	MaxCycles     int                `json:"max_cycles"`
}

func DefaultConfig(repo string) Config {
	return Config{Repo: repo, MergePolicy: "bot", PollSeconds: 60, ReportSeconds: 1800, MaxCycles: 5}
}
func (c Config) Validate() error {
	if err := validateAgent(c); err != nil {
		return err
	}
	if !ValidRepo(c.Repo) {
		return fmt.Errorf("repository must be OWNER/REPO")
	}
	if c.MergePolicy != "bot" && c.MergePolicy != "manual" && c.MergePolicy != "all" {
		return fmt.Errorf("merge policy must be bot, manual, or all")
	}
	if c.PollSeconds < 10 || c.ReportSeconds < 60 || c.MaxCycles < 1 || c.MaxCycles > 20 {
		return fmt.Errorf("poll must be at least 10 seconds, reports at least 60 seconds, and cycles between 1 and 20")
	}
	if c.Branch != "" && (!branchName.MatchString(c.Branch) || strings.Contains(c.Branch, "..") || strings.Contains(c.Branch, "//") || strings.HasSuffix(c.Branch, "/") || strings.HasSuffix(c.Branch, ".") || strings.HasSuffix(c.Branch, ".lock")) {
		return fmt.Errorf("invalid branch")
	}
	return nil
}

// PublicConfig deliberately excludes agent environment values and command arguments.
type PublicConfig struct {
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	MergePolicy string `json:"merge_policy"`
	MaxCycles   int    `json:"max_cycles"`
	Harness     string `json:"harness"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
}
type Worker struct {
	Role    Role      `json:"role"`
	Enabled bool      `json:"enabled"`
	Status  string    `json:"status"`
	Phase   string    `json:"phase"`
	Task    string    `json:"task"`
	Error   string    `json:"error,omitempty"`
	Updated time.Time `json:"updated"`
	Next    time.Time `json:"next,omitempty"`
	Logs    []Log     `json:"logs"`
}
type Log struct {
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}
type Finding struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}
type Audit struct {
	Base        string    `json:"base"`
	Head        string    `json:"head"`
	Discussion  string    `json:"discussion"`
	Description string    `json:"description"`
	Verdict     string    `json:"verdict"`
	Complete    bool      `json:"complete"`
	Summary     string    `json:"summary"`
	Checks      []string  `json:"checks"`
	Findings    []Finding `json:"findings"`
	At          time.Time `json:"at"`
}

func (a *Audit) Clean(base, head string) bool {
	if a == nil || !a.Complete || a.Verdict != "clean" || a.Base != base || a.Head != head || !SHA(base) || !SHA(head) {
		return false
	}
	for _, f := range a.Findings {
		if f.State != "resolved" && f.State != "dismissed" {
			return false
		}
	}
	return true
}

type Task struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Number      int               `json:"number,omitempty"`
	Title       string            `json:"title"`
	URL         string            `json:"url,omitempty"`
	Stage       string            `json:"stage"`
	House       Role              `json:"house"`
	External    bool              `json:"external"`
	Head        string            `json:"head,omitempty"`
	Base        string            `json:"base,omitempty"`
	Branch      string            `json:"branch,omitempty"`
	Detail      string            `json:"detail,omitempty"`
	Description string            `json:"description,omitempty"`
	Updated     time.Time         `json:"updated"`
	Audit       *Audit            `json:"audit,omitempty"`
	Concerns    map[string]string `json:"concerns,omitempty"`
	Cycles      int               `json:"cycles"`
	Blocked     bool              `json:"blocked"`
	Attempts    int               `json:"attempts"`
	RetryAt     time.Time         `json:"retry_at,omitempty"`
}
type Ownership struct {
	Branch string `json:"branch"`
	Issue  int    `json:"issue"`
}
type Intent struct {
	Kind      string    `json:"kind"`
	PR        int       `json:"pr"`
	Base      string    `json:"base"`
	Head      string    `json:"head"`
	NewHead   string    `json:"new_head,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Directory string    `json:"directory,omitempty"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	At        time.Time `json:"at"`
}
type Report struct {
	At    time.Time `json:"at"`
	Title string    `json:"title"`
	Body  string    `json:"body"`
}
type Event struct {
	Seq   uint64    `json:"seq"`
	At    time.Time `json:"at"`
	Town  string    `json:"town"`
	Kind  string    `json:"kind"`
	From  string    `json:"from,omitempty"`
	To    string    `json:"to,omitempty"`
	Cargo string    `json:"cargo,omitempty"`
	Title string    `json:"title"`
}
type Town struct {
	ID          string                   `json:"id"`
	Deleted     bool                     `json:"deleted,omitempty"`
	Config      Config                   `json:"config"`
	Initialized bool                     `json:"initialized"`
	Workers     map[Role]*Worker         `json:"workers"`
	Tasks       map[string]*Task         `json:"tasks"`
	Owned       map[int]Ownership        `json:"owned"`
	Intents     map[int]*Intent          `json:"intents"`
	Requests    map[string]*IssueRequest `json:"requests,omitempty"`
	Reports     []Report                 `json:"reports"`
	Head        string                   `json:"head"`
	LastSync    time.Time                `json:"last_sync"`
	LastRelease string                   `json:"last_release"`
	Error       string                   `json:"error,omitempty"`
}
type State struct {
	Format int              `json:"format"`
	Seq    uint64           `json:"seq"`
	Demo   bool             `json:"demo"`
	Towns  map[string]*Town `json:"towns"`
	Events []Event          `json:"events"`
}

func NewState(demo bool) State {
	return State{Format: 1, Demo: demo, Towns: map[string]*Town{}, Events: []Event{}}
}
func (s *State) Add(c Config) (*Town, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	id := strings.ToLower(c.Repo)
	if _, ok := s.Towns[id]; ok {
		t := s.Towns[id]
		if !t.Deleted {
			return nil, fmt.Errorf("town already exists")
		}
		// Retain ownership, worktrees and uncertain writes when restoring a town.
		// Restoration never resumes automation or pending issue submissions.
		t.Deleted = false
		t.Workers[Repo].Enabled = true
		t.Workers[Repo].Next = time.Time{}
		return t, nil
	}
	t := &Town{ID: id, Config: c, Workers: map[Role]*Worker{}, Tasks: map[string]*Task{}, Owned: map[int]Ownership{}, Intents: map[int]*Intent{}, Reports: []Report{}}
	for _, r := range Roles {
		t.Workers[r] = &Worker{Role: r, Enabled: r == Repo, Status: "paused", Task: "Ready when you are", Logs: []Log{}}
	}
	s.Towns[id] = t
	return t, nil
}
func (s *State) Event(town, kind, from, to, cargo, title string, now time.Time) {
	s.Seq++
	s.Events = append(s.Events, Event{s.Seq, now, town, kind, from, to, cargo, title})
	if len(s.Events) > 512 {
		s.Events = s.Events[len(s.Events)-512:]
	}
}
func (s *State) Move(t *Town, task *Task, stage string, house Role, title string, now time.Time) {
	if task.Stage == stage && task.House == house {
		return
	}
	from := string(task.House)
	task.Stage = stage
	task.House = house
	task.Updated = now
	s.Event(t.ID, "delivery", from, string(house), task.ID, title, now)
}
func (t *Town) Report(title, body string, now time.Time) {
	t.Reports = append(t.Reports, Report{now, title, body})
	if len(t.Reports) > 100 {
		t.Reports = t.Reports[len(t.Reports)-100:]
	}
}
func clone[T any](v T) T { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }
func (s State) Public() map[string]any {
	b, _ := json.Marshal(s)
	var public map[string]any
	_ = json.Unmarshal(b, &public)
	for id, t := range s.Towns {
		if t.Deleted {
			delete(public["towns"].(map[string]any), id)
			continue
		}
		public["towns"].(map[string]any)[id].(map[string]any)["config"] = t.Config.Public()
	}
	events := []Event{}
	for _, e := range s.Events {
		if t := s.Towns[e.Town]; t != nil && !t.Deleted {
			events = append(events, e)
		}
	}
	public["events"] = events
	return public
}

func (c Config) Public() PublicConfig {
	return PublicConfig{Repo: c.Repo, Branch: c.Branch, MergePolicy: c.MergePolicy, MaxCycles: c.MaxCycles, Harness: c.harness(), Model: c.Agent.Model, Effort: c.Agent.Effort}
}

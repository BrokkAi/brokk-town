package town

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/harness"
)

type Role string

const (
	Bug     Role = "bug"
	Feature Role = "feature"
	Issue   Role = "issue"
	Review  Role = "review"
	Release Role = "release"
	Repo    Role = "repo"
	Hall    Role = "hall"
)

var Roles = []Role{Bug, Issue, Review, Release, Repo, Feature}

// AgentRoles excludes the reporter, which never starts an agent.
var AgentRoles = []Role{Bug, Feature, Issue, Review, Release}

// WorkerAuthority is the concise operator-facing description shared by the
// terminal clients. Browser code mirrors these strings near the same controls.
func WorkerAuthority(role Role, mergePolicy string) string {
	switch role {
	case Bug:
		return "May inspect repository content and file GitHub bug issues."
	case Feature:
		return "May inspect repository content and propose or file GitHub feature issues."
	case Issue:
		return "May claim issues, create pull requests, and push repairs to Town-owned branches."
	case Review:
		return "May post pull request reviews and findings and merge eligible pull requests when merge policy permits; it does not edit contributor branches."
	case Release:
		detail := "May create and merge release-preparation pull requests and publish releases and packages."
		if mergePolicy == "manual" {
			return detail + " Paused while every merge is manual."
		}
		return detail
	case Repo:
		return "Read-only: inventories and reconciles repository state without an agent or GitHub writes."
	default:
		return ""
	}
}

func ValidAgentRole(r Role) bool {
	for _, v := range AgentRoles {
		if r == v {
			return true
		}
	}
	return false
}

func ValidRole(r Role) bool {
	if r == Hall {
		return true
	}
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

// ValidBranch accepts a Git branch name Town is willing to fetch and push to.
func ValidBranch(b string) bool {
	return branchName.MatchString(b) && !strings.Contains(b, "..") && !strings.Contains(b, "//") &&
		!strings.HasSuffix(b, "/") && !strings.HasSuffix(b, ".") && !strings.HasSuffix(b, ".lock")
}
func Key(v string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(v)))[:24] }
func SHA(v string) bool {
	return (len(v) == 40 || len(v) == 64) && strings.Trim(v, "0123456789abcdef") == ""
}

type Config struct {
	Repo                 string                  `json:"repo"`
	Branch               string                  `json:"branch,omitempty"`
	Harness              string                  `json:"harness,omitempty"`
	HarnessDefinition    *harness.Entry          `json:"harness_definition,omitempty"`
	Agent                runner.AgentConfig      `json:"agent"`
	BotAgents            map[Role]BotAgentConfig `json:"bot_agents,omitempty"`
	BotVersions          map[Role]string         `json:"bot_versions,omitempty"`
	Verify               []string                `json:"verify,omitempty"`
	MergePolicy          string                  `json:"merge_policy"`
	PollSeconds          int                     `json:"poll_seconds"`
	ReportSeconds        int                     `json:"report_seconds"`
	MaxCycles            int                     `json:"max_cycles"`
	Funnels              FunnelConfigs           `json:"funnels,omitempty"`
	MayoralFeatureReview *bool                   `json:"mayoral_feature_review,omitempty"`
	AutoUpdateBots       bool                    `json:"auto_update_bots,omitempty"`
}

// BotAgentConfig is a complete private selection. Omitted roles inherit the town
// default; present profiles stay independent when that default changes.
type BotAgentConfig struct {
	Harness           string             `json:"harness,omitempty"`
	HarnessDefinition *harness.Entry     `json:"harness_definition,omitempty"`
	Agent             runner.AgentConfig `json:"agent"`
}

func (c Config) botAgent() BotAgentConfig {
	return BotAgentConfig{Harness: c.Harness, HarnessDefinition: c.HarnessDefinition, Agent: c.Agent}
}

func (c Config) withAgent(a BotAgentConfig) Config {
	c.Harness, c.HarnessDefinition, c.Agent = a.Harness, a.HarnessDefinition, a.Agent
	return c
}

// ForRole resolves a stored town configuration into an isolated configuration for
// one bot dispatch. Call it on the town configuration, before resolving a harness.
func (c Config) ForRole(role Role) Config {
	c = clone(c)
	if a, ok := c.BotAgents[role]; ok {
		c = c.withAgent(a)
	}
	return c
}

func DefaultConfig(repo string) Config {
	review := true
	return Config{Repo: repo, MergePolicy: "bot", PollSeconds: 60, ReportSeconds: 1800, MaxCycles: 5, MayoralFeatureReview: &review}
}
func (c Config) ReviewsFeaturesWithMayor() bool {
	return c.MayoralFeatureReview == nil || *c.MayoralFeatureReview
}
func (c Config) Validate() error {
	if err := validateAgent(c); err != nil {
		return err
	}
	for role, a := range c.BotAgents {
		if !ValidAgentRole(role) {
			return fmt.Errorf("agent settings require a bot role: %q", role)
		}
		if err := validateAgent(c.withAgent(a)); err != nil {
			return fmt.Errorf("%s agent: %w", role, err)
		}
	}
	for role, version := range c.BotVersions {
		if !ValidAgentRole(role) || !workerVersionPattern.MatchString(version) {
			return fmt.Errorf("invalid pinned bot version")
		}
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
	if c.Branch != "" && !ValidBranch(c.Branch) {
		return fmt.Errorf("invalid branch")
	}
	if err := c.Funnels.Validate(); err != nil {
		return err
	}
	return nil
}

// PublicConfig deliberately excludes agent environment values and command arguments.
type PublicConfig struct {
	Repo                 string                        `json:"repo"`
	Branch               string                        `json:"branch"`
	MergePolicy          string                        `json:"merge_policy"`
	MaxCycles            int                           `json:"max_cycles"`
	Harness              string                        `json:"harness"`
	Model                string                        `json:"model"`
	Effort               string                        `json:"effort"`
	HarnessVersion       string                        `json:"harness_version,omitempty"`
	BotAgents            map[Role]PublicBotAgentConfig `json:"bot_agents"`
	BotVersions          map[Role]string               `json:"bot_versions"`
	Funnels              []PublicFunnelConfig          `json:"funnels,omitempty"`
	MayoralFeatureReview bool                          `json:"mayoral_feature_review"`
	AutoUpdateBots       bool                          `json:"auto_update_bots"`
}

type PublicBotAgentConfig struct {
	Harness        string `json:"harness"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	HarnessVersion string `json:"harness_version,omitempty"`
	Inherited      bool   `json:"inherited"`
}
type Worker struct {
	Agent   *PublicBotAgentConfig `json:"agent,omitempty"`
	Run     *WorkerRun            `json:"run,omitempty"`
	Role    Role                  `json:"role"`
	Enabled bool                  `json:"enabled"`
	Status  string                `json:"status"`
	Phase   string                `json:"phase"`
	Task    string                `json:"task"`
	Error   string                `json:"error,omitempty"`
	Updated time.Time             `json:"updated"`
	Next    time.Time             `json:"next,omitempty"`
	Logs    []Log                 `json:"logs"`
	// RetryRequested asks the next release dispatch to lift the release bot's
	// exhausted attempt budget through its worker API before running.
	RetryRequested bool `json:"retry_requested,omitempty"`
}

// WorkerRun is the durable handle of one external bot process. It is written
// before the run request is sent and cleared when Town has consumed the outcome,
// so a service that restarts can find the process again instead of losing it.
type WorkerRun struct {
	Bot        string    `json:"bot"`
	Version    string    `json:"version"`
	Command    string    `json:"command"`
	Args       []string  `json:"args,omitempty"`
	Hash       string    `json:"hash"`
	PID        int       `json:"pid"`
	Socket     string    `json:"socket"`
	Output     string    `json:"output"`
	Detachable bool      `json:"detachable"`
	Seq        uint64    `json:"seq"`
	Started    time.Time `json:"started"`
	Deadline   time.Time `json:"deadline"`
	Issue      int       `json:"issue,omitempty"`
	PR         int       `json:"pr,omitempty"`
	BaseSHA    string    `json:"base_sha,omitempty"`
	HeadSHA    string    `json:"head_sha,omitempty"`
}

func (r *WorkerRun) Validate(role Role) error {
	if r == nil {
		return nil
	}
	if !ValidAgentRole(role) {
		return fmt.Errorf("%s worker cannot own an external run", role)
	}
	if r.PID <= 0 || r.Socket == "" || r.Bot == "" || !workerVersionPattern.MatchString(r.Version) || r.Command == "" {
		return errors.New("invalid worker run handle")
	}
	if (r.BaseSHA != "" && !SHA(r.BaseSHA)) || (r.HeadSHA != "" && !SHA(r.HeadSHA)) || r.Issue < 0 || r.PR < 0 {
		return errors.New("invalid worker run identity")
	}
	return nil
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
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Number          int               `json:"number,omitempty"`
	Title           string            `json:"title"`
	URL             string            `json:"url,omitempty"`
	Stage           string            `json:"stage"`
	House           Role              `json:"house"`
	External        bool              `json:"external"`
	MayoralDecision string            `json:"mayoral_decision,omitempty"`
	Head            string            `json:"head,omitempty"`
	Base            string            `json:"base,omitempty"`
	Branch          string            `json:"branch,omitempty"`
	Detail          string            `json:"detail,omitempty"`
	Description     string            `json:"description,omitempty"`
	Updated         time.Time         `json:"updated"`
	Audit           *Audit            `json:"audit,omitempty"`
	Concerns        map[string]string `json:"concerns,omitempty"`
	Cycles          int               `json:"cycles"`
	Blocked         bool              `json:"blocked"`
	Attempts        int               `json:"attempts"`
	RetryAt         time.Time         `json:"retry_at,omitempty"`
	IssueJob        *IssueJob         `json:"issue_job,omitempty"`
	Source          *WorkItem         `json:"source,omitempty"`
	Upgrade         *BotUpgrade       `json:"upgrade,omitempty"`
}

// BotUpgrade records one published stable bot version that is newer than the
// town's pin. Town applies it only after the Mayor approves it, or immediately
// when the town has opted into automatic bot updates.
type BotUpgrade struct {
	Role Role   `json:"role"`
	From string `json:"from"`
	To   string `json:"to"`
}

// IssueJob is the public scheduling outcome from issue-bot durable state.
type IssueJob struct {
	Status        string `json:"status"`
	LastError     string `json:"last_error,omitempty"`
	ResultStatus  string `json:"result_status,omitempty"`
	ResultDetail  string `json:"result_detail,omitempty"`
	ClaimPending  bool   `json:"claim_pending"`
	RetryEligible bool   `json:"retry_eligible"`
	RetryDetail   string `json:"retry_detail"`
}

type FunnelSync struct {
	Funnel   FunnelID   `json:"funnel"`
	Provider ProviderID `json:"provider"`
	Cursor   Cursor     `json:"cursor,omitempty"`
	LastSync time.Time  `json:"last_sync,omitempty"`
	Outcome  Outcome    `json:"outcome"`
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
	ID            string                   `json:"id"`
	Deleted       bool                     `json:"deleted,omitempty"`
	Config        Config                   `json:"config"`
	Initialized   bool                     `json:"initialized"`
	Workers       map[Role]*Worker         `json:"workers"`
	Tasks         map[string]*Task         `json:"tasks"`
	Owned         map[int]Ownership        `json:"owned"`
	Intents       map[int]*Intent          `json:"intents"`
	FunnelIntents map[string]*WriteIntent  `json:"funnel_intents,omitempty"`
	FunnelSyncs   map[FunnelID]*FunnelSync `json:"funnel_syncs,omitempty"`
	Requests      map[string]*IssueRequest `json:"requests,omitempty"`
	Reports       []Report                 `json:"reports"`
	Outcomes      []OutcomeRecord          `json:"outcomes"`
	Head          string                   `json:"head"`
	// DefaultBranch is the repository default GitHub reported at the last
	// inventory. It is an observation, never configuration: Config.Branch stays
	// whatever the operator chose, or empty to follow this default.
	DefaultBranch string    `json:"default_branch,omitempty"`
	LastSync      time.Time `json:"last_sync"`
	LastRelease   string    `json:"last_release"`
	Error         string    `json:"error,omitempty"`
}

// ServiceConfig governs the single local scheduler across every town.
const DefaultMaxWorkers = 4
const MaximumMaxWorkers = 64

type ServiceConfig struct {
	MaxWorkers int `json:"max_workers"`
}

func (c ServiceConfig) Validate() error {
	if c.MaxWorkers < 1 || c.MaxWorkers > MaximumMaxWorkers {
		return fmt.Errorf("max_workers must be between 1 and %d", MaximumMaxWorkers)
	}
	return nil
}

type Capacity struct {
	Active int `json:"active"`
	Limit  int `json:"limit"`
}

type State struct {
	ServiceConfig ServiceConfig    `json:"service_config"`
	Capacity      *Capacity        `json:"capacity,omitempty"`
	Format        int              `json:"format"`
	Seq           uint64           `json:"seq"`
	Demo          bool             `json:"demo"`
	Towns         map[string]*Town `json:"towns"`
	Events        []Event          `json:"events"`
	Update        *UpdateNotice    `json:"update,omitempty"`
}

func NewState(demo bool) State {
	return State{ServiceConfig: ServiceConfig{MaxWorkers: DefaultMaxWorkers}, Format: 1, Demo: demo, Towns: map[string]*Town{}, Events: []Event{}}
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
	t := &Town{ID: id, Config: c, Workers: map[Role]*Worker{}, Tasks: map[string]*Task{}, Owned: map[int]Ownership{}, Intents: map[int]*Intent{}, FunnelIntents: map[string]*WriteIntent{}, FunnelSyncs: map[FunnelID]*FunnelSync{}, Reports: []Report{}, Outcomes: []OutcomeRecord{}}
	for _, r := range Roles {
		t.Workers[r] = &Worker{Role: r, Enabled: r == Repo, Status: "paused", Task: "Ready when you are", Logs: []Log{}}
	}
	s.Towns[id] = t
	return t, nil
}

// Branch is the branch this town actually works on: the operator's choice when
// they made one, otherwise the repository default observed at the last inventory.
func (t *Town) Branch() string {
	if t.Config.Branch != "" {
		return t.Config.Branch
	}
	return t.DefaultBranch
}

// PublicConfig reports the branch in use so clients show the effective branch
// rather than an empty setting on a town that follows the repository default.
func (t *Town) PublicConfig() PublicConfig {
	cfg := t.Config.Public()
	cfg.Branch = t.Branch()
	return cfg
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
		public["towns"].(map[string]any)[id].(map[string]any)["config"] = t.PublicConfig()
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
	version := ""
	if c.HarnessDefinition != nil {
		version = c.HarnessDefinition.Version
	}
	bots := make(map[Role]PublicBotAgentConfig, len(AgentRoles))
	for _, role := range AgentRoles {
		cfg := c
		a, overridden := c.BotAgents[role]
		if overridden {
			cfg = c.withAgent(a)
		}
		botVersion := ""
		if cfg.HarnessDefinition != nil {
			botVersion = cfg.HarnessDefinition.Version
		}
		bots[role] = PublicBotAgentConfig{Harness: cfg.harness(), Model: cfg.Agent.Model, Effort: cfg.Agent.Effort, HarnessVersion: botVersion, Inherited: !overridden}
	}
	funnels := make([]PublicFunnelConfig, 0, len(c.Funnels))
	for _, funnel := range c.Funnels {
		funnels = append(funnels, funnel.Public())
	}
	versions := make(map[Role]string, len(AgentRoles))
	for _, role := range AgentRoles {
		versions[role] = c.BotVersion(role)
	}
	return PublicConfig{Repo: c.Repo, Branch: c.Branch, MergePolicy: c.MergePolicy, MaxCycles: c.MaxCycles, Harness: c.harness(), Model: c.Agent.Model, Effort: c.Agent.Effort, HarnessVersion: version, BotAgents: bots, BotVersions: versions, Funnels: funnels, MayoralFeatureReview: c.ReviewsFeaturesWithMayor(), AutoUpdateBots: c.AutoUpdateBots}
}

func (c Config) BotVersion(role Role) string {
	if version := c.BotVersions[role]; version != "" {
		return version
	}
	return workerDefaultVersions[role]
}

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
	Bug        Role = "bug"
	Feature    Role = "feature"
	Issue      Role = "issue"
	Review     Role = "review"
	Release    Role = "release"
	Repo       Role = "repo"
	Simplifier Role = "simplifier"
	Hall       Role = "hall"
)

var Roles = []Role{Bug, Simplifier, Issue, Review, Release, Repo, Feature, Hall}

// AgentRoles excludes the reporter, which never starts an agent. Repo Bot is
// one of them: observing the repository needs no agent, but repairing the
// branch it covers does.
var AgentRoles = []Role{Bug, Feature, Issue, Review, Release, Simplifier, Repo, Hall}

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
		return "Inventories repository state, and repairs the branch it covers when its checks fail."
	case Simplifier:
		return "May inspect repository content, file simplification issues, and in auto mode recommend that Town decline or close low-value complex issues."
	default:
		return ""
	}
}

// OccupiesAgentSlot reports the houses whose run holds one of the service's
// agent slots for its whole length. The repo house observes the repository
// without an agent, and holds a slot only while it repairs the branch.
func OccupiesAgentSlot(r Role) bool { return ValidAgentRole(r) && r != Repo }

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
	Repo              string                  `json:"repo"`
	Branch            string                  `json:"branch,omitempty"`
	Harness           string                  `json:"harness,omitempty"`
	HarnessDefinition *harness.Entry          `json:"harness_definition,omitempty"`
	Agent             runner.AgentConfig      `json:"agent"`
	BotAgents         map[Role]BotAgentConfig `json:"bot_agents,omitempty"`
	// BotPolicies is the per-house work selection and limits. An absent role
	// takes every item the house is given, with the bot's own defaults.
	BotPolicies    map[Role]BotPolicy `json:"bot_policies,omitempty"`
	Verify         []string           `json:"verify,omitempty"`
	MergePolicy    string             `json:"merge_policy"`
	PollSeconds    int                `json:"poll_seconds"`
	ReportSeconds  int                `json:"report_seconds"`
	MaxCycles      int                `json:"max_cycles"`
	Funnels        FunnelConfigs      `json:"funnels,omitempty"`
	SimplifierMode string             `json:"simplifier_mode,omitempty"`
	// BulletinSeconds is how often Mayor Bot writes the town bulletin when
	// something merged since the last one. Zero uses DefaultBulletinSeconds.
	BulletinSeconds int `json:"bulletin_seconds,omitempty"`
	// ReviewCloseSeverity is the least severe finding (P1, P2 or P3) that
	// still closes a pull request when it survives the second review. Findings
	// below it are filed as follow-up issues and the pull request merges.
	ReviewCloseSeverity string `json:"review_close_severity,omitempty"`
	// Budget bounds the agent attempts and agent minutes this town may start
	// in one accounting period. Nil leaves automation bounded only by capacity
	// and the existing per-task attempt limits.
	Budget *Budget `json:"budget,omitempty"`
	// QuietHours are this town's weekly quiet windows. Nil follows the
	// service default; an empty list opts this town out of it.
	QuietHours *[]QuietWindow `json:"quiet_hours,omitempty"`
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
	return Config{Repo: repo, MergePolicy: "bot", PollSeconds: 60, ReportSeconds: 1800, MaxCycles: 5, SimplifierMode: "suggest"}
}
func (c Config) BulletinSecondsOrDefault() int {
	if c.BulletinSeconds == 0 {
		return DefaultBulletinSeconds
	}
	return c.BulletinSeconds
}
func (c Config) SimplifierModeOrDefault() string {
	if c.SimplifierMode == "" {
		return "suggest"
	}
	return c.SimplifierMode
}

// DefaultReviewCloseSeverity closes on P1 and P2 and defers P3.
const DefaultReviewCloseSeverity = "P2"

func (c Config) ReviewCloseSeverityOrDefault() string {
	if c.ReviewCloseSeverity == "" {
		return DefaultReviewCloseSeverity
	}
	return c.ReviewCloseSeverity
}

// ValidSeverity accepts the review-bot rating scale.
func ValidSeverity(s string) bool { return s == "P1" || s == "P2" || s == "P3" }

// Blocking reports whether a finding of this severity keeps a pull request from
// merging under the town's close threshold. An unrated finding is treated as
// blocking: the reviewer could not bound it, so the town does not either.
func Blocking(severity, threshold string) bool {
	rank := func(s string) int {
		switch s {
		case "P1":
			return 1
		case "P2":
			return 2
		case "P3":
			return 3
		}
		return 0
	}
	if !ValidSeverity(threshold) {
		threshold = DefaultReviewCloseSeverity
	}
	return rank(severity) <= rank(threshold)
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
	if !ValidRepo(c.Repo) {
		return fmt.Errorf("repository must be OWNER/REPO")
	}
	if c.MergePolicy != "bot" && c.MergePolicy != "manual" && c.MergePolicy != "all" {
		return fmt.Errorf("merge policy must be bot, manual, or all")
	}
	if c.BulletinSeconds != 0 && c.BulletinSeconds < 600 {
		return errors.New("bulletin_seconds must be at least 600")
	}
	if c.PollSeconds < 10 || c.ReportSeconds < 60 || c.MaxCycles < 1 || c.MaxCycles > 20 {
		return fmt.Errorf("poll must be at least 10 seconds, reports at least 60 seconds, and cycles between 1 and 20")
	}
	if c.Branch != "" && !ValidBranch(c.Branch) {
		return fmt.Errorf("invalid branch")
	}
	if mode := c.SimplifierModeOrDefault(); mode != "suggest" && mode != "auto" {
		return fmt.Errorf("simplifier mode must be suggest or auto")
	}
	if !ValidSeverity(c.ReviewCloseSeverityOrDefault()) {
		return fmt.Errorf("review close severity must be P1, P2 or P3")
	}
	for role, policy := range c.BotPolicies {
		if !ValidRole(role) {
			return fmt.Errorf("work policy names an unknown house: %q", role)
		}
		if err := policy.Validate(role); err != nil {
			return fmt.Errorf("%s policy: %w", role, err)
		}
	}
	if err := c.Budget.Validate(); err != nil {
		return err
	}
	if c.QuietHours != nil {
		if err := ValidateQuietHours(*c.QuietHours); err != nil {
			return err
		}
	}
	if err := c.Funnels.Validate(); err != nil {
		return err
	}
	return nil
}

// PublicConfig deliberately excludes agent environment values and command arguments.
type PublicConfig struct {
	Repo            string                        `json:"repo"`
	Branch          string                        `json:"branch"`
	MergePolicy     string                        `json:"merge_policy"`
	MaxCycles       int                           `json:"max_cycles"`
	Harness         string                        `json:"harness"`
	Model           string                        `json:"model"`
	Effort          string                        `json:"effort"`
	HarnessVersion  string                        `json:"harness_version,omitempty"`
	BotAgents       map[Role]PublicBotAgentConfig `json:"bot_agents"`
	Funnels         []PublicFunnelConfig          `json:"funnels,omitempty"`
	SimplifierMode  string                        `json:"simplifier_mode"`
	BulletinSeconds int                           `json:"bulletin_seconds"`
	// ReviewCloseSeverity is the least severe finding that closes a pull
	// request after its second review.
	ReviewCloseSeverity string            `json:"review_close_severity"`
	Budget              *Budget           `json:"budget,omitempty"`
	WorkPolicies        []PublicBotPolicy `json:"work_policies"`
	// QuietHours is the town's own schedule: null follows the service
	// default, and an empty list opts out of it.
	QuietHours *[]QuietWindow `json:"quiet_hours"`
}

type PublicBotAgentConfig struct {
	Harness        string `json:"harness"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	HarnessVersion string `json:"harness_version,omitempty"`
	Inherited      bool   `json:"inherited"`
}
type Worker struct {
	Recovery *WorkerRecovery       `json:"recovery,omitempty"`
	Agent    *PublicBotAgentConfig `json:"agent,omitempty"`
	Run      *WorkerRun            `json:"run,omitempty"`
	Role     Role                  `json:"role"`
	Enabled  bool                  `json:"enabled"`
	Status   string                `json:"status"`
	Phase    string                `json:"phase"`
	Task     string                `json:"task"`
	Error    string                `json:"error,omitempty"`
	Updated  time.Time             `json:"updated"`
	Next     time.Time             `json:"next,omitempty"`
	Logs     []Log                 `json:"logs"`
	// RetryRequested asks the next release dispatch to lift the release bot's
	// exhausted attempt budget through its worker API before running.
	RetryRequested bool `json:"retry_requested,omitempty"`
}

// WorkerRecovery preserves an unresolved dispatch without exposing its private
// process paths or pretending that an absent process proves no write occurred.
type WorkerRecovery struct {
	TaskID  string    `json:"task_id,omitempty"`
	Base    string    `json:"base,omitempty"`
	Head    string    `json:"head,omitempty"`
	Started time.Time `json:"started"`
	Detail  string    `json:"detail"`
}

// WorkerRun records dispatch identity before sending work. An interrupted
// dispatch becomes a recovery hold; it is never used to adopt a process.
type WorkerRun struct {
	Bot      string    `json:"bot"`
	Version  string    `json:"version"`
	Command  string    `json:"command"`
	Hash     string    `json:"hash"`
	PID      int       `json:"pid"`
	Socket   string    `json:"socket"`
	Started  time.Time `json:"started"`
	Deadline time.Time `json:"deadline"`
	Issue    int       `json:"issue,omitempty"`
	PR       int       `json:"pr,omitempty"`
	BaseSHA  string    `json:"base_sha,omitempty"`
	HeadSHA  string    `json:"head_sha,omitempty"`
	Mode     string    `json:"mode,omitempty"`
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
	if role == Simplifier {
		if r.Mode != "suggest" && r.Mode != "auto" {
			return errors.New("invalid simplifier run mode")
		}
		if (r.Issue > 0 && r.PR > 0) || (r.BaseSHA != "" && r.PR == 0) || (r.HeadSHA != "" && r.PR == 0) {
			return errors.New("invalid simplifier run target")
		}
	} else if role == Repo {
		if r.Mode != "inventory" && r.Mode != "full" {
			return errors.New("invalid repo run mode")
		}
		if r.Issue > 0 || r.PR > 0 || r.BaseSHA != "" || r.HeadSHA != "" {
			return errors.New("a repository inventory names no issue, pull request or revision")
		}
	} else if role == Hall {
		switch r.Mode {
		case "judge":
			if (r.Issue > 0 && r.PR > 0) || (r.HeadSHA != "" && r.PR == 0) || (r.BaseSHA != "" && r.PR == 0) {
				return errors.New("invalid mayor judgment target")
			}
		case "bulletin":
			if r.Issue > 0 || r.PR > 0 || r.BaseSHA != "" || r.HeadSHA != "" {
				return errors.New("a bulletin names no issue, pull request or revision")
			}
		default:
			return errors.New("invalid mayor run mode")
		}
	} else if r.Mode != "" {
		return errors.New("invalid worker run mode")
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
	// Severity is the reviewer's P1, P2 or P3 rating when one is known.
	Severity string `json:"severity,omitempty"`
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
		if f.State != "resolved" && f.State != "dismissed" && f.State != "deferred" {
			return false
		}
	}
	return true
}

// OpenFindings are the certified findings that still stand against the
// revision: open defects and anything the certifier could not check.
func (a *Audit) OpenFindings() []Finding {
	var open []Finding
	if a == nil {
		return open
	}
	for _, f := range a.Findings {
		if f.State == "open" || f.State == "uncertain" {
			open = append(open, f)
		}
	}
	return open
}

type Task struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Number   int    `json:"number,omitempty"`
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	Stage    string `json:"stage"`
	House    Role   `json:"house"`
	External bool   `json:"external"`
	// Labels are the repository labels the last inventory observed. Town
	// applies its own label filters against them so the queue it shows an
	// operator is the queue the house will take from.
	Labels          []string          `json:"labels,omitempty"`
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
	Simplification  *Simplification   `json:"simplification,omitempty"`
	// Severities maps evidence IDs to the reviewer's P1, P2 or P3 rating.
	Severities map[string]string `json:"severities,omitempty"`
	// FollowUps are findings below the close threshold that survived the
	// second review. They are filed as issues once the pull request merges.
	FollowUps []Finding `json:"follow_ups,omitempty"`
	// Requeue names the pull request Town closed after review. Issue-bot's
	// next run on this issue starts over and never counts that PR again.
	Requeue int `json:"requeue,omitempty"`
	// Closes counts the times Town decided to close this pull request; it
	// tells one requeue comment from the next when a reopened pull request
	// is closed again.
	Closes int `json:"closes,omitempty"`
	// BranchKept marks a pull request Town closed whose branch GitHub refused
	// to delete. Its issue waits until the branch is gone.
	BranchKept bool `json:"branch_kept,omitempty"`
	// Retired marks an external pull request whose review attempts on the
	// current revision were exhausted; the Mayor decides whether to try again.
	Retired bool `json:"retired,omitempty"`
	// Offbranch marks a pull request that now targets a branch this town does
	// not cover. It blocks the task, and is the record that lets Town release
	// its own block if the pull request is retargeted back.
	Offbranch bool `json:"offbranch,omitempty"`
	// DeferredUntil is an operator's snooze: before then Town starts no new
	// agent run and makes no merge for this task. DeferReason says why.
	// Both are operator state; nothing Town observes on GitHub changes them.
	DeferredUntil time.Time `json:"deferred_until,omitzero"`
	DeferReason   string    `json:"defer_reason,omitempty"`
}

// Deferred reports whether the operator's snooze still holds at now.
func (t *Task) Deferred(now time.Time) bool { return t.DeferredUntil.After(now) }

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

type Simplification struct {
	Mode     string `json:"mode"`
	Decision string `json:"decision"`
	Summary  string `json:"summary,omitempty"`
	Detail   string `json:"detail,omitempty"`
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
	// Bulletins is the work-completed feed Mayor Bot writes, oldest first.
	Bulletins []Bulletin      `json:"bulletins"`
	Outcomes  []OutcomeRecord `json:"outcomes"`
	Head      string          `json:"head"`
	// DefaultBranch is the repository default GitHub reported at the last
	// inventory. It is an observation, never configuration: Config.Branch stays
	// whatever the operator chose, or empty to follow this default.
	DefaultBranch string `json:"default_branch,omitempty"`
	// Health is what Repo Bot last reported about the branch this town covers.
	Health *BranchHealth `json:"health,omitempty"`
	// Budget is the measured agent spend for the accounting period in progress.
	Budget *BudgetLedger `json:"budget_ledger,omitempty"`
	// Quiet records that the scheduler last saw this town inside its quiet
	// hours, so entering and leaving them is announced once. The dispatch
	// gate reads the clock, never this flag.
	Quiet       bool      `json:"quiet,omitempty"`
	LastSync    time.Time `json:"last_sync"`
	LastRelease string    `json:"last_release"`
	Error       string    `json:"error,omitempty"`
}

// BranchHealth is Repo Bot's report on the branch this town covers: the checks
// GitHub reported on its head, and what the bot did about a failing one.
type BranchHealth struct {
	// State is "green", "pending", "unreported", "red", "repaired" when the bot
	// published a fix this run, or "unrepairable" when no further attempt on
	// this revision can land one.
	State    string    `json:"state"`
	Head     string    `json:"head"`
	Failing  []string  `json:"failing,omitempty"`
	Pushed   string    `json:"pushed,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	At       time.Time `json:"at,omitempty"`
}

// Healthy reports a branch nothing is owed on. An unreported branch is not a
// failure to repair, so it counts as healthy for scheduling.
func (h *BranchHealth) Healthy() bool {
	return h == nil || h.State == "green" || h.State == "pending" || h.State == "unreported"
}

func ValidBranchHealth(h *BranchHealth) bool {
	if h == nil {
		return true
	}
	switch h.State {
	case "green", "pending", "unreported", "red", "repaired", "unrepairable":
	default:
		return false
	}
	if h.Head != "" && !SHA(h.Head) {
		return false
	}
	return h.Pushed == "" || SHA(h.Pushed)
}

// ServiceConfig governs the single local scheduler across every town.
const DefaultMaxWorkers = 4
const MaximumMaxWorkers = 64

type ServiceConfig struct {
	MaxWorkers int `json:"max_workers"`
	// QuietHours is the default quiet schedule for towns that set none.
	QuietHours []QuietWindow `json:"quiet_hours,omitempty"`
}

func (c ServiceConfig) Validate() error {
	if c.MaxWorkers < 1 || c.MaxWorkers > MaximumMaxWorkers {
		return fmt.Errorf("max_workers must be between 1 and %d", MaximumMaxWorkers)
	}
	return ValidateQuietHours(c.QuietHours)
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
}

func NewState(demo bool) State {
	return State{ServiceConfig: ServiceConfig{MaxWorkers: DefaultMaxWorkers}, Format: 1, Demo: demo, Towns: map[string]*Town{}, Events: []Event{}}
}
func (s *State) Add(c Config) (*Town, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	id := strings.ToLower(c.Repo)
	if t := s.Towns[id]; t != nil {
		if !t.Deleted {
			return nil, fmt.Errorf("town already exists")
		}
		if c.Branch == "" {
			// Adding names no branch; the restored town keeps the branch its
			// recovery records were made on.
			c.Branch = t.Config.Branch
		}
		if err := t.Restore(c); err != nil {
			return nil, err
		}
		return t, nil
	}
	t := &Town{ID: id, Config: c, Workers: map[Role]*Worker{}, Tasks: map[string]*Task{}, Owned: map[int]Ownership{}, Intents: map[int]*Intent{}, FunnelIntents: map[string]*WriteIntent{}, FunnelSyncs: map[FunnelID]*FunnelSync{}, Reports: []Report{}, Bulletins: []Bulletin{}, Outcomes: []OutcomeRecord{}}
	for _, r := range Roles {
		t.Workers[r] = &Worker{Role: r, Enabled: r == Repo, Status: "paused", Task: "Ready when you are", Logs: []Log{}}
	}
	s.Towns[id] = t
	return t, nil
}

// Restore revives a deleted town under the complete configuration c.
// Ownership, worktrees, tasks and uncertain writes are retained; restoration
// never resumes automation or pending issue submissions. An initialized town
// cannot be restored onto a different named branch in place; an empty branch
// follows the repository default.
func (t *Town) Restore(c Config) error {
	if !t.Deleted {
		return fmt.Errorf("town %s is not deleted", t.ID)
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if t.Initialized && c.Branch != "" && c.Branch != t.Branch() {
		return fmt.Errorf("cannot restore town %s from branch %s onto %s; use a separate state directory", t.ID, t.Branch(), c.Branch)
	}
	t.Config = c
	t.Deleted = false
	for r, w := range t.Workers {
		w.Enabled, w.Status, w.Next = r == Repo, "paused", time.Time{}
	}
	return nil
}

// Branch is the branch this town actually works on: the operator's choice when
// they made one, otherwise the repository default observed at the last inventory.
func (t *Town) Branch() string {
	if t.Config.Branch != "" {
		return t.Config.Branch
	}
	return t.DefaultBranch
}

// markFiltered tags the tasks a house's work policy excludes, and counts
// them, so a client can tell the repository's inventory apart from the work
// this town will actually pick up. The task stays in the snapshot: an
// operator has to be able to see what their filter is holding back.
func (t *Town) markFiltered(public map[string]any) {
	if len(t.Config.BotPolicies) == 0 {
		public["filtered_tasks"] = 0
		return
	}
	tasks, _ := public["tasks"].(map[string]any)
	filtered := 0
	for id, task := range t.Tasks {
		if t.Config.eligibleUnderPolicy(task.House, task) {
			continue
		}
		filtered++
		if entry, ok := tasks[id].(map[string]any); ok {
			entry["policy_excluded"] = true
		}
	}
	public["filtered_tasks"] = filtered
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
func clone[T any](v T) T               { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }
func (s State) Public() map[string]any { return s.PublicAt(time.Now()) }

// PublicAt projects state for clients as of now. The budget view is derived
// rather than stored, so a period that rolled over while nothing ran still
// reads as an empty new period.
func (s State) PublicAt(now time.Time) map[string]any {
	b, _ := json.Marshal(s)
	var public map[string]any
	_ = json.Unmarshal(b, &public)
	for id, t := range s.Towns {
		if t.Deleted {
			delete(public["towns"].(map[string]any), id)
			continue
		}
		public["towns"].(map[string]any)[id].(map[string]any)["config"] = t.PublicConfig()
		public["towns"].(map[string]any)[id].(map[string]any)["budget"] = t.BudgetState(now)
		quiet := t.QuietState(now, s.ServiceConfig.QuietHours)
		public["towns"].(map[string]any)[id].(map[string]any)["quiet_hours"] = quiet
		if quiet.Active {
			t.markQuiet(public["towns"].(map[string]any)[id].(map[string]any))
		}
		t.markFiltered(public["towns"].(map[string]any)[id].(map[string]any))
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
	return PublicConfig{Repo: c.Repo, Branch: c.Branch, MergePolicy: c.MergePolicy, MaxCycles: c.MaxCycles, Harness: c.harness(), Model: c.Agent.Model, Effort: c.Agent.Effort, HarnessVersion: version, BotAgents: bots, Funnels: funnels, SimplifierMode: c.SimplifierModeOrDefault(), BulletinSeconds: c.BulletinSecondsOrDefault(), ReviewCloseSeverity: c.ReviewCloseSeverityOrDefault(), Budget: c.Budget, WorkPolicies: c.PublicPolicies(), QuietHours: c.QuietHours}
}

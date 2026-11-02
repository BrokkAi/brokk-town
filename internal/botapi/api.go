// Package botapi is the contract between the SlopCop Squad service and its
// bots. The service reaches a bot only through Bot: it never imports a bot
// package, reads a bot's files, or resolves a command for a bot to run. Every
// value that crosses the boundary is plain JSON, carries no path on the
// service's filesystem, and has no type shared with the service's persisted
// state, so a bot can run in its own process, container or machine.
package botapi

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"time"

	"github.com/BrokkAi/brokk-town/internal/harness"
)

// Version is the contract version. Additive changes keep it; a change that an
// older peer would misread raises it.
const Version = 1

// Bot is one bot as the service sees it.
type Bot interface {
	// Run performs one operation. emit receives progress and log events in
	// order while the operation runs; it is never called after Run returns. A
	// failed run may still return the part of its result it completed.
	Run(ctx context.Context, request Request, emit func(Event)) (Result, error)
	// Storage inventories the data the bot keeps for one workspace, and
	// removes the transcripts the request names.
	Storage(ctx context.Context, request StorageRequest) (StorageResult, error)
}

// Request is one bot operation.
type Request struct {
	// Town and Role name the workspace. The bot keeps that workspace's files
	// under its own data root (see Dirs); the service never sends a path.
	Town string `json:"town"`
	Role string `json:"role"`

	InventorySince  *time.Time `json:"inventory_since,omitempty"`
	InventoryBranch string     `json:"inventory_branch,omitempty"`
	Remote          string     `json:"remote"`
	Branch          string     `json:"branch"`
	Repo            string     `json:"repo"`
	Host            string     `json:"host"`
	Agent           Agent      `json:"agent"`
	Verify          []string   `json:"verify,omitempty"`
	Issue           int        `json:"issue,omitempty"`
	PR              int        `json:"pr,omitempty"`
	BaseSHA         string     `json:"base_sha,omitempty"`
	HeadSHA         string     `json:"head_sha,omitempty"`
	// RemoteAgent is the URL of an agent endpoint the service serves for this
	// run (unix:///path or http(s)://host/...). It replaces Agent.
	RemoteAgent string `json:"remote_agent,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Mode        string `json:"mode,omitempty"`
	// Retry asks Release Bot to lift its attempt budget before running.
	Retry bool `json:"retry,omitempty"`
	// SinceHead and Commits are the repo bot's inventory inputs: the branch
	// head the service last observed, and the revisions it still needs release
	// ancestry for.
	SinceHead string   `json:"since_head,omitempty"`
	Commits   []string `json:"commits,omitempty"`
	// SupersededPR asks the issue bot to start the issue over because the
	// service closed this pull request after review.
	SupersededPR int `json:"superseded_pr,omitempty"`
	// Arrival is the service's description of the item a Mayor judgment
	// decides; Since and Until bound the window a Mayor bulletin summarizes.
	Arrival json.RawMessage `json:"arrival,omitempty"`
	Since   *time.Time      `json:"since,omitempty"`
	Until   *time.Time      `json:"until,omitempty"`
	// Policy is the operator's work selection and limits for this unit.
	Policy *Policy `json:"policy,omitempty"`
}

// Agent selects the ACP agent a bot runs. It is a selection, not a launch: the
// bot resolves it in its own environment (see ResolveAgent), because a command
// resolved on the service's host means nothing inside another container.
type Agent struct {
	// Command is an operator-configured launch command, used as given.
	Command []string `json:"command,omitempty"`
	// Harness is a catalog entry the bot installs and launches when Command is
	// empty. HarnessName is the operator's selection it came from.
	Harness     *harness.Entry    `json:"harness,omitempty"`
	HarnessName string            `json:"harness_name,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	AuthMethod  string            `json:"auth_method,omitempty"`
	Mode        string            `json:"mode,omitempty"`
	Model       string            `json:"model,omitempty"`
	Effort      string            `json:"effort,omitempty"`
}

// Configured reports whether the selection names an agent at all.
func (a Agent) Configured() bool { return len(a.Command) > 0 || a.Harness != nil }

// Policy is the operator's work selection and limits for one unit.
type Policy struct {
	Labels        []string       `json:"labels,omitempty"`
	ExcludeLabels []string       `json:"exclude_labels,omitempty"`
	Only          int            `json:"only,omitempty"`
	Focus         string         `json:"focus,omitempty"`
	Limit         int            `json:"limit,omitempty"`
	Attempts      int            `json:"attempts,omitempty"`
	Verify        []string       `json:"verify,omitempty"`
	Release       *ReleasePolicy `json:"release,omitempty"`
}

// ReleasePolicy is the release cadence and gating Release Bot supports.
type ReleasePolicy struct {
	DailySeconds               int      `json:"daily_seconds,omitempty"`
	MinimumGapSeconds          int      `json:"minimum_gap_seconds,omitempty"`
	QuietSeconds               int      `json:"quiet_seconds,omitempty"`
	Burst                      int      `json:"burst,omitempty"`
	BurstWindowSeconds         int      `json:"burst_window_seconds,omitempty"`
	Triage                     *bool    `json:"triage,omitempty"`
	Preflight                  []string `json:"preflight,omitempty"`
	VerificationTimeoutSeconds int      `json:"verification_timeout_seconds,omitempty"`
	Workflows                  []string `json:"workflows,omitempty"`
	Assets                     []string `json:"assets,omitempty"`
}

// Event is one observation streamed while a run is in flight. Exactly one
// field is set.
type Event struct {
	Progress *Progress `json:"progress,omitempty"`
	Log      *Log      `json:"log,omitempty"`
}

// Progress is the bot's current phase and task.
type Progress struct {
	Phase string `json:"phase"`
	Task  string `json:"task"`
}

// Log is one diagnostic record. Attribute values are rendered as text.
type Log struct {
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// Result is what one run reports. Which typed field is set depends on the role
// and mode; the service rejects a result with a field its request did not ask
// for.
type Result struct {
	Jobs           map[int]*IssueJob `json:"jobs,omitempty"`
	Issue          *IssueResult      `json:"issue,omitempty"`
	Review         *ReviewResult     `json:"review,omitempty"`
	Simplification *Simplification   `json:"simplification,omitempty"`
	Judgment       *Judgment         `json:"judgment,omitempty"`
	Bulletin       *Bulletin         `json:"bulletin,omitempty"`
	Inventory      *Inventory        `json:"inventory,omitempty"`
	Health         *BranchHealth     `json:"health,omitempty"`
	Usage          *Usage            `json:"usage,omitempty"`
	CostUSD        *float64          `json:"cost_usd,omitempty"`
	// Transcripts are the agent session transcripts this run wrote.
	Transcripts []Transcript `json:"transcripts,omitempty"`
}

// IssueJob is Issue Bot's public summary of one issue, not its state schema.
type IssueJob struct {
	Status       string          `json:"status"`
	Failure      string          `json:"failure"`
	ClaimPending bool            `json:"claim_pending"`
	Tries        int             `json:"tries"`
	RetryAt      time.Time       `json:"retry_at"`
	URL          string          `json:"url"`
	Branch       string          `json:"branch"`
	Result       *IssueJobResult `json:"result,omitempty"`
}

type IssueJobResult struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// IssueResult names the pull requests an issue run owns.
type IssueResult struct {
	Owned []IssueOwnership `json:"owned"`
}

type IssueOwnership struct {
	PR     int    `json:"pr"`
	Branch string `json:"branch"`
	Issue  int    `json:"issue"`
}

// ReviewResult is a review bound to the exact revision it covered.
type ReviewResult struct {
	Status     string            `json:"status,omitempty"`
	Detail     string            `json:"detail,omitempty"`
	Complete   bool              `json:"complete"`
	Findings   map[string]string `json:"findings,omitempty"`
	Severities map[string]string `json:"severities,omitempty"`
	ExactBase  string            `json:"exact_base,omitempty"`
	ExactHead  string            `json:"exact_head,omitempty"`
}

type Simplification struct {
	Mode     string `json:"mode"`
	Decision string `json:"decision"`
	Summary  string `json:"summary,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Judgment is Mayor Bot's decision on one arrival.
type Judgment struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// Bulletin is Mayor Bot's summary of one window of completed work.
type Bulletin struct {
	Since   time.Time      `json:"since"`
	Until   time.Time      `json:"until"`
	Title   string         `json:"title"`
	Summary string         `json:"summary"`
	Items   []BulletinItem `json:"items"`
	Pulls   []int          `json:"pulls"`
}

type BulletinItem struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Pulls  []int  `json:"pulls"`
	Issues []int  `json:"issues,omitempty"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Transcript is one session transcript a run wrote. Path is slash-separated
// and relative to the workspace (see Dirs).
type Transcript struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

// StorageRequest asks for the inventory of one workspace. Verify names the
// transcripts whose digests the service needs; hashing every transcript would
// make an inventory cost as much as the history it measures. With Remove set,
// the bot instead removes those transcripts, each only if it still matches its
// digest and size and the bot is not running in that workspace.
type StorageRequest struct {
	Town   string       `json:"town"`
	Role   string       `json:"role"`
	Verify []string     `json:"verify,omitempty"`
	Remove []Transcript `json:"remove,omitempty"`
}

// StorageResult describes what the bot keeps for one workspace.
type StorageResult struct {
	Entries []StorageEntry `json:"entries"`
	// Incomplete means the inventory could not see everything; nothing in it
	// may be treated as removable.
	Incomplete bool             `json:"incomplete,omitempty"`
	Removed    []StorageRemoval `json:"removed,omitempty"`
}

// StorageEntry is one top-level item of a workspace. Path is slash-separated
// and relative to the workspace.
type StorageEntry struct {
	Path     string    `json:"path"`
	Kind     string    `json:"kind"` // "workspace", "worker state" or "transcript"
	Bytes    int64     `json:"bytes"`
	Files    int       `json:"files"`
	Modified time.Time `json:"modified"`
	// Digest is set for a transcript the request asked to verify, when the
	// bot could read it.
	Digest string `json:"digest,omitempty"`
	// Unsafe marks a symbolic link or a path that escapes the workspace.
	Unsafe bool `json:"unsafe,omitempty"`
	// Unreadable marks an entry that could not be measured.
	Unreadable bool `json:"unreadable,omitempty"`
}

type StorageRemoval struct {
	Path string `json:"path"`
	// Status is "removed", "retained" or "uncertain".
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Dirs names a workspace's checkout and state directories under a bot's data
// root. The service lays its own data out the same way, so an in-process bot
// sharing the service's root sees the same directories it always has.
func Dirs(root, town, role string) (checkout, state string, err error) {
	if !namePattern.MatchString(town) || !namePattern.MatchString(role) {
		return "", "", errors.New("workspace names must be short lowercase identifiers")
	}
	base := filepath.Join(root, "towns", town, role)
	return filepath.Join(base, "checkout"), filepath.Join(base, "state"), nil
}

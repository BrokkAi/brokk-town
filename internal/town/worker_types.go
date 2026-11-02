package town

import (
	"regexp"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

type workerIssueOwnership struct {
	PR     int    `json:"pr"`
	Branch string `json:"branch"`
	Issue  int    `json:"issue"`
}

type workerIssueResult struct {
	Owned []workerIssueOwnership `json:"owned"`
}

type workerReviewResult struct {
	Status     string            `json:"status,omitempty"`
	Detail     string            `json:"detail,omitempty"`
	Complete   bool              `json:"complete"`
	Findings   map[string]string `json:"findings,omitempty"`
	Severities map[string]string `json:"severities,omitempty"`
	ExactBase  string            `json:"exact_base,omitempty"`
	ExactHead  string            `json:"exact_head,omitempty"`
}

type workerResult struct {
	Jobs           map[int]*issueJobSummary `json:"jobs,omitempty"`
	Issue          *workerIssueResult       `json:"issue,omitempty"`
	Review         *workerReviewResult      `json:"review,omitempty"`
	Simplification *workerSimplification    `json:"simplification,omitempty"`
	Judgment       *Judgment                `json:"judgment,omitempty"`
	Bulletin       *Bulletin                `json:"bulletin,omitempty"`
	Inventory      *workerInventory         `json:"inventory,omitempty"`
	Health         *BranchHealth            `json:"health,omitempty"`
	Usage          *OutcomeUsage            `json:"usage,omitempty"`
	CostUSD        *float64                 `json:"cost_usd,omitempty"`
	// retried records that this run lifted the release bot's attempt budget.
	retried bool
	// transcripts are the session transcripts the bot reports writing.
	transcripts []botapi.Transcript
}

// workerInventory is the repo bot's observation of the repository. Its fields
// are the remote types Town already reconciles, so one observation is read once
// and applied without a translation layer in between.
type workerInventory struct {
	Incremental   bool            `json:"incremental,omitempty"`
	StartedAt     time.Time       `json:"started_at,omitempty"`
	Branch        string          `json:"branch"`
	DefaultBranch string          `json:"default_branch"`
	Head          string          `json:"head"`
	Issues        []RemoteIssue   `json:"issues"`
	Pulls         []Pull          `json:"pulls"`
	Releases      []RemoteRelease `json:"releases"`
	Commits       []RemoteCommit  `json:"commits,omitempty"`
	Released      map[string]bool `json:"released,omitempty"`
}

func (i workerInventory) snapshot() RepoSnapshot {
	return RepoSnapshot{
		Branch: i.Branch, DefaultBranch: i.DefaultBranch, Head: i.Head, Incremental: i.Incremental, StartedAt: i.StartedAt,
		Issues: i.Issues, Pulls: i.Pulls, Releases: i.Releases, Commits: i.Commits, Released: i.Released,
	}
}

type workerSimplification struct {
	Mode     string `json:"mode"`
	Decision string `json:"decision"`
	Summary  string `json:"summary,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

var workerBotNames = map[Role]string{
	Bug: "bug-bot", Feature: "feature-bot", Issue: "issue-bot", Review: "review-bot", Release: "release-bot", Simplifier: "simplifier-bot", Repo: "repo-bot", Hall: "mayor-bot",
}

var workerVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…[truncated]"
}

package repobot

import (
	"encoding/json"
	"time"
)

// The observation types below are what one repository read reports. They are
// the bot's own types now: Town consumes the values from Run directly instead
// of reading them back over a worker protocol.

// Inventory is one complete observation of the repository.
type Inventory struct {
	Incremental   bool            `json:"incremental,omitempty"`
	StartedAt     time.Time       `json:"started_at,omitempty"`
	Branch        string          `json:"branch"`
	DefaultBranch string          `json:"default_branch"`
	Head          string          `json:"head"`
	Issues        []Issue         `json:"issues"`
	Pulls         []Pull          `json:"pulls"`
	Releases      []Release       `json:"releases"`
	Commits       []Commit        `json:"commits,omitempty"`
	Released      map[string]bool `json:"released,omitempty"`
}

type Issue struct {
	Number  int             `json:"number"`
	Title   string          `json:"title"`
	Body    string          `json:"body"`
	URL     string          `json:"html_url"`
	State   string          `json:"state"`
	Locked  bool            `json:"locked"`
	Labels  []Label         `json:"labels,omitempty"`
	Pull    json.RawMessage `json:"pull_request,omitempty"`
	Updated time.Time       `json:"updated_at"`
}

// Label is one repository label. Only the name crosses into Town: it filters
// and displays on it, and the rest belongs to the repository.
type Label struct {
	Name string `json:"name"`
}

type Ref struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

type Pull struct {
	Number         int        `json:"number"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	URL            string     `json:"html_url"`
	State          string     `json:"state"`
	Draft          bool       `json:"draft"`
	Locked         bool       `json:"locked"`
	Labels         []Label    `json:"labels,omitempty"`
	Comments       int        `json:"comments"`
	ReviewComments int        `json:"review_comments"`
	Head           Ref        `json:"head"`
	Base           Ref        `json:"base"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommit    string     `json:"merge_commit_sha"`
	Updated        time.Time  `json:"updated_at"`
}

type Release struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	URL        string    `json:"html_url"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	At         time.Time `json:"published_at"`
}

type Commit struct {
	SHA    string `json:"sha"`
	URL    string `json:"html_url"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

// BranchHealth reports the state of the branch this inventory covers and what,
// if anything, the bot did about a failing one. Pushed names the commit the bot
// published; an empty value means the branch was not changed.
type BranchHealth struct {
	State    string   `json:"state"`
	Head     string   `json:"head"`
	Failing  []string `json:"failing,omitempty"`
	Pushed   string   `json:"pushed,omitempty"`
	Attempts int      `json:"attempts,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// Result is one finished repository run.
type Result struct {
	Inventory *Inventory    `json:"inventory,omitempty"`
	Health    *BranchHealth `json:"health,omitempty"`
}

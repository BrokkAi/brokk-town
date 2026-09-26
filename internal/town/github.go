package town

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

type RemoteIssue struct {
	Number  int             `json:"number"`
	Title   string          `json:"title"`
	Body    string          `json:"body"`
	URL     string          `json:"html_url"`
	State   string          `json:"state"`
	Locked  bool            `json:"locked"`
	Labels  []RemoteLabel   `json:"labels,omitempty"`
	Pull    json.RawMessage `json:"pull_request"`
	Updated time.Time       `json:"updated_at"`
}

// RemoteLabel is one repository label. Only the name is kept: Town filters on
// it and shows it, and the colour and description are the repository's business.
type RemoteLabel struct {
	Name string `json:"name"`
}

// LabelNames flattens observed labels into the form Town's filters compare.
func LabelNames(labels []RemoteLabel) []string {
	if len(labels) == 0 {
		return nil
	}
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		if name := strings.TrimSpace(l.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

type Ref struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}
type Pull struct {
	Number         int           `json:"number"`
	Title          string        `json:"title"`
	Body           string        `json:"body"`
	URL            string        `json:"html_url"`
	State          string        `json:"state"`
	Draft          bool          `json:"draft"`
	Locked         bool          `json:"locked"`
	Labels         []RemoteLabel `json:"labels,omitempty"`
	Comments       int           `json:"comments"`
	ReviewComments int           `json:"review_comments"`
	Head           Ref           `json:"head"`
	Base           Ref           `json:"base"`
	MergedAt       *time.Time    `json:"merged_at"`
	MergeCommit    string        `json:"merge_commit_sha"`
	User           struct {
		Login string `json:"login"`
	} `json:"user"`
	Updated time.Time `json:"updated_at"`
}
type RemoteRelease struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	URL        string    `json:"html_url"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	At         time.Time `json:"published_at"`
}
type RemoteCommit struct {
	SHA    string `json:"sha"`
	URL    string `json:"html_url"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}
type RepoSnapshot struct {
	Archived    map[string]bool // Cold identities already accounted for; never transmitted to a worker.
	Incremental bool
	StartedAt   time.Time
	// Branch is the branch this inventory covers: the town's configured branch
	// when it has one, otherwise DefaultBranch. DefaultBranch is what GitHub
	// currently reports as the repository default, which a rename can change.
	Branch, DefaultBranch, Head string
	Issues                      []RemoteIssue
	Pulls                       []Pull
	Releases                    []RemoteRelease
	Released                    map[string]bool
	Commits                     []RemoteCommit
	ObservedHeads               map[int]string
}
type Discussion struct {
	ID    string `json:"id"`
	Body  string `json:"body"`
	Path  string `json:"path,omitempty"`
	State string `json:"state,omitempty"`
}
type MergeGate struct {
	Head string `json:"headRefOid"`
	Base string `json:"baseRefOid"`
	// BaseRef is the branch the pull request currently targets. Retargeting a
	// pull request leaves its head and base commits untouched, so the SHAs
	// alone cannot tell Town the merge would land somewhere it does not cover.
	BaseRef       string `json:"baseRefName"`
	Draft         bool   `json:"isDraft"`
	State         string `json:"state"`
	Mergeable     string `json:"mergeable"`
	MergeState    string `json:"mergeStateStatus"`
	Review        string `json:"reviewDecision"`
	PolicyKnown   bool   `json:"-"`
	SquashAllowed bool   `json:"-"`
	MergeQueue    *struct {
		ID string `json:"id"`
	} `json:"mergeQueue"`
	Checks *struct {
		State string `json:"state"`
	} `json:"statusCheckRollup"`
}

// Allows reports whether this pull request may be merged by the town covering
// branch. The branch is checked against both observations Town holds, because a
// merge that lands outside the configured branch is a write the operator never
// authorized.
func (g MergeGate) Allows(p Pull, a *Audit, branch string) bool {
	if branch == "" || p.Base.Ref != branch || g.BaseRef != branch || !g.PolicyKnown || !g.SquashAllowed || g.MergeQueue != nil {
		return false
	}
	return a.Clean(g.Base, g.Head) && p.Head.SHA == g.Head && p.Base.SHA == g.Base && p.State == "open" && !p.Draft && !p.Locked && !g.Draft && g.State == "OPEN" && g.Mergeable == "MERGEABLE" && g.MergeState == "CLEAN" && (g.Checks == nil || g.Checks.State == "SUCCESS") && (g.Review == "" || g.Review == "APPROVED")
}

// GitHub is what Town itself still needs from GitHub: the writes it authorizes,
// and the exact-revision reads that decide them. Repository inventory belongs to
// Repo Bot, which reports it over the worker protocol.
type GitHub interface {
	Pull(context.Context, string, int) (Pull, error)
	Discussion(context.Context, string, int) ([]Discussion, error)
	Gate(context.Context, string, int) (MergeGate, error)
	Merge(context.Context, string, int, string) (string, error)
	CloseIssue(context.Context, string, int) error
	// ClosePull closes a pull request without merging it.
	ClosePull(context.Context, string, int) error
	// Comment posts one comment on an issue or pull request.
	Comment(context.Context, string, int, string) error
	// IssueComments lists the comment bodies on an issue or pull request.
	IssueComments(context.Context, string, int) ([]string, error)
	// DeleteBranch removes a branch Town's own bots created.
	DeleteBranch(context.Context, string, string) error
	// CreateIssue files a new issue.
	CreateIssue(context.Context, string, string, string) (RemoteIssue, error)
	Actor(context.Context) (string, error)
	Contains(context.Context, string, string, string) (bool, error)
}

type GitHubClient struct {
	// Timeout bounds each gh invocation; zero means DefaultGitHubTimeout.
	// Town's own GitHub reads and writes, such as the merge gate, run outside
	// any worker attempt, so without it a stalled gh would hold a house
	// indefinitely.
	Timeout time.Duration
}

// DefaultGitHubTimeout bounds one gh invocation when GitHubClient.Timeout is
// unset.
const DefaultGitHubTimeout = time.Minute

// gh runs one bounded gh command. When the bound expires, the error says so
// instead of reporting only the killed process.
func (g GitHubClient) gh(ctx context.Context, args ...string) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = DefaultGitHubTimeout
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	text, err := osrun.Run(bounded, "", nil, append([]string{"gh"}, args...)...)
	if err != nil && bounded.Err() != nil {
		if ctx.Err() != nil {
			return text, fmt.Errorf("gh %s stopped: %w", args[0], errors.Join(ctx.Err(), err))
		}
		return text, fmt.Errorf("gh %s timed out after %s: %w", args[0], timeout, errors.Join(context.DeadlineExceeded, err))
	}
	return text, err
}

func (g GitHubClient) api(ctx context.Context, method, path string, body any, out any) error {
	args := []string{"api", "--hostname", "github.com", "--method", method, path}
	if body != nil {
		f, err := os.CreateTemp("", "brokk-town-request-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if err = json.NewEncoder(f).Encode(body); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		args = append(args, "--input", f.Name())
	}
	text, err := g.gh(ctx, args...)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(text), out)
}
func pages[T any](ctx context.Context, g GitHubClient, path string) ([]T, error) {
	all := []T{}
	join := "?"
	if strings.Contains(path, "?") {
		join = "&"
	}
	for page := 1; page <= 1000; page++ {
		var batch []T
		if err := g.api(ctx, "GET", fmt.Sprintf("%s%sper_page=100&page=%d", path, join, page), nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
	return nil, errors.New("GitHub pagination limit reached; refusing an incomplete snapshot")
}
func (g GitHubClient) Pull(ctx context.Context, repo string, n int) (Pull, error) {
	var p Pull
	err := g.api(ctx, "GET", fmt.Sprintf("repos/%s/pulls/%d", repo, n), nil, &p)
	return p, err
}
func (g GitHubClient) Actor(ctx context.Context) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	err := g.api(ctx, "GET", "user", nil, &u)
	return u.Login, err
}
func (g GitHubClient) Discussion(ctx context.Context, repo string, n int) ([]Discussion, error) {
	out := []Discussion{}
	for _, source := range []struct{ path, prefix string }{{fmt.Sprintf("repos/%s/issues/%d/comments", repo, n), "comment"}, {fmt.Sprintf("repos/%s/pulls/%d/comments", repo, n), "inline"}, {fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, n), "review"}} {
		items, err := pages[struct {
			ID    int64  `json:"id"`
			Body  string `json:"body"`
			Path  string `json:"path"`
			State string `json:"state"`
		}](ctx, g, source.path)
		if err != nil {
			return nil, err
		}
		for _, v := range items {
			out = append(out, Discussion{fmt.Sprintf("%s:%d", source.prefix, v.ID), v.Body, v.Path, v.State})
		}
	}
	return out, nil
}
func (g GitHubClient) Gate(ctx context.Context, repo string, n int) (MergeGate, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || n <= 0 {
		return MergeGate{}, errors.New("merge gate requires a repository and pull request")
	}
	var reply struct {
		Data struct {
			Repository *struct {
				SquashAllowed *bool      `json:"squashMergeAllowed"`
				Pull          *MergeGate `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	// Query the API directly: older gh pr view versions do not expose
	// baseRefOid. One observation also binds checks and repository policy to
	// the PR revision, with no paginated list to mistake for complete evidence.
	err := g.api(ctx, "POST", "graphql", map[string]any{
		"query": mergeGateQuery, "variables": map[string]any{"owner": owner, "name": name, "number": n},
	}, &reply)
	if err != nil {
		return MergeGate{}, err
	}
	r := reply.Data.Repository
	if len(reply.Errors) != 0 || r == nil || r.Pull == nil || r.SquashAllowed == nil || !SHA(r.Pull.Head) || !SHA(r.Pull.Base) || r.Pull.BaseRef == "" {
		return MergeGate{}, errors.New("GitHub merge gate returned incomplete information; check repository access and retry")
	}
	r.Pull.PolicyKnown, r.Pull.SquashAllowed = true, *r.SquashAllowed
	return *r.Pull, nil
}

const mergeGateQuery = `query($owner:String!,$name:String!,$number:Int!) {
  repository(owner:$owner,name:$name) {
    squashMergeAllowed
    pullRequest(number:$number) {
      headRefOid baseRefOid baseRefName isDraft state mergeable mergeStateStatus reviewDecision
      mergeQueue { id }
      statusCheckRollup { state }
    }
  }
}`

func (g GitHubClient) Merge(ctx context.Context, repo string, n int, sha string) (string, error) {
	var result struct {
		Merged  bool   `json:"merged"`
		SHA     string `json:"sha"`
		Message string `json:"message"`
	}
	err := g.api(ctx, "PUT", fmt.Sprintf("repos/%s/pulls/%d/merge", repo, n), map[string]string{"sha": sha, "merge_method": "squash"}, &result)
	if err != nil {
		return "", err
	}
	if !result.Merged || !SHA(result.SHA) {
		return "", fmt.Errorf("merge not confirmed: %s", result.Message)
	}
	return result.SHA, nil
}

// CloseIssue retires an issue the town itself proposed, as "not planned".
// GitHub accepts a repeated close, so an uncertain response is safe to retry on
// the next inventory.
func (g GitHubClient) CloseIssue(ctx context.Context, repo string, n int) error {
	if err := g.api(ctx, "PATCH", fmt.Sprintf("repos/%s/issues/%d", repo, n), map[string]string{"state": "closed", "state_reason": "not_planned"}, nil); err != nil {
		return definiteRejection(err)
	}
	return nil
}

// RejectedError is a GitHub write the API definitively refused: a 4xx answer
// that says the write did not happen and will not on retry. Timeouts,
// transport failures, 5xx and rate limits stay plain errors, whose outcome is
// uncertain.
type RejectedError struct {
	Status int
	Err    error
}

func (e *RejectedError) Error() string { return e.Err.Error() }
func (e *RejectedError) Unwrap() error { return e.Err }

var ghHTTPStatus = regexp.MustCompile(`HTTP (\d{3})`)

// definiteRejection classifies a gh api failure, which reports the response
// status as "(HTTP 404)" on stderr.
func definiteRejection(err error) error {
	m := ghHTTPStatus.FindStringSubmatch(err.Error())
	if m == nil {
		return err
	}
	status, _ := strconv.Atoi(m[1])
	if status < 400 || status >= 500 || status == 408 || status == 429 || strings.Contains(strings.ToLower(err.Error()), "rate limit") {
		return err
	}
	return &RejectedError{Status: status, Err: err}
}

// The writes below classify a definite refusal as RejectedError, so the closer
// can tell a step that will never succeed from one worth retrying.
func (g GitHubClient) ClosePull(ctx context.Context, repo string, n int) error {
	if err := g.api(ctx, "PATCH", fmt.Sprintf("repos/%s/pulls/%d", repo, n), map[string]string{"state": "closed"}, nil); err != nil {
		return definiteRejection(err)
	}
	return nil
}
func (g GitHubClient) Comment(ctx context.Context, repo string, n int, body string) error {
	if err := g.api(ctx, "POST", fmt.Sprintf("repos/%s/issues/%d/comments", repo, n), map[string]string{"body": body}, nil); err != nil {
		return definiteRejection(err)
	}
	return nil
}

// IssueComments returns the bodies of the comments on an issue or pull
// request.
func (g GitHubClient) IssueComments(ctx context.Context, repo string, n int) ([]string, error) {
	items, err := pages[struct {
		Body string `json:"body"`
	}](ctx, g, fmt.Sprintf("repos/%s/issues/%d/comments", repo, n))
	if err != nil {
		return nil, definiteRejection(err)
	}
	bodies := make([]string, 0, len(items))
	for _, item := range items {
		bodies = append(bodies, item.Body)
	}
	return bodies, nil
}
func (g GitHubClient) DeleteBranch(ctx context.Context, repo, branch string) error {
	if !ValidBranch(branch) {
		return fmt.Errorf("invalid branch %q", branch)
	}
	err := g.api(ctx, "DELETE", fmt.Sprintf("repos/%s/git/refs/heads/%s", repo, branch), nil, nil)
	if err != nil && strings.Contains(err.Error(), "Reference does not exist") {
		return nil
	}
	if err != nil {
		return definiteRejection(err)
	}
	return nil
}
func Digest(v any) string { b, _ := json.Marshal(v); return Key(string(b)) }

// Contains proves the commit is an ancestor of the published tag. Publication
// timestamps do not prove which commits a release actually contains.
func (g GitHubClient) Contains(ctx context.Context, repo, sha, tag string) (bool, error) {
	if !SHA(sha) || tag == "" {
		return false, errors.New("invalid release ancestry query")
	}
	var result struct {
		Status string `json:"status"`
	}
	err := g.api(ctx, "GET", "repos/"+repo+"/compare/"+sha+"..."+url.PathEscape(tag), nil, &result)
	return result.Status == "ahead" || result.Status == "identical", err
}

func latestRelease(releases []RemoteRelease) *RemoteRelease {
	var latest *RemoteRelease
	for i := range releases {
		r := &releases[i]
		if !r.Draft && !r.Prerelease && (latest == nil || r.At.After(latest.At)) {
			latest = r
		}
	}
	return latest
}

func description(p Pull) string { return Digest([]string{p.Title, p.Body}) }

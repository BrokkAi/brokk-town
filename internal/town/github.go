package town

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
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
	Head       string `json:"headRefOid"`
	Base       string `json:"baseRefOid"`
	Draft      bool   `json:"isDraft"`
	State      string `json:"state"`
	Mergeable  string `json:"mergeable"`
	MergeState string `json:"mergeStateStatus"`
	Review     string `json:"reviewDecision"`
}

func (g MergeGate) Allows(p Pull, a *Audit) bool {
	return a.Clean(g.Base, g.Head) && p.Head.SHA == g.Head && p.Base.SHA == g.Base && p.State == "open" && !p.Draft && !p.Locked && !g.Draft && g.State == "OPEN" && g.Mergeable == "MERGEABLE" && g.MergeState == "CLEAN" && (g.Review == "" || g.Review == "APPROVED")
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
	// DeleteBranch removes a branch Town's own bots created.
	DeleteBranch(context.Context, string, string) error
	// CreateIssue files a new issue.
	CreateIssue(context.Context, string, string, string) (RemoteIssue, error)
	Actor(context.Context) (string, error)
	Contains(context.Context, string, string, string) (bool, error)
}

type GitHubClient struct{}

func (g GitHubClient) api(ctx context.Context, method, path string, body any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	args := []string{"gh", "api", "--hostname", "github.com", "--method", method, path}
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
	text, err := osrun.Run(ctx, "", nil, args...)
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
	var gate MergeGate
	raw, err := osrun.Run(ctx, "", nil, "gh", "pr", "view", fmt.Sprint(n), "--repo", repo, "--json", "headRefOid,baseRefOid,isDraft,state,mergeable,mergeStateStatus,reviewDecision")
	if err != nil {
		return gate, err
	}
	err = json.Unmarshal([]byte(raw), &gate)
	return gate, err
}
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
	return g.api(ctx, "PATCH", fmt.Sprintf("repos/%s/issues/%d", repo, n), map[string]string{"state": "closed", "state_reason": "not_planned"}, nil)
}
func (g GitHubClient) ClosePull(ctx context.Context, repo string, n int) error {
	return g.api(ctx, "PATCH", fmt.Sprintf("repos/%s/pulls/%d", repo, n), map[string]string{"state": "closed"}, nil)
}
func (g GitHubClient) Comment(ctx context.Context, repo string, n int, body string) error {
	return g.api(ctx, "POST", fmt.Sprintf("repos/%s/issues/%d/comments", repo, n), map[string]string{"body": body}, nil)
}
func (g GitHubClient) DeleteBranch(ctx context.Context, repo, branch string) error {
	if !ValidBranch(branch) {
		return fmt.Errorf("invalid branch %q", branch)
	}
	err := g.api(ctx, "DELETE", fmt.Sprintf("repos/%s/git/refs/heads/%s", repo, branch), nil, nil)
	if err != nil && strings.Contains(err.Error(), "Reference does not exist") {
		return nil
	}
	return err
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

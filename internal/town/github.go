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
	Pull    json.RawMessage `json:"pull_request"`
	Updated time.Time       `json:"updated_at"`
}
type Ref struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}
type Pull struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	URL         string     `json:"html_url"`
	State       string     `json:"state"`
	Draft       bool       `json:"draft"`
	Locked      bool       `json:"locked"`
	Head        Ref        `json:"head"`
	Base        Ref        `json:"base"`
	MergedAt    *time.Time `json:"merged_at"`
	MergeCommit string     `json:"merge_commit_sha"`
	User        struct {
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
	Branch, Head  string
	Issues        []RemoteIssue
	Pulls         []Pull
	Releases      []RemoteRelease
	Released      map[string]bool
	Commits       []RemoteCommit
	ObservedHeads map[int]string
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

type GitHub interface {
	Snapshot(context.Context, Config) (RepoSnapshot, error)
	Pull(context.Context, string, int) (Pull, error)
	Discussion(context.Context, string, int) ([]Discussion, error)
	Gate(context.Context, string, int) (MergeGate, error)
	Merge(context.Context, string, int, string) (string, error)
	Actor(context.Context) (string, error)
	Contains(context.Context, string, string, string) (bool, error)
	Changes(context.Context, string, string, string) ([]RemoteCommit, error)
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
func (g GitHubClient) Snapshot(ctx context.Context, c Config) (RepoSnapshot, error) {
	var out RepoSnapshot
	var metadata struct {
		Branch string `json:"default_branch"`
	}
	if err := g.api(ctx, "GET", "repos/"+c.Repo, nil, &metadata); err != nil {
		return out, err
	}
	out.Branch = c.Branch
	if out.Branch == "" {
		out.Branch = metadata.Branch
	}
	var branch struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := g.api(ctx, "GET", "repos/"+c.Repo+"/branches/"+url.PathEscape(out.Branch), nil, &branch); err != nil {
		return out, err
	}
	out.Head = branch.Commit.SHA
	var err error
	out.Issues, err = pages[RemoteIssue](ctx, g, "repos/"+c.Repo+"/issues?state=all&sort=updated&direction=desc")
	if err != nil {
		return out, err
	}
	out.Pulls, err = pages[Pull](ctx, g, "repos/"+c.Repo+"/pulls?state=all&sort=updated&direction=desc")
	if err != nil {
		return out, err
	}
	out.Releases, err = pages[RemoteRelease](ctx, g, "repos/"+c.Repo+"/releases")
	return out, err
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

func (g GitHubClient) Changes(ctx context.Context, repo, from, to string) ([]RemoteCommit, error) {
	if !SHA(from) || !SHA(to) {
		return nil, errors.New("invalid comparison revisions")
	}
	var commits []RemoteCommit
	for page := 1; page <= 1000; page++ {
		var result struct {
			Status  string         `json:"status"`
			Commits []RemoteCommit `json:"commits"`
		}
		err := g.api(ctx, "GET", fmt.Sprintf("repos/%s/compare/%s...%s?per_page=100&page=%d", repo, from, to, page), nil, &result)
		if err != nil {
			return nil, err
		}
		if result.Status != "ahead" && result.Status != "identical" {
			return nil, nil
		}
		commits = append(commits, result.Commits...)
		if len(result.Commits) < 100 {
			return commits, nil
		}
	}
	return nil, errors.New("commit comparison exceeded pagination limit")
}

func description(p Pull) string { return Digest([]string{p.Title, p.Body}) }

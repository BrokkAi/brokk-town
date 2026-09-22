package mayorbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BrokkAi/mayor-bot/internal/osrun"
)

// Issue is one GitHub issue with its comment bodies, as the Mayor reads it.
type Issue struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	URL         string          `json:"html_url"`
	State       string          `json:"state"`
	Labels      []Label         `json:"labels,omitempty"`
	Comments    []string        `json:"discussion,omitempty"`
	PullRequest json.RawMessage `json:"pull_request,omitempty"`
}
type Label struct {
	Name string `json:"name"`
}

// Pull is one GitHub pull request with its discussion. MergedAt is set only
// for merged pull requests; a closed unmerged one has none.
type Pull struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	URL         string     `json:"html_url"`
	State       string     `json:"state"`
	Draft       bool       `json:"draft"`
	Base        Ref        `json:"base"`
	Head        Ref        `json:"head"`
	Comments    int        `json:"comments"`
	Reviews     int        `json:"review_comments"`
	MergedAt    *time.Time `json:"merged_at,omitempty"`
	MergeCommit string     `json:"merge_commit_sha,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
	Discussion []string `json:"discussion,omitempty"`
}
type Ref struct {
	SHA  string `json:"sha"`
	Ref  string `json:"ref"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

// ChangedFile is one file a pull request touched, as GitHub reports it.
type ChangedFile struct {
	Path      string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type source interface {
	issue(context.Context, int) (Issue, error)
	pull(context.Context, int) (Pull, error)
	// mergedPulls lists pull requests into the configured branch that merged
	// after since and no later than until, oldest first.
	mergedPulls(context.Context, time.Time, time.Time) ([]Pull, error)
	files(context.Context, int) ([]ChangedFile, error)
}
type githubClient struct{ config Config }

func (g githubClient) api(ctx context.Context, endpoint string, result any, fields ...string) error {
	out, err := g.request(ctx, endpoint, fields...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), result)
}
func (g githubClient) request(ctx context.Context, endpoint string, fields ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	args := []string{"gh", "api", "--hostname", g.config.GitHub.Host, endpoint}
	return osrun.Run(ctx, "", nil, append(args, fields...)...)
}
func (g githubClient) path(suffix string) string { return "repos/" + g.config.GitHubRepo() + suffix }
func (g githubClient) issue(ctx context.Context, number int) (Issue, error) {
	var i Issue
	if err := g.api(ctx, g.path(fmt.Sprintf("/issues/%d", number)), &i); err != nil {
		return i, err
	}
	if i.Number != number || len(i.PullRequest) > 0 && string(i.PullRequest) != "null" || (i.State != "open" && i.State != "closed") {
		return i, errors.New("incomplete GitHub issue response")
	}
	comments, err := pages[struct {
		Body string `json:"body"`
	}](ctx, g, g.path(fmt.Sprintf("/issues/%d/comments", number)), url.Values{"sort": {"created"}, "direction": {"asc"}}, 100)
	if err != nil {
		return i, err
	}
	for _, c := range comments {
		i.Comments = append(i.Comments, c.Body)
	}
	return i, nil
}
func (g githubClient) pull(ctx context.Context, number int) (Pull, error) {
	var p Pull
	if err := g.api(ctx, g.path(fmt.Sprintf("/pulls/%d", number)), &p); err != nil {
		return p, err
	}
	if p.Number != number || p.Head.SHA == "" || p.Base.SHA == "" || (p.State != "open" && p.State != "closed") {
		return p, errors.New("incomplete GitHub pull response")
	}
	comments, err := pages[struct {
		Body string `json:"body"`
	}](ctx, g, g.path(fmt.Sprintf("/issues/%d/comments", number)), url.Values{"sort": {"created"}, "direction": {"asc"}}, 100)
	if err != nil {
		return p, err
	}
	for _, c := range comments {
		p.Discussion = append(p.Discussion, c.Body)
	}
	return p, nil
}

// mergedPulls walks closed pull requests newest-updated first and stops at the
// first one updated before the window opened: a merge updates the pull
// request, so nothing merged in the window can be older than that.
func (g githubClient) mergedPulls(ctx context.Context, since, until time.Time) ([]Pull, error) {
	var merged []Pull
	q := url.Values{"state": {"closed"}, "sort": {"updated"}, "direction": {"desc"}, "base": {g.config.Branch}}
	for page := 1; page <= 50; page++ {
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprint(page))
		var items []Pull
		if err := g.api(ctx, g.path("/pulls")+"?"+q.Encode(), &items); err != nil {
			return nil, err
		}
		if items == nil {
			return nil, errors.New("expected a GitHub array response")
		}
		done := len(items) < 100
		for _, p := range items {
			if p.UpdatedAt.Before(since) {
				done = true
				break
			}
			if p.Number < 1 || p.MergedAt == nil || !p.MergedAt.After(since) || p.MergedAt.After(until) {
				continue
			}
			merged = append(merged, p)
		}
		if done {
			sort.Slice(merged, func(i, j int) bool { return merged[i].MergedAt.Before(*merged[j].MergedAt) })
			return merged, nil
		}
	}
	return nil, errors.New("merged pull request history exceeded pagination limit; narrow the window")
}
func (g githubClient) files(ctx context.Context, number int) ([]ChangedFile, error) {
	return pages[ChangedFile](ctx, g, g.path(fmt.Sprintf("/pulls/%d/files", number)), url.Values{}, 3)
}
func pages[T any](ctx context.Context, g githubClient, path string, q url.Values, limit int) ([]T, error) {
	var all []T
	for page := 1; page <= limit; page++ {
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprint(page))
		var items []T
		if err := g.api(ctx, path+"?"+q.Encode(), &items); err != nil {
			return nil, err
		}
		if items == nil {
			return nil, errors.New("expected a GitHub array response")
		}
		all = append(all, items...)
		if len(items) < 100 {
			return all, nil
		}
	}
	return nil, errors.New("history exceeded pagination limit; refusing an incomplete read")
}

// linkedIssues finds the issues a pull request says it closes. Only closing
// keywords count; a bare mention is not a fix.
var closingKeyword = regexp.MustCompile(`(?i)\b(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)\s*:?\s+#(\d+)`)

func linkedIssues(body string) []int {
	seen := map[int]bool{}
	var numbers []int
	for _, m := range closingKeyword.FindAllStringSubmatch(body, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || seen[n] {
			continue
		}
		seen[n] = true
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	return numbers
}
func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "�") + "\n…truncated by mayor-bot…"
}

package simplifierbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BrokkAi/simplifier-bot/internal/osrun"
)

type Issue struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	URL         string          `json:"html_url"`
	State       string          `json:"state"`
	Comments    []string        `json:"discussion,omitempty"`
	PullRequest json.RawMessage `json:"pull_request,omitempty"`
}
type Pull struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	URL        string   `json:"html_url"`
	State      string   `json:"state"`
	Draft      bool     `json:"draft"`
	Base       Ref      `json:"base"`
	Head       Ref      `json:"head"`
	Comments   int      `json:"comments"`
	Reviews    int      `json:"review_comments"`
	Discussion []string `json:"discussion,omitempty"`
}
type Ref struct {
	SHA  string `json:"sha"`
	Ref  string `json:"ref"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}
type source interface {
	issues(context.Context) ([]Issue, error)
	issue(context.Context, int) (Issue, error)
	pull(context.Context, int) (Pull, error)
	create(context.Context, *Proposal, string) (*Issue, error)
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
func (g githubClient) issues(ctx context.Context) ([]Issue, error) {
	all, err := pages[Issue](ctx, g, g.path("/issues"), url.Values{"state": {"all"}, "sort": {"created"}, "direction": {"asc"}})
	if err != nil {
		return nil, err
	}
	byNumber := map[int]int{}
	var issues []Issue
	for _, i := range all {
		if len(i.PullRequest) > 0 && string(i.PullRequest) != "null" {
			continue
		}
		if i.Number < 1 || (i.State != "open" && i.State != "closed") {
			return nil, errors.New("incomplete GitHub issue response")
		}
		if _, exists := byNumber[i.Number]; exists {
			return nil, errors.New("unstable issue pagination; retry snapshot")
		}
		byNumber[i.Number] = len(issues)
		issues = append(issues, i)
	}
	comments, err := pages[struct {
		IssueURL string `json:"issue_url"`
		Body     string `json:"body"`
	}](ctx, g, g.path("/issues/comments"), url.Values{"sort": {"created"}, "direction": {"asc"}})
	if err != nil {
		return nil, err
	}
	for _, c := range comments {
		u, err := url.Parse(c.IssueURL)
		if err != nil {
			return nil, errors.New("invalid comment issue URL")
		}
		n, err := strconv.Atoi(u.Path[strings.LastIndex(u.Path, "/")+1:])
		if err != nil {
			return nil, errors.New("invalid comment issue URL")
		}
		if at, ok := byNumber[n]; ok {
			issues[at].Comments = append(issues[at].Comments, c.Body)
		}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	return issues, nil
}
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
	}](ctx, g, g.path(fmt.Sprintf("/issues/%d/comments", number)), url.Values{"sort": {"created"}, "direction": {"asc"}})
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
	}](ctx, g, g.path(fmt.Sprintf("/issues/%d/comments", number)), url.Values{"sort": {"created"}, "direction": {"asc"}})
	if err != nil {
		return p, err
	}
	for _, c := range comments {
		p.Discussion = append(p.Discussion, c.Body)
	}
	return p, nil
}
func pages[T any](ctx context.Context, g githubClient, path string, q url.Values) ([]T, error) {
	var all []T
	for page := 1; page <= 10000; page++ {
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
	return nil, errors.New("history exceeded pagination limit; refusing incomplete comparison")
}
func marker(requestID string) string { return "<!-- simplifier-bot: " + requestID + " -->" }
func proposalBody(p *Proposal, commit string) string {
	return fmt.Sprintf("## Complexity or value concern\n\n%s\n\n## Simpler alternative\n\n%s\n\n## Affected subsystems\n\n%s\n\n## Evidence\n\n%s\n\nInvestigated at commit `%s` by simplifier-bot.\n\n%s\n", p.Concern, p.Alternative, strings.Join(p.Subsystems, "\n"), strings.Join(p.Evidence, "\n\n"), commit, marker(p.RequestID))
}
func (g githubClient) create(ctx context.Context, p *Proposal, commit string) (*Issue, error) {
	fields := []string{"--method", "POST", "--include", "-f", "title=" + p.Title, "-f", "body=" + proposalBody(p, commit)}
	for _, l := range g.config.Labels {
		fields = append(fields, "-f", "labels[]="+l)
	}
	out, runErr := g.request(ctx, g.path("/issues"), fields...)
	i, err := createdResponse(out, runErr)
	if err != nil {
		return nil, err
	}
	if err := validateCreated(g.config, p, i); err != nil {
		return nil, err
	}
	return i, nil
}

type rejectedCreateError struct{ error }

func (e *rejectedCreateError) Unwrap() error { return e.error }
func createdResponse(out string, runErr error) (*Issue, error) {
	if runErr != nil {
		return nil, runErr
	}
	var headers, body string
	if raw, rest, ok := strings.Cut(out, "\n\n"); ok {
		headers, body = raw, strings.TrimPrefix(rest, "\n")
	} else {
		body = out
	}
	if !strings.Contains(strings.ToLower(headers), " http 2") && headers != "" {
		return nil, errors.New("issue creation returned a non-success response")
	}
	var i Issue
	if err := json.Unmarshal([]byte(body), &i); err != nil || i.Number < 1 {
		return nil, errors.New("issue creation returned an incomplete response")
	}
	return &i, nil
}
func validateCreated(cfg Config, p *Proposal, i *Issue) error {
	if i == nil || i.Number < 1 || (i.State != "open" && i.State != "closed") || !strings.Contains(i.Body, marker(p.RequestID)) ||
		!strings.EqualFold(i.URL, fmt.Sprintf("https://%s/%s/issues/%d", cfg.GitHub.Host, cfg.GitHubRepo(), i.Number)) || strings.TrimSpace(i.Title) == "" {
		return errors.New("GitHub did not confirm the proposed issue")
	}
	return nil
}
func tempJSON(value any) (string, error) {
	f, err := os.CreateTemp("", "simplifier-request-*.json")
	if err != nil {
		return "", err
	}
	if err := json.NewEncoder(f).Encode(value); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

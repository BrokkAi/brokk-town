package town

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type IssueRequest struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	Status  string    `json:"status"`
	Number  int       `json:"number,omitempty"`
	URL     string    `json:"url,omitempty"`
	Detail  string    `json:"detail,omitempty"`
	Created time.Time `json:"created"`
	Next    time.Time `json:"next,omitempty"`
}

func (r IssueRequest) validate() error {
	if len(r.ID) < 16 || len(r.ID) > 64 || strings.Trim(r.ID, "0123456789abcdef-") != "" {
		return errors.New("a unique request ID is required")
	}
	if r.Kind != "feature" && r.Kind != "bug" {
		return errors.New("request kind must be feature or bug")
	}
	if strings.TrimSpace(r.Title) == "" || utf8.RuneCountInString(r.Title) > 256 || strings.ContainsAny(r.Title, "\r\n\x00") {
		return errors.New("issue title must be 1–256 characters on one line")
	}
	if strings.TrimSpace(r.Body) == "" || len(r.Body) > 30000 || strings.ContainsRune(r.Body, 0) || strings.Contains(r.Body, "<!-- brokk-town-request:") {
		return errors.New("describe the request in 1–30000 bytes, without reserved Town markers")
	}
	return nil
}

func requestMarker(id string) string { return "<!-- brokk-town-request:" + id + " -->" }
func (r IssueRequest) githubBody() string {
	heading := "Feature request"
	if r.Kind == "bug" {
		heading = "Bug report"
	}
	return "## " + heading + "\n\n" + r.Body + "\n\n" + requestMarker(r.ID)
}

type IssuePublisher interface {
	CreateIssue(context.Context, string, string, string) (RemoteIssue, error)
	FindRequest(context.Context, string, string) (*RemoteIssue, error)
}

func (g GitHubClient) CreateIssue(ctx context.Context, repo, title, body string) (RemoteIssue, error) {
	var issue RemoteIssue
	err := g.api(ctx, "POST", "repos/"+repo+"/issues", map[string]string{"title": title, "body": body}, &issue)
	return issue, err
}

func (g GitHubClient) FindRequest(ctx context.Context, repo, id string) (*RemoteIssue, error) {
	issues, err := pages[RemoteIssue](ctx, g, "repos/"+repo+"/issues?state=all&sort=created&direction=desc")
	if err != nil {
		return nil, err
	}
	var found *RemoteIssue
	for _, issue := range issues {
		if len(issue.Pull) == 0 && strings.Contains(issue.Body, requestMarker(id)) {
			if found != nil {
				return nil, errors.New("multiple issues carry this request ID; inspect GitHub")
			}
			v := issue
			found = &v
		}
	}
	return found, nil
}

func (s *Supervisor) SubmitRequest(id string, input IssueRequest) (*IssueRequest, error) {
	input.Title, input.Body = strings.TrimSpace(input.Title), strings.TrimSpace(input.Body)
	if err := input.validate(); err != nil {
		return nil, err
	}
	var result *IssueRequest
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		if !st.Demo && s.Publisher == nil {
			return errors.New("issue publishing is unavailable")
		}
		if old := t.Requests[input.ID]; old != nil {
			if old.Kind != input.Kind || old.Title != input.Title || old.Body != input.Body {
				return errors.New("request ID already belongs to a different submission")
			}
			result = clone(old)
			return nil
		}
		pending := 0
		for _, r := range t.Requests {
			if r.Status == "queued" || r.Status == "uncertain" {
				pending++
			}
		}
		if pending >= 50 {
			return errors.New("finish or reconcile pending submissions before adding more")
		}
		r := &IssueRequest{ID: input.ID, Kind: input.Kind, Title: input.Title, Body: input.Body, Created: s.now(), Status: "queued"}
		if t.Requests == nil {
			t.Requests = map[string]*IssueRequest{}
		}
		t.Requests[r.ID] = r
		st.Event(id, "request", "operator", "hall", "", "Issue submission queued: "+r.Title, s.now())
		if st.Demo {
			n := 10000
			for t.Tasks[fmt.Sprintf("issue:%d", n)] != nil {
				n++
			}
			confirmRequest(st, t, r, RemoteIssue{Number: n, Title: r.Title, Body: r.githubBody(), State: "open"}, s.now())
		}
		result = clone(r)
		return nil
	})
	if err == nil {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return result, err
}

// Recheck only reads GitHub. An uncertain POST is never blindly sent again.
func (s *Supervisor) RecheckRequest(id, requestID string) error {
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		r := t.Requests[requestID]
		if r == nil || r.Status != "uncertain" {
			return errors.New("request does not need reconciliation")
		}
		r.Next = time.Time{}
		return nil
	})
	if err == nil {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return err
}

func (s *Supervisor) scheduleRequests(ctx context.Context, t *Town) {
	if s.Publisher == nil {
		return
	}
	var pending []*IssueRequest
	for _, r := range t.Requests {
		if (r.Status == "queued" || r.Status == "uncertain") && !r.Next.After(s.now()) {
			pending = append(pending, r)
		}
	}
	if len(pending) == 0 {
		return
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Created.Before(pending[j].Created) })
	key := t.ID + ":requests"
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[key] != nil {
		return
	}
	child, cancel := context.WithTimeout(ctx, 2*time.Minute)
	s.running[key] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { cancel(); s.mu.Lock(); delete(s.running, key); s.mu.Unlock() }()
		s.publishRequest(child, t.ID, pending[0].ID)
	}()
}

func (s *Supervisor) publishRequest(ctx context.Context, id, requestID string) {
	var request IssueRequest
	var repo string
	create := false
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted || st.Demo || ctx.Err() != nil {
			return context.Canceled
		}
		r := t.Requests[requestID]
		if r == nil || (r.Status != "queued" && r.Status != "uncertain") {
			return context.Canceled
		}
		create = r.Status == "queued"
		// Commit the possibility of a write before sending even one byte to GitHub.
		r.Status = "uncertain"
		r.Detail = "Waiting for GitHub confirmation. This submission will not be posted twice."
		r.Next = s.now().Add(5 * time.Minute)
		request, repo = *r, t.Config.Repo
		return nil
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.fail(err)
		}
		return
	}
	var issue *RemoteIssue
	if create {
		var result RemoteIssue
		result, err = s.Publisher.CreateIssue(ctx, repo, request.Title, request.githubBody())
		if err == nil {
			issue = &result
		}
	} else {
		issue, err = s.Publisher.FindRequest(ctx, repo, request.ID)
	}
	// Treat malformed success responses as uncertain too. Preserve the durable ID.
	valid := err == nil && issue != nil && issue.Number > 0 && len(issue.Pull) == 0 &&
		(issue.State == "open" || issue.State == "closed") && strings.Contains(issue.Body, requestMarker(request.ID)) &&
		strings.EqualFold(issue.URL, fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue.Number))
	s.update(func(st *State) error {
		t := st.Towns[id]
		r := t.Requests[requestID]
		if valid {
			confirmRequest(st, t, r, *issue, s.now())
		} else {
			r.Detail = "GitHub has not confirmed this submission. Check the repository before filing it again. Town will keep looking for its receipt."
			r.Next = s.now().Add(5 * time.Minute)
		}
		return nil
	})
}

func confirmRequest(st *State, t *Town, r *IssueRequest, issue RemoteIssue, now time.Time) {
	r.Status, r.Number, r.URL, r.Detail = "confirmed", issue.Number, issue.URL, "Issue created and added to the workshop queue."
	r.Next = time.Time{}
	id := fmt.Sprintf("issue:%d", issue.Number)
	if t.Tasks[id] == nil {
		stage := "queued"
		if issue.State == "closed" {
			stage = "closed"
		}
		t.Tasks[id] = &Task{ID: id, Kind: "issue", Number: issue.Number, Title: issue.Title, URL: issue.URL, Description: issue.Body, House: Issue, Stage: stage, Updated: now}
		st.Event(t.ID, "delivery", "hall", "issue", id, "New "+r.Kind+": "+r.Title, now)
	}
	if t.Workers[Issue].Enabled {
		t.Workers[Issue].Next = time.Time{}
	}
}

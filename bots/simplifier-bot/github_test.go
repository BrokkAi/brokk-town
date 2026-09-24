package simplifierbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitHubPaginationAndDiscussion(t *testing.T) {
	cfg := DefaultConfig()
	var page []Issue
	for n := 1; n <= 100; n++ {
		page = append(page, Issue{Number: n, Title: fmt.Sprint(n), State: "open"})
	}
	page[0].PullRequest = json.RawMessage(`{"url":"pull"}`)
	first, _ := json.Marshal(page)
	for _, scenario := range []string{"complete", "duplicate", "null", "failed page", "failed comments"} {
		t.Run(scenario, func(t *testing.T) {
			q := url.Values{"state": {"all"}, "sort": {"created"}, "direction": {"asc"}, "per_page": {"100"}, "page": {"1"}}
			routes := map[string]fixtureResponse{"repos/o/r/issues?" + q.Encode(): {Body: string(first)}}
			q.Set("page", "2")
			second := fixtureResponse{Body: `[{"number":101,"state":"closed","title":"Old issue"}]`}
			switch scenario {
			case "duplicate":
				second.Body = `[{"number":100,"state":"open"}]`
			case "null":
				second.Body = "null"
			case "failed page":
				second.Exit = 1
			}
			routes["repos/o/r/issues?"+q.Encode()] = second
			comments := fixtureResponse{Body: `[{"issue_url":"https://api.github.com/repos/o/r/issues/101","body":"Old discussion"}]`}
			if scenario == "failed comments" {
				comments.Exit = 1
			}
			routes["repos/o/r/issues/comments"] = comments
			installFixture(t, &cfg, "unused", fixtureGitHub{Routes: routes})
			got, err := (githubClient{cfg}).issues(context.Background())
			if scenario != "complete" {
				if err == nil {
					t.Fatal("incomplete history accepted")
				}
				return
			}
			if err != nil || len(got) != 100 || got[0].Number != 2 || got[99].Number != 101 || got[99].State != "closed" || len(got[99].Comments) != 1 || got[99].Comments[0] != "Old discussion" {
				t.Fatalf("issues %+v: %v", got, err)
			}
		})
	}
}

func TestCreatedResponseRequiresConfirmedHTTPCreate(t *testing.T) {
	body := `{"number":42,"state":"open","title":"Proposal"}`
	for _, newline := range []string{"\n", "\r\n"} {
		for _, version := range []string{"HTTP/1.1", "HTTP/2.0"} {
			raw := version + " 201 Created" + newline + "Content-Type: application/json" + newline + newline + body
			if got, err := createdResponse(raw, nil); err != nil || got.Number != 42 {
				t.Fatalf("normal gh response %q: %+v %v", raw, got, err)
			}
		}
	}
	for _, raw := range []string{
		"", body, "HTTP/2.0 200 OK\n\n" + body,
		"HTTP/2.0 500 Error\n\n" + body,
		"invalid 201 Created\n\n" + body,
		"HTTP/2.0 201 Created\nBrokenHeader\n\n" + body,
		"HTTP/2.0 201 Created\n\n{}",
		"HTTP/2.0 201 Created\n\n" + body + " trailing",
	} {
		if _, err := createdResponse(raw, nil); err == nil {
			t.Fatalf("unconfirmed response accepted: %q", raw)
		}
	}
	if _, err := createdResponse("HTTP/2.0 201 Created\n\n"+body, errors.New("transport lost")); err == nil {
		t.Fatal("failed command treated as success")
	}
}

func TestValidateCreatedIssueIdentity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GitHub.Repo = "o/r"
	proposal := &Proposal{RequestID: strings.Repeat("a", 32)}
	good := Issue{Number: 42, Title: "Proposal", Body: marker(proposal.RequestID), State: "open", URL: "https://github.com/o/r/issues/42"}
	if err := validateCreated(cfg, proposal, &good); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Issue){
		func(i *Issue) { i.URL = "https://github.com/other/r/issues/42" },
		func(i *Issue) { i.Body = "missing marker" },
		func(i *Issue) { i.State = "unknown" },
		func(i *Issue) { i.Number = 0 },
		func(i *Issue) { i.Title = "" },
		func(i *Issue) { i.PullRequest = json.RawMessage(`{"url":"pull"}`) },
	} {
		bad := good
		change(&bad)
		if err := validateCreated(cfg, proposal, &bad); err == nil {
			t.Fatalf("invalid create confirmed: %+v", bad)
		}
	}
}

func TestRecoveryDoesNotTrustIncompleteMarkerMatch(t *testing.T) {
	remote, _ := originRepo(t)
	cfg := testConfig(t, remote)
	root := installFixture(t, &cfg, "must not run", fixtureGitHub{})
	s := newState(cfg)
	s.NextScan = time.Now().Add(time.Hour)
	s.Proposals = []*Proposal{{RequestID: strings.Repeat("a", 32), Status: "posting", Title: "Proposal", Concern: "Concern", Alternative: "Alternative", Subsystems: []string{"src"}, Evidence: []string{"Evidence"}}}
	if err := writeState(cfg, s); err != nil {
		t.Fatal(err)
	}
	// A marker alone, without the returned issue's identity, is not a receipt.
	body, _ := json.Marshal([]Issue{{Number: 42, State: "open", Body: marker(s.Proposals[0].RequestID)}})
	saveGitHubFixture(t, root, fixtureGitHub{Routes: map[string]fixtureResponse{"repos/o/r/issues": {Body: string(body)}, "repos/o/r/issues/comments": {Body: "[]"}}})
	if err := Run(context.Background(), cfg, nil, true); err == nil {
		t.Fatal("incomplete marker match accepted")
	}
	saved, err := ReadState(cfg)
	if err != nil || saved.Proposals[0].Status != "posting" {
		t.Fatalf("uncertainty discarded: %+v %v", saved, err)
	}
	if len(fixtureLines[fixturePrompt](t, filepath.Join(root, "prompts"))) != 0 {
		t.Fatal("recovery started agent")
	}
}

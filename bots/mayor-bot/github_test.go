package mayorbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"
)

func TestGitHubMergedWindowPagination(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)
	atStart, atEnd, after := since, until, until.Add(time.Second)
	var first []Pull
	for n := 1; n <= 100; n++ {
		first = append(first, Pull{Number: n, UpdatedAt: after})
	}
	first[0].MergedAt = &atStart // exclusive
	first[1].MergedAt = &atEnd   // inclusive
	first[2].MergedAt = &after   // too late
	firstJSON, _ := json.Marshal(first)
	middle := since.Add(time.Minute)
	secondJSON, _ := json.Marshal([]Pull{{Number: 101, MergedAt: &middle, UpdatedAt: middle}})
	for _, scenario := range []string{"complete", "null", "failed second page", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := DefaultConfig()
			q := url.Values{"state": {"closed"}, "sort": {"updated"}, "direction": {"desc"}, "base": {cfg.Branch}, "per_page": {"100"}, "page": {"1"}}
			routes := map[string]fixtureResponse{"repos/o/r/pulls?" + q.Encode(): {Body: string(firstJSON)}}
			q.Set("page", "2")
			second := fixtureResponse{Body: string(secondJSON)}
			switch scenario {
			case "null":
				second.Body = "null"
			case "failed second page":
				second.Exit = 1
			case "malformed":
				second.Body = "{"
			}
			routes["repos/o/r/pulls?"+q.Encode()] = second
			installFixture(t, &cfg, "unused", fixtureGitHub{Routes: routes})
			got, err := (githubClient{cfg}).mergedPulls(context.Background(), since, until)
			if scenario != "complete" {
				if err == nil {
					t.Fatal("incomplete window accepted")
				}
				return
			}
			if err != nil || len(got) != 2 || got[0].Number != 101 || got[1].Number != 2 {
				t.Fatalf("wrong window/order: %+v %v", got, err)
			}
		})
	}
}

func TestGitHubCommentsAndPaginationLimit(t *testing.T) {
	cfg := DefaultConfig()
	var comments []map[string]string
	for n := 0; n < 100; n++ {
		comments = append(comments, map[string]string{"body": fmt.Sprint(n)})
	}
	first, _ := json.Marshal(comments)
	q := url.Values{"sort": {"created"}, "direction": {"asc"}, "per_page": {"100"}, "page": {"1"}}
	routes := map[string]fixtureResponse{
		"repos/o/r/issues/7":                        {Body: `{"number":7,"state":"open"}`},
		"repos/o/r/issues/7/comments?" + q.Encode(): {Body: string(first)},
	}
	q.Set("page", "2")
	routes["repos/o/r/issues/7/comments?"+q.Encode()] = fixtureResponse{Body: `[{"body":"last"}]`}
	installFixture(t, &cfg, "unused", fixtureGitHub{Routes: routes})
	g := githubClient{cfg}
	issue, err := g.issue(context.Background(), 7)
	if err != nil || len(issue.Comments) != 101 || issue.Comments[100] != "last" {
		t.Fatalf("comments %+v %v", issue, err)
	}
	q.Del("page")
	q.Del("per_page")
	if _, err := pages[map[string]string](context.Background(), g, "repos/o/r/issues/7/comments", q, 1); err == nil {
		t.Fatal("truncated pagination accepted")
	}
}

package repobot

import (
	"errors"
	"strings"
	"testing"

	"github.com/BrokkAi/repo-bot/internal/worker"
)

func TestObservationSummarizesARun(t *testing.T) {
	head := strings.Repeat("a", 40)
	result := worker.Result{
		Inventory: &worker.Inventory{Head: head, Issues: []worker.Issue{{State: "open"}, {State: "closed"}, {State: "open", Pull: []byte(`{}`)}}, Pulls: []worker.Pull{{State: "open"}, {State: "closed"}}, Releases: []worker.Release{{Tag: "v1"}}, Commits: []worker.Commit{{SHA: head}}},
		Health:    &worker.BranchHealth{State: healthRepaired, Head: head, Failing: []string{"test"}, Pushed: strings.Repeat("b", 40), Attempts: 1},
	}
	o := observation(result, nil)
	if o.Head != head || o.OpenIssues != 1 || o.OpenPulls != 1 || o.Releases != 1 || o.NewCommits != 1 || o.Health != healthRepaired || o.Pushed == "" || o.Attempts != 1 || o.Error != "" {
		t.Fatalf("observation %+v", o)
	}
	failed := observation(worker.Result{}, errors.New("GitHub unavailable"))
	if failed.Error != "GitHub unavailable" || observationStatus(failed) != "error" {
		t.Fatalf("failed observation %+v", failed)
	}
}

func TestHistoryReportsTheLatestObservation(t *testing.T) {
	head := strings.Repeat("c", 40)
	observations := []Observation{
		{Head: strings.Repeat("a", 40), Health: healthRed, Failing: []string{"lint"}, OpenIssues: 1},
		{Head: strings.Repeat("b", 40), Health: healthRepaired, Pushed: head},
		{Error: "GitHub unavailable"},
		{Head: head, Health: healthGreen, OpenIssues: 4, OpenPulls: 2, Releases: 3},
	}
	p := history(observations, Progress{Phase: "waiting"})
	want := ObservationCounts{Health: healthGreen, OpenIssues: 4, OpenPulls: 2, Releases: 3, Observations: 4, Red: 1, Repairs: 1}
	if p.Counts != want || p.Commit != head || len(p.Items) != 4 {
		t.Fatalf("progress %+v", p)
	}
	if p.Items[2].Status != "error" || !strings.Contains(p.Items[2].Body, "Error\nGitHub unavailable") {
		t.Fatalf("error item %+v", p.Items[2])
	}
	if !strings.Contains(p.Items[0].Title, "failing lint") || !strings.Contains(p.Items[0].Body, "Failing checks\nlint") {
		t.Fatalf("red item %+v", p.Items[0])
	}
}

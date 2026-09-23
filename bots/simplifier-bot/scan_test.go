package simplifierbot

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

type scanAgent struct{ runs *int }

func (a scanAgent) Execute(context.Context, string) (string, error) {
	*a.runs++
	return `SIMPLIFY_SCAN {"summary":"One candidate","proposals":[{"title":"Remove plugin registry","concern":"unused","alternative":"direct construction","subsystems":["internal/registry"],"evidence":["no callers"]}]}`, nil
}

type scanSource struct{ created *int }

func (scanSource) issues(context.Context) ([]Issue, error)   { return []Issue{}, nil }
func (scanSource) issue(context.Context, int) (Issue, error) { return Issue{}, nil }
func (scanSource) pull(context.Context, int) (Pull, error)   { return Pull{}, nil }
func (s scanSource) create(_ context.Context, p *Proposal, _ string) (*Issue, error) {
	*s.created++
	return &Issue{URL: fmt.Sprintf("https://github.com/o/r/issues/%d", *s.created)}, nil
}

// A fresh workspace has no saved next scan, so its first scan is due at once;
// after it, the next scan waits for the poll interval.
func TestScanRunsWhenDueAndWaitsForTheInterval(t *testing.T) {
	remote, _ := originRepo(t)
	cfg := testConfig(t, remote)
	cfg.GitHub.Repo = "o/r"
	runs, created := 0, 0
	e := engine{config: cfg, source: scanSource{&created}, log: slog.Default(), agent: func(Config) Agent { return scanAgent{&runs} }, sleep: pause, observe: func(Progress) {}}
	s := newState(cfg)
	if err := e.scan(context.Background(), s, false); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || created != 1 || len(s.Proposals) != 1 || s.Proposals[0].Status != "submitted" {
		t.Fatalf("first scan: runs %d created %d proposals %+v", runs, created, s.Proposals)
	}
	if !s.NextScan.After(time.Now()) {
		t.Fatalf("next scan not scheduled: %v", s.NextScan)
	}
	advance(t, remote)
	if err := e.scan(context.Background(), s, false); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || created != 1 {
		t.Fatalf("scan ran before its interval: runs %d created %d", runs, created)
	}
	s.NextScan = time.Now().Add(-time.Second)
	if err := e.scan(context.Background(), s, false); err != nil {
		t.Fatal(err)
	}
	if runs != 2 || created != 2 {
		t.Fatalf("due scan did not run: runs %d created %d", runs, created)
	}
}

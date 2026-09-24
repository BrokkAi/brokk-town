package simplifierbot

import (
	"strings"
	"testing"
	"time"
)

func TestProgressReportsSavedProposals(t *testing.T) {
	var got Progress
	e := engine{observe: func(p Progress) { got = p }}
	wake := time.Now().Add(time.Hour)
	s := &State{LastCommit: strings.Repeat("a", 40), NextScan: wake, Proposals: []*Proposal{
		{RequestID: "1", Title: "Remove loader", Concern: "Duplicates modules", Alternative: "Use go modules", Subsystems: []string{"loader/"}, Evidence: []string{"loader.go:12"}, Status: "submitted", URL: "https://github.com/o/r/issues/4"},
		{RequestID: "2", Title: "Drop wrapper", Status: "dry_run"},
		{RequestID: "3", Title: "Merge configs", Status: "posting"},
	}}
	e.report(s, "waiting", "Next simplification scan")
	if got.Phase != "waiting" || got.Commit != s.LastCommit || !got.WakeAt.Equal(wake) || len(got.Items) != 3 {
		t.Fatalf("progress %+v", got)
	}
	if got.Counts != (ProposalCounts{Found: 3, Filed: 1, Pending: 1, DryRun: 1}) {
		t.Fatalf("counts %+v", got.Counts)
	}
	body := got.Items[0].Body
	for _, want := range []string{"Concern\nDuplicates modules", "Simpler alternative\nUse go modules", "Subsystems\nloader/", "Evidence\nloader.go:12"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(got.Items[1].Body, "Concern") {
		t.Fatalf("empty sections shown: %q", got.Items[1].Body)
	}
}

func TestProgressKeepsTheLatestProposals(t *testing.T) {
	var got Progress
	e := engine{observe: func(p Progress) { got = p }}
	s := &State{}
	for i := 0; i < 250; i++ {
		s.Proposals = append(s.Proposals, &Proposal{RequestID: string(rune('a' + i%26)), Title: "p", Status: "submitted"})
	}
	e.report(s, "complete", "")
	if len(got.Items) != 200 || got.Counts.Found != 250 || got.Counts.Filed != 250 {
		t.Fatalf("items %d counts %+v", len(got.Items), got.Counts)
	}
}

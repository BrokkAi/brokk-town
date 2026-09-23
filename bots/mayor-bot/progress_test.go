package mayorbot

import (
	"strings"
	"testing"
	"time"
)

func TestHistoryReportsRecordedBulletins(t *testing.T) {
	since := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	s := &State{Bulletins: []BulletinRecord{
		{Since: since, Until: since.Add(24 * time.Hour), At: since.Add(24 * time.Hour), Title: "Faster startup", Items: 2, Pulls: []int{4, 5}, Summary: "Two changes.",
			Entries: []BulletinItem{{Kind: "feature", Title: "Startup is faster", Detail: "Cold start halves.", Pulls: []int{4}, Issues: []int{9}}, {Kind: "fix", Title: "Logs are quieter", Pulls: []int{5}}}},
		{Since: since.Add(24 * time.Hour), Until: since.Add(48 * time.Hour), At: since.Add(48 * time.Hour), Title: "Older record", Items: 1, Pulls: []int{6}},
	}}
	p := history(s, Progress{Phase: "waiting", Task: "Next bulletin"})
	if p.Phase != "waiting" || len(p.Items) != 2 || p.Counts != (BulletinCounts{Bulletins: 2, Items: 3, Pulls: 3}) {
		t.Fatalf("progress %+v", p)
	}
	body := p.Items[0].Body
	for _, want := range []string{"Window\n", "Summary\nTwo changes.", "FEATURE · Startup is faster (#4, issue #9)\nCold start halves.", "FIX · Logs are quieter (#5)"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if !strings.Contains(p.Items[1].Body, "Pull requests\n#6") {
		t.Fatalf("record without entries lost its pulls: %q", p.Items[1].Body)
	}
	if empty := history(nil, Progress{Phase: "starting"}); len(empty.Items) != 0 || empty.Phase != "starting" {
		t.Fatalf("empty history %+v", empty)
	}
}

func TestLatestUntilIsTheNewestWindowEnd(t *testing.T) {
	base := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	s := &State{Bulletins: []BulletinRecord{{Until: base.Add(48 * time.Hour)}, {Until: base}}}
	if got := latestUntil(s); !got.Equal(base.Add(48 * time.Hour)) {
		t.Fatalf("latest %v", got)
	}
}

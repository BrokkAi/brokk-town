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

func TestWindowStartContinuesAnUnrecordedWindow(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	poll := 24 * time.Hour
	if got := windowStart(nil, time.Time{}, now, poll); !got.Equal(now.Add(-poll)) {
		t.Fatalf("fresh start %v", got)
	}
	// An empty window records no bulletin; the next one must start where it
	// ended rather than one interval before now, which would skip the merges
	// between the two windows.
	emptyEnd := now.Add(-poll - time.Minute)
	if got := windowStart(nil, emptyEnd, now, poll); !got.Equal(emptyEnd) {
		t.Fatalf("after an empty window %v, want %v", got, emptyEnd)
	}
	// A failed first window still owes everything from its start.
	failedStart := now.Add(-2*poll - time.Hour)
	if got := windowStart(nil, failedStart, now, poll); !got.Equal(failedStart) {
		t.Fatalf("after a failed window %v, want %v", got, failedStart)
	}
	recorded := &State{Bulletins: []BulletinRecord{{Until: now.Add(-time.Hour)}}}
	if got := windowStart(recorded, emptyEnd, now, poll); !got.Equal(now.Add(-time.Hour)) {
		t.Fatalf("a newer recorded bulletin wins: %v", got)
	}
	if got := windowStart(recorded, now.Add(-time.Minute), now, poll); !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("a newer empty window wins: %v", got)
	}
}

package mayorbot

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Progress is an owned snapshot of the Mayor, suitable for a live display.
// Worker duties report only Phase and Task; a standalone Watch also reports the
// latest 200 recorded bulletins, oldest first, and counts covering them all.
type Progress struct {
	Phase, Task string
	WakeAt      time.Time
	Items       []ItemProgress
	Counts      BulletinCounts
}

type ItemProgress struct {
	ID, Title, Status, URL, Body string
}

type BulletinCounts struct {
	Bulletins, Items, Pulls int
}

type progressKey struct{}

// WithProgress observes progress synchronously. The callback should return promptly.
func WithProgress(ctx context.Context, observe func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, observe)
}
func observe(ctx context.Context) func(Progress) {
	if fn, _ := ctx.Value(progressKey{}).(func(Progress)); fn != nil {
		return fn
	}
	return func(Progress) {}
}

// history fills a progress snapshot with the recorded bulletins.
func history(s *State, p Progress) Progress {
	p.Items, p.Counts = nil, BulletinCounts{}
	if s == nil {
		return p
	}
	for i, r := range s.Bulletins {
		p.Counts.Bulletins++
		p.Counts.Items += r.Items
		p.Counts.Pulls += len(r.Pulls)
		if i < len(s.Bulletins)-200 {
			continue
		}
		p.Items = append(p.Items, ItemProgress{
			ID:     r.Since.UTC().Format(time.RFC3339) + "/" + r.Until.UTC().Format(time.RFC3339),
			Title:  r.Title,
			Status: r.Until.Local().Format("Jan 02"),
			Body:   bulletinDetails(r),
		})
	}
	return p
}

func bulletinDetails(r BulletinRecord) string {
	sections := []string{fmt.Sprintf("Window\n%s → %s", r.Since.Local().Format("2006-01-02 15:04"), r.Until.Local().Format("2006-01-02 15:04"))}
	if r.Summary != "" {
		sections = append(sections, "Summary\n"+r.Summary)
	}
	for _, item := range r.Entries {
		var refs []string
		for _, n := range item.Pulls {
			refs = append(refs, fmt.Sprintf("#%d", n))
		}
		for _, n := range item.Issues {
			refs = append(refs, fmt.Sprintf("issue #%d", n))
		}
		text := strings.ToUpper(item.Kind) + " · " + item.Title
		if len(refs) > 0 {
			text += " (" + strings.Join(refs, ", ") + ")"
		}
		if item.Detail != "" {
			text += "\n" + item.Detail
		}
		sections = append(sections, text)
	}
	if len(r.Entries) == 0 && len(r.Pulls) > 0 {
		var refs []string
		for _, n := range r.Pulls {
			refs = append(refs, fmt.Sprintf("#%d", n))
		}
		sections = append(sections, "Pull requests\n"+strings.Join(refs, ", "))
	}
	return strings.Join(sections, "\n\n")
}

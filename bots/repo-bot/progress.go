package repobot

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Progress is an owned snapshot of the bot, suitable for a live display. Worker
// runs report only Phase and Task; a standalone Watch also reports the saved
// observations, oldest first, and counts covering them.
type Progress struct {
	Phase, Task, Commit string
	WakeAt              time.Time
	Items               []ItemProgress
	Counts              ObservationCounts
}

type ItemProgress struct {
	ID, Title, Status, URL, Body string
}

// ObservationCounts describes the latest observation and the saved history.
type ObservationCounts struct {
	Health                          string
	OpenIssues, OpenPulls, Releases int
	Observations, Red, Repairs      int
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

// history fills a progress snapshot with the saved observations.
func history(observations []Observation, p Progress) Progress {
	p.Items, p.Counts = nil, ObservationCounts{}
	for _, o := range observations {
		p.Counts.Observations++
		switch o.Health {
		case healthRed, healthUnrepairable:
			p.Counts.Red++
		case healthRepaired:
			p.Counts.Repairs++
		}
		p.Items = append(p.Items, ItemProgress{ID: o.At.UTC().Format(time.RFC3339Nano), Title: observationTitle(o), Status: observationStatus(o), Body: observationDetails(o)})
	}
	if n := len(observations); n > 0 {
		last := observations[n-1]
		p.Counts.Health = observationStatus(last)
		p.Counts.OpenIssues, p.Counts.OpenPulls, p.Counts.Releases = last.OpenIssues, last.OpenPulls, last.Releases
		if p.Commit == "" {
			p.Commit = last.Head
		}
	}
	return p
}

func observationStatus(o Observation) string {
	if o.Error != "" && o.Health == "" {
		return "error"
	}
	return o.Health
}

func observationTitle(o Observation) string {
	if o.Head == "" {
		return o.At.Local().Format("Jan 02 15:04") + " · " + o.Error
	}
	title := fmt.Sprintf("%s · %s · %d issues · %d PRs", o.At.Local().Format("Jan 02 15:04"), short(o.Head), o.OpenIssues, o.OpenPulls)
	if o.NewCommits > 0 {
		title += fmt.Sprintf(" · +%d commits", o.NewCommits)
	}
	if len(o.Failing) > 0 {
		title += " · failing " + strings.Join(o.Failing, ", ")
	}
	return title
}

func observationDetails(o Observation) string {
	sections := []string{"Observed\n" + o.At.Local().Format("2006-01-02 15:04:05")}
	if o.Head != "" {
		sections = append(sections, fmt.Sprintf("Branch head\n%s\n\nRepository\n%d open issues · %d open pull requests · %d releases · %d new commits", o.Head, o.OpenIssues, o.OpenPulls, o.Releases, o.NewCommits))
	}
	if o.Health != "" {
		sections = append(sections, "Health\n"+strings.ToUpper(o.Health))
	}
	if len(o.Failing) > 0 {
		sections = append(sections, "Failing checks\n"+strings.Join(o.Failing, "\n"))
	}
	if o.Attempts > 0 {
		sections = append(sections, fmt.Sprintf("Repair attempts on this revision\n%d", o.Attempts))
	}
	if o.Pushed != "" {
		sections = append(sections, "Published repair\n"+o.Pushed)
	}
	if o.Detail != "" {
		sections = append(sections, "Detail\n"+o.Detail)
	}
	if o.Error != "" {
		sections = append(sections, "Error\n"+o.Error)
	}
	return strings.Join(sections, "\n\n")
}

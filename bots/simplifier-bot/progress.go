package simplifierbot

import (
	"context"
	"strings"
	"time"
)

// Progress is an owned snapshot of the bot, suitable for a live display. Items
// holds the latest 200 saved proposals, oldest first, and Counts covers them
// all. Assessments report only Phase and Task.
type Progress struct {
	Phase, Task, Commit string
	WakeAt              time.Time
	Items               []ItemProgress
	Counts              ProposalCounts
}

type ItemProgress struct {
	ID, Title, Status, URL, Body string
}

type ProposalCounts struct {
	Found, Filed, Pending, DryRun int
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

// report publishes the scan's phase together with every saved proposal.
func (e engine) report(s *State, phase, task string) {
	if e.observe == nil {
		return
	}
	p := Progress{Phase: phase, Task: task, Commit: s.LastCommit, WakeAt: s.NextScan}
	for i, proposal := range s.Proposals {
		p.Counts.Found++
		switch proposal.Status {
		case "submitted":
			p.Counts.Filed++
		case "pending", "posting":
			p.Counts.Pending++
		case "dry_run":
			p.Counts.DryRun++
		}
		if i >= len(s.Proposals)-200 {
			p.Items = append(p.Items, ItemProgress{ID: proposal.RequestID, Title: proposal.Title, Status: proposal.Status, URL: proposal.URL, Body: proposalDetails(proposal)})
		}
	}
	e.observe(p)
}

func proposalDetails(p *Proposal) string {
	var sections []string
	for _, section := range [][2]string{
		{"Concern", p.Concern},
		{"Simpler alternative", p.Alternative},
		{"Subsystems", strings.Join(p.Subsystems, "\n")},
		{"Evidence", strings.Join(p.Evidence, "\n")},
	} {
		if strings.TrimSpace(section[1]) != "" {
			sections = append(sections, section[0]+"\n"+section[1])
		}
	}
	return strings.Join(sections, "\n\n")
}

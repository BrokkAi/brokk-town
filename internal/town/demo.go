package town

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Demo is a closed simulation: it never receives a GitHub client or worker runner.
func Demo(ctx context.Context, store *Store) error { return runDemo(ctx, store, 8*time.Second) }
func runDemo(ctx context.Context, store *Store, interval time.Duration) error {
	if !store.Snapshot().Demo {
		return fmt.Errorf("demo requires an isolated demo store")
	}
	if err := store.Update(func(s *State) error {
		if len(s.Towns) > 0 {
			return nil
		}
		for _, repo := range []string{"BrokkAi/orchard", "BrokkAi/paper-trail"} {
			c := DefaultConfig(repo)
			c.Branch = "main"
			t, _ := s.Add(c)
			t.Initialized = true
			t.Head = strings.Repeat("a", 40)
			t.LastRelease = "v0.8.2"
			for _, w := range t.Workers {
				w.Enabled = true
				w.Status = "waiting"
				w.Task = "Watching the village"
			}
			t.Report("A good morning in orchard", "The town is awake. A bug investigation is underway, and a delivery of external work is expected shortly. This is simulated activity.", time.Now())
		}
		return nil
	}); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	step := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if err := store.Update(func(s *State) error {
				t := s.Towns["brokkai/orchard"]
				if t == nil || t.Deleted {
					return nil
				}
				role := []Role{Bug, Bug, Issue, Review, Issue, Review, Review, Release, Repo}[step%9]
				if !t.Workers[role].Enabled {
					return nil
				}
				number := 142 + (step/9)%20
				active := false
				for _, w := range t.Workers {
					if w.Enabled {
						active = true
					}
					if w.Enabled {
						w.Status = "waiting"
						w.Phase = "idle"
						w.Task = "Watching for the next delivery"
					}
				}
				if !active {
					return nil
				}
				issueID := fmt.Sprintf("issue:%d", number)
				prID := fmt.Sprintf("pr:%d", number+100)
				switch step % 9 {
				case 0:
					w := t.Workers[Bug]
					w.Status = "working"
					w.Task = "Investigating a dropped event during reconnect"
					w.Phase = "investigating"
					s.Event(t.ID, "activity", "bug", "bug", "", "Bug-bot is checking a reconnect race", now)
				case 1:
					task := &Task{ID: issueID, Kind: "issue", Number: number, Title: "Recover events after a dropped connection", Stage: "queued", House: Issue, Updated: now, Detail: "The reproducer drops the connection between receiving an event and saving its cursor."}
					t.Tasks[issueID] = task
					s.Event(t.ID, "delivery", "bug", "issue", issueID, "Bug-bot filed a verified issue", now)
					t.Workers[Issue].Status = "working"
					t.Workers[Issue].Task = "Building a regression test and a focused fix"
				case 2:
					t.Tasks[issueID].Stage = "implemented"
					t.Tasks[prID] = &Task{ID: prID, Kind: "pr", Number: number + 100, Title: "Preserve the reconnect cursor", Stage: "queued", House: Review, Head: strings.Repeat("b", 40), Base: t.Head, Updated: now}
					s.Event(t.ID, "delivery", "issue", "review", prID, "A new PR arrived at the observatory", now)
					t.Workers[Review].Status = "working"
					t.Workers[Review].Task = "Independently checking the change"
				case 3:
					task := t.Tasks[prID]
					task.Detail = "The reconnect path still skips one event when the queue is full."
					s.Move(t, task, "fixes", Issue, "Review-bot returned one finding", now)
					t.Workers[Issue].Status = "working"
					t.Workers[Issue].Task = "Repairing the remaining queue-full case"
				case 4:
					task := t.Tasks[prID]
					task.Head = strings.Repeat("c", 40)
					task.Cycles++
					s.Move(t, task, "queued", Review, "The revised PR is back for review", now)
					t.Workers[Review].Status = "working"
					t.Workers[Review].Task = "Verifying the fix and all previous findings"
				case 5:
					task := t.Tasks[prID]
					task.Audit = &Audit{Base: t.Head, Head: task.Head, Verdict: "clean", Complete: true, Summary: "The lost-event regression is fixed. Both reconnect paths and the queue-full case passed.", Checks: []string{"Reconnect regression tests passed"}, Findings: []Finding{}, At: now}
					s.Move(t, task, "ready", Review, "Review complete; waiting for required checks", now)
				case 6:
					task := t.Tasks[prID]
					s.Move(t, task, "merged", Release, "The merged change reached the release depot", now)
					t.Tasks["commit:"+task.Head] = &Task{ID: "commit:" + task.Head, Kind: "commit", Title: task.Title, Head: task.Head, House: Release, Stage: "unreleased", Updated: now}
					t.Workers[Release].Status = "working"
					t.Workers[Release].Task = "Preparing the next shipment"
				case 7:
					t.LastRelease = fmt.Sprintf("v0.8.%d", 3+(number-142))
					for _, task := range t.Tasks {
						if task.Kind == "commit" {
							task.Stage = "shipped"
						}
					}
					s.Event(t.ID, "delivery", "release", "outside", "release:"+t.LastRelease, "Shipment "+t.LastRelease+" left town", now)
					t.Report("A shipment is on its way", "The reconnect fix passed its review loop and shipped. One issue moved from discovery through implementation, verification, merge, and release.", now)
					s.Event(t.ID, "report", "repo", "hall", "", "Repo-bot brought the latest town report", now)
				case 8:
					id := fmt.Sprintf("issue:%d", number+500)
					t.Tasks[id] = &Task{ID: id, Kind: "issue", Number: number + 500, Title: "Support a custom report schedule", House: Issue, Stage: "queued", External: true, Updated: now}
					s.Event(t.ID, "delivery", "outside", "issue", id, "An external issue arrived by truck", now)
					external := fmt.Sprintf("pr:%d", number+700)
					t.Tasks[external] = &Task{ID: external, Kind: "pr", Number: number + 700, Title: "Clarify setup instructions", House: Review, Stage: "awaiting_author", External: true, Updated: now}
					s.Event(t.ID, "delivery", "outside", "review", external, "A contributor PR arrived at review-bot", now)
				}
				for _, w := range t.Workers {
					w.Updated = now
					if !w.Enabled {
						w.Status = "paused"
					}
					if w.Status == "working" {
						w.Logs = append(w.Logs, Log{now, "INFO", w.Task})
						if len(w.Logs) > 30 {
							w.Logs = w.Logs[len(w.Logs)-30:]
						}
					}
				}
				t.LastSync = now
				step++
				return nil
			}); err != nil {
				return err
			}
		}
	}
}

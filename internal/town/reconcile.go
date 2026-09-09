package town

import (
	"fmt"
	"strings"
	"time"
)

// Reconcile consumes a complete remote inventory. First inventory is a baseline,
// subsequent transitions generate deliveries exactly once, irrespective of polls.
func Reconcile(s *State, t *Town, remote RepoSnapshot, now time.Time) {
	initial := !t.Initialized
	changes := []string{}
	t.Config.Branch = remote.Branch
	for _, i := range remote.Issues {
		if len(i.Pull) > 0 {
			continue
		}
		id := fmt.Sprintf("issue:%d", i.Number)
		task := t.Tasks[id]
		if task == nil {
			task = &Task{ID: id, Kind: "issue", Number: i.Number, Title: i.Title, URL: i.URL, House: Issue, External: !strings.Contains(i.Body, "<!-- bug-bot:"), Stage: "queued", Updated: now}
			t.Tasks[id] = task
			if !initial && i.State == "open" {
				from := "bug"
				if task.External {
					from = "outside"
				}
				s.Event(t.ID, "delivery", from, "issue", id, "Issue arrived: "+i.Title, now)
				changes = append(changes, fmt.Sprintf("New issue #%d: %s", i.Number, i.Title))
			}
		}
		task.Title = i.Title
		task.URL = i.URL
		if i.State == "closed" {
			task.Stage = "closed"
		} else if i.Locked {
			task.Stage = "locked"
		} else if task.Stage == "closed" || task.Stage == "locked" {
			task.Stage = "queued"
		}
	}
	for _, p := range remote.Pulls {
		if p.Base.Ref != remote.Branch {
			continue
		}
		id := fmt.Sprintf("pr:%d", p.Number)
		task := t.Tasks[id]
		// A repair may finish while the remote inventory is in flight. Do not
		// overwrite its confirmed head with the older observation; poll again.
		if remote.ObservedHeads != nil && task != nil && task.Head != remote.ObservedHeads[p.Number] {
			continue
		}
		owned := t.Owned[p.Number]
		isOwned := owned.Branch != "" && owned.Branch == p.Head.Ref && strings.EqualFold(p.Head.Repo.FullName, t.Config.Repo)
		if task == nil {
			task = &Task{ID: id, Kind: "pr", Number: p.Number, Title: p.Title, URL: p.URL, House: Review, Stage: "queued", External: !isOwned, Updated: now}
			t.Tasks[id] = task
			if !initial && p.State == "open" {
				from := "issue"
				if !isOwned {
					from = "outside"
				}
				s.Event(t.ID, "delivery", from, "review", id, "PR arrived: "+p.Title, now)
				changes = append(changes, fmt.Sprintf("New PR #%d: %s", p.Number, p.Title))
			}
		}
		task.External = !isOwned
		task.Title = p.Title
		task.URL = p.URL
		task.Branch = p.Head.Ref
		if (task.Head != "" && task.Head != p.Head.SHA) || (task.Base != "" && task.Base != p.Base.SHA) || (task.Description != "" && task.Description != description(p)) {
			task.Audit = nil
			task.Blocked = false
			task.Attempts = 0
			task.RetryAt = time.Time{}
			if task.Stage != "merged" && task.Stage != "closed" {
				s.Move(t, task, "queued", Review, "New revision ready for review", now)
			}
		}
		task.Description = description(p)
		task.Head = p.Head.SHA
		task.Base = p.Base.SHA
		if intent := t.Intents[p.Number]; intent != nil && intent.Kind == "repair" && p.Head.SHA == intent.NewHead {
			if intent.Status != "confirmed" {
				intent.Status = "confirmed"
				task.Blocked = false
				task.Attempts = 0
				task.Cycles++
				task.Audit = nil
				s.Move(t, task, "queued", Review, "Fixes delivered for another review", now)
			}
		}
		if p.MergedAt != nil {
			if intent := t.Intents[p.Number]; intent != nil && intent.Kind == "merge" {
				intent.Status = "confirmed"
				task.Blocked = false
			}
			if task.Stage != "merged" {
				if !initial {
					s.Move(t, task, "merged", Release, "Merged: "+p.Title, now)
					changes = append(changes, fmt.Sprintf("Merged PR #%d", p.Number))
				} else {
					task.Stage = "merged"
					task.House = Release
				}
			}
			if SHA(p.MergeCommit) {
				cid := "commit:" + p.MergeCommit
				if t.Tasks[cid] == nil {
					t.Tasks[cid] = &Task{ID: cid, Kind: "commit", Title: p.Title, URL: p.URL, Stage: "unreleased", House: Release, Head: p.MergeCommit, Updated: *p.MergedAt}
				}
			}
		} else if p.State == "closed" {
			task.Stage = "closed"
		} else if p.Draft {
			task.Stage = "draft"
		} else if p.Locked {
			task.Stage = "locked"
		} else if task.Stage == "draft" || task.Stage == "locked" || task.Stage == "closed" {
			task.Stage = "queued"
			task.House = Review
		}
		if isOwned {
			if it := t.Tasks[fmt.Sprintf("issue:%d", owned.Issue)]; it != nil && it.Stage != "closed" {
				it.Stage = "implemented"
			}
		}
	}
	for _, c := range remote.Commits {
		if !SHA(c.SHA) {
			continue
		}
		id := "commit:" + c.SHA
		title := strings.SplitN(c.Commit.Message, "\n", 2)[0]
		if t.Tasks[id] == nil {
			t.Tasks[id] = &Task{ID: id, Kind: "commit", Title: title, URL: c.URL, Stage: "unreleased", House: Release, Head: c.SHA, External: true, Updated: now}
			if !initial {
				s.Event(t.ID, "delivery", "outside", "release", id, "Change arrived: "+title, now)
			}
		}
		changes = append(changes, c.SHA[:8]+": "+title)
	}
	if t.Head != "" && t.Head != remote.Head && !initial {
		s.Event(t.ID, "change", "outside", "release", "commit:"+remote.Head, "Release branch advanced", now)
		changes = append(changes, "New changes reached "+remote.Branch)
	}
	t.Head = remote.Head
	latest := latestRelease(remote.Releases)
	if latest != nil && latest.Tag != t.LastRelease {
		if !initial {
			s.Event(t.ID, "delivery", "release", "outside", "release:"+latest.Tag, "Shipped "+latest.Tag, now)
			changes = append(changes, "Released "+latest.Tag)
		}
		t.LastRelease = latest.Tag
	}
	for _, task := range t.Tasks {
		if task.Kind == "commit" && remote.Released[task.Head] {
			task.Stage = "shipped"
		}
	}

	if initial || len(changes) > 0 || len(t.Reports) == 0 || now.Sub(t.Reports[len(t.Reports)-1].At) >= time.Duration(t.Config.ReportSeconds)*time.Second {
		openIssues, openPRs, blocked := 0, 0, 0
		for _, task := range t.Tasks {
			if task.Kind == "issue" && task.Stage == "queued" {
				openIssues++
			}
			if task.Kind == "pr" && task.Stage != "merged" && task.Stage != "closed" {
				openPRs++
			}
			if task.Blocked {
				blocked++
			}
		}
		title := "Repository check-in"
		if initial {
			title = "Town inventory"
		} else if len(changes) > 0 {
			title = fmt.Sprintf("%d changes in town", len(changes))
		}
		body := fmt.Sprintf("%d queued issues · %d open PRs · %d blocked tasks. Latest release: %s.", openIssues, openPRs, blocked, t.LastRelease)
		if len(changes) > 0 {
			body += "\n" + strings.Join(changes, "\n")
		}
		if len(changes) > 10 {
			title = "Busy arrivals: " + title
		}
		t.Report(title, body, now)
		s.Event(t.ID, "report", "repo", "hall", "", title, now)
	}
	t.Initialized = true
	t.LastSync = now
	t.Error = ""
}

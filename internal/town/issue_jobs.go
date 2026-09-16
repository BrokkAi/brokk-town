package town

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	issuebot "github.com/BrokkAi/issue-bot"
)

func (b *BotWorkers) issueConfig(t *Town) issuebot.Config {
	dir, state := Workspace(b.Root, t.ID, Issue)
	c := issuebot.DefaultConfig()
	c.Remote = b.remote(t.Config.Repo)
	c.Branch = t.Branch()
	c.Directory = dir
	c.StateDirectory = state
	c.GitHub.Repo = t.Config.Repo
	c.Draft = false
	c.Verify = t.Config.Verify
	return c
}

// SyncIssues imports only the public scheduling outcome from issue-bot's
// durable state. Worktree paths, issue bodies, claims and agent settings remain
// private to the bot.
func (b *BotWorkers) SyncIssues(t *Town) error {
	b.issueMu.Lock()
	defer b.issueMu.Unlock()
	saved, err := issuebot.ReadState(b.issueConfig(t))
	if err != nil || saved == nil {
		return err
	}
	return b.Store.Update(func(st *State) error {
		current := st.Towns[t.ID]
		if current == nil || current.Deleted {
			return nil
		}
		for number, job := range saved.Jobs {
			task := current.Tasks[fmt.Sprintf("issue:%d", number)]
			if task == nil {
				continue
			}
			public := &IssueJob{Status: job.Status, LastError: job.Failure, ClaimPending: job.ClaimPending}
			if job.Result != nil {
				public.ResultStatus = job.Result.Status
				public.ResultDetail = job.Result.Detail
			}
			task.IssueJob = public
			task.Attempts = job.Tries
			task.RetryAt = job.RetryAt

			terminalOnGitHub := task.Stage == "closed" || task.Stage == "locked"
			switch job.Status {
			case "blocked":
				current.RecordOutcome(OutcomeRecord{ID: fmt.Sprintf("blocked:issue:%d:%d", number, job.Tries), At: time.Now(), Class: "outcome", Kind: "blocked", Status: "blocked", Role: Issue, TaskID: task.ID, Detail: issueJobDetail(job)})
				if terminalOnGitHub {
					task.Blocked = false
					public.RetryDetail = "The issue is no longer open and eligible on GitHub."
					continue
				}
				task.Stage = "blocked"
				task.House = Issue
				task.Blocked = true
				task.Detail = issueJobDetail(job)
				public.RetryEligible = !job.ClaimPending
				if public.RetryEligible {
					public.RetryDetail = "Resolve the issue-bot explanation, then reconcile and retry."
				} else {
					public.RetryDetail = "Wait for issue-bot to finish updating its GitHub claim before retrying."
				}
			case "submitted", "has_pr":
				task.Blocked = false
				if !terminalOnGitHub {
					task.Stage = "implemented"
				}
				if job.Status == "submitted" {
					if pr := submittedPR(t.Config.Repo, job.URL); pr > 0 {
						current.Owned[pr] = Ownership{Branch: job.Branch, Issue: number}
						current.RecordOutcome(OutcomeRecord{ID: fmt.Sprintf("implementation-pr:pr:%d", pr), At: time.Now(), Class: "artifact", Kind: "implementation_pr", Status: "submitted", Role: Issue, TaskID: fmt.Sprintf("pr:%d", pr), RelatedTaskID: task.ID, URL: job.URL, Detail: task.Title})
					}
				}
			case "pending":
				task.Blocked = false
				if task.Stage == "blocked" {
					task.Stage = "queued"
					task.Detail = "Waiting for issue-bot."
				}
			default:
				task.Blocked = false
			}
		}
		return nil
	})
}

func issueJobDetail(job *issuebot.Job) string {
	if job.Result != nil && strings.TrimSpace(job.Result.Detail) != "" {
		return job.Result.Detail
	}
	if strings.TrimSpace(job.Failure) != "" {
		return job.Failure
	}
	return "Issue-bot exhausted its attempt budget. Inspect the saved work before retrying."
}

func submittedPR(repo, raw string) int {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" {
		return 0
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || !strings.EqualFold(strings.Join(parts[:2], "/"), repo) || parts[2] != "pull" {
		return 0
	}
	n, _ := strconv.Atoi(parts[3])
	return n
}

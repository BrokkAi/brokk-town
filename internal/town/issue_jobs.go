package town

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"context"
)

// SyncIssues imports only the public scheduling outcome from issue-bot's
// durable state. Worktree paths, issue bodies, claims and agent settings remain
// private to the bot.
func (b *BotWorkers) SyncIssues(t *Town) error {
	b.issueMu.Lock()
	defer b.issueMu.Unlock()
	jobs, err := b.queryIssueJobs(t, "jobs", 0)
	if err != nil || jobs == nil {
		return err
	}
	return b.Store.Update(func(st *State) error {
		current := st.Towns[t.ID]
		if current == nil || current.Deleted {
			return nil
		}
		for number, job := range jobs {
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
			if task.Requeue > 0 {
				// Town closed this issue's pull request and queued a fresh
				// attempt. Until issue-bot has started over, its saved job
				// still names the closed PR and must not put the issue back
				// to "implemented".
				if submittedPR(t.Config.Repo, job.URL) == task.Requeue || job.Status == "has_pr" {
					task.Blocked = false
					task.Stage = "queued"
					task.House = Issue
					task.Attempts = 0
					task.RetryAt = time.Time{}
					continue
				}
				task.Requeue = 0
			}

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
		resolveIssueRecovery(current)
		return nil
	})
}

func issueJobDetail(job *issueJobSummary) string {
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

// issueJobSummary is Issue Bot's public protocol result, not its state schema.
type issueJobSummary struct {
	Status       string          `json:"status"`
	Failure      string          `json:"failure"`
	ClaimPending bool            `json:"claim_pending"`
	Tries        int             `json:"tries"`
	RetryAt      time.Time       `json:"retry_at"`
	URL          string          `json:"url"`
	Branch       string          `json:"branch"`
	Result       *issueJobResult `json:"result,omitempty"`
}
type issueJobResult struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (b *BotWorkers) queryIssueJobs(t *Town, mode string, issue int) (map[int]*issueJobSummary, error) {
	if b.jobsQuery != nil {
		return b.jobsQuery(t, mode, issue)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	bot, err := b.externalBot(ctx, t.Config, Issue)
	if err != nil {
		return nil, err
	}
	dir, state := Workspace(b.Root, t.ID, Issue)
	result, err := b.runPersistent(ctx, t.ID, bot, workerRequest{Protocol: workerProtocolVersion, Remote: b.remote(t.Config.Repo), Branch: t.Branch(), Directory: dir, StateDirectory: state, Repo: t.Config.Repo, Host: "github.com", Mode: mode, Issue: issue}, false, time.Now().Add(15*time.Second), func(Progress) {}, nil)
	return result.Jobs, err
}
func (b *BotWorkers) CanRetryIssue(t *Town, issue int) (bool, error) {
	jobs, err := b.queryIssueJobs(t, "jobs", 0)
	if err != nil {
		return false, err
	}
	job := jobs[issue]
	return job != nil && (job.Status == "blocked" || job.Status == "pending"), nil
}
func (b *BotWorkers) RetryIssue(t *Town, issue int) error {
	_, err := b.queryIssueJobs(t, "retry-issue", issue)
	return err
}

package town

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/BrokkAi/acp-go/runner"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// botVersion is the identity Town records for a dispatch. The bot contract
// does not report a release yet, so every dispatch records this placeholder.
const botVersion = "0.0.0"

// workerRequest is the contract's request: what Town sends is exactly what a
// bot receives, wherever it runs.
type workerRequest = botapi.Request

// dispatchBot sends one operation to the role's bot through the bot contract.
// Town never calls a bot package, reads a bot's files or resolves a command
// for a bot: everything it learns comes back in the result. Tests may replace
// the bot with botRun.
func (b *BotWorkers) dispatchBot(ctx context.Context, role Role, request workerRequest, retry bool, observe func(Progress)) (workerResult, error) {
	if observe == nil {
		observe = func(Progress) {}
	}
	if b.botRun != nil {
		return b.botRun(ctx, role, request, retry, observe)
	}
	name := workerBotNames[role]
	bot := b.Bots[role]
	if bot == nil {
		return workerResult{}, fmt.Errorf("no %s is configured", name)
	}
	request.Role = string(role)
	request.Retry = retry
	wire, err := bot.Run(ctx, request, func(e botapi.Event) {
		if p := e.Progress; p != nil {
			phase, task := strings.TrimSpace(p.Phase), strings.TrimSpace(p.Task)
			if phase == "" {
				phase = "running"
			}
			observe(Progress{Phase: botapi.Truncate(phase, 128), Task: botapi.Truncate(task, 4096)})
		}
		if l := e.Log; l != nil {
			botLog(ctx, name, l)
		}
	})
	return fromWire(wire), err
}

// botLog writes a bot's diagnostic to the service log, bounded so a bot cannot
// flood it.
func botLog(ctx context.Context, bot string, l *botapi.Log) {
	var level slog.Level
	if level.UnmarshalText([]byte(l.Level)) != nil {
		level = slog.LevelInfo
	}
	args := []any{"bot", bot}
	count := 0
	for key, value := range l.Attrs {
		if count == 32 {
			args = append(args, "truncated_attrs", len(l.Attrs)-count)
			break
		}
		if key == "bot" {
			continue
		}
		args = append(args, botapi.Truncate(key, 128), botapi.Truncate(value, 4096))
		count++
	}
	slog.Default().Log(ctx, level, botapi.Truncate(l.Message, 4096), args...)
}

// agentSelection carries an explicitly configured agent to a bot unchanged.
func agentSelection(a runner.AgentConfig) botapi.Agent {
	return botapi.Agent{Command: a.Command, Environment: a.Environment, AuthMethod: a.AuthMethod, Mode: a.Mode, Model: a.Model, Effort: a.Effort}
}

// wirePolicy copies the operator's policy onto the contract.
func wirePolicy(p BotPolicy) *botapi.Policy {
	policy := &botapi.Policy{Labels: p.Labels, ExcludeLabels: p.ExcludeLabels, Only: p.Only, Focus: p.Focus, Limit: p.Limit, Attempts: p.Attempts, Verify: p.Verify}
	if r := p.Release; r != nil {
		policy.Release = &botapi.ReleasePolicy{
			DailySeconds: r.DailySeconds, MinimumGapSeconds: r.MinimumGapSeconds, QuietSeconds: r.QuietSeconds,
			Burst: r.Burst, BurstWindowSeconds: r.BurstWindowSeconds, Triage: r.Triage, Preflight: r.Preflight,
			VerificationTimeoutSeconds: r.VerificationTimeoutSeconds, Workflows: r.Workflows, Assets: r.Assets,
		}
	}
	return policy
}

// Bounds on text a bot reports. A bot may run in another process, so its
// result is input: these keep one bad answer from bloating saved state.
const (
	wireStatusBytes = 64
	wireTextBytes   = 16 << 10
	wireURLBytes    = 2048
	maxWireJobs     = 10000
)

// fromWire copies a bot's result into Town's own types, bounding the text it
// carries. Town validates the copy before anything reaches persisted state.
func fromWire(r botapi.Result) workerResult {
	result := workerResult{CostUSD: r.CostUSD, transcripts: r.Transcripts}
	if r.Usage != nil {
		result.Usage = &OutcomeUsage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens}
	}
	if r.Jobs != nil {
		result.Jobs = make(map[int]*issueJobSummary, len(r.Jobs))
		for number, j := range r.Jobs {
			if j == nil || number < 1 || len(result.Jobs) >= maxWireJobs {
				continue
			}
			v := &issueJobSummary{
				Status: botapi.Truncate(j.Status, wireStatusBytes), Failure: botapi.Truncate(j.Failure, wireTextBytes), ClaimPending: j.ClaimPending,
				Tries: max(j.Tries, 0), RetryAt: j.RetryAt,
			}
			if len(j.URL) <= wireURLBytes {
				v.URL = j.URL
			}
			if ValidBranch(j.Branch) {
				v.Branch = j.Branch
			}
			if j.Result != nil {
				v.Result = &issueJobResult{Status: botapi.Truncate(j.Result.Status, wireStatusBytes), Detail: botapi.Truncate(j.Result.Detail, wireTextBytes)}
			}
			result.Jobs[number] = v
		}
	}
	if r.Issue != nil {
		result.Issue = &workerIssueResult{Owned: []workerIssueOwnership{}}
		for _, o := range r.Issue.Owned {
			// Ownership names the branch Town may later push to or delete; an
			// unnamed one is kept as it always was, a malformed one is not.
			if o.Branch == "" || ValidBranch(o.Branch) {
				result.Issue.Owned = append(result.Issue.Owned, workerIssueOwnership{PR: o.PR, Branch: o.Branch, Issue: o.Issue})
			}
		}
	}
	if v := r.Review; v != nil {
		result.Review = &workerReviewResult{Status: botapi.Truncate(v.Status, wireStatusBytes), Detail: botapi.Truncate(v.Detail, wireTextBytes), Complete: v.Complete, Findings: v.Findings, Severities: v.Severities, ExactBase: v.ExactBase, ExactHead: v.ExactHead}
	}
	if s := r.Simplification; s != nil {
		result.Simplification = &workerSimplification{Mode: s.Mode, Decision: s.Decision, Summary: s.Summary, Detail: s.Detail}
	}
	if j := r.Judgment; j != nil {
		result.Judgment = &Judgment{Decision: j.Decision, Reason: j.Reason}
	}
	if b := r.Bulletin; b != nil {
		items := make([]BulletinItem, 0, len(b.Items))
		for _, item := range b.Items {
			items = append(items, BulletinItem{Kind: item.Kind, Title: item.Title, Detail: item.Detail, Pulls: item.Pulls, Issues: item.Issues})
		}
		result.Bulletin = &Bulletin{Since: b.Since, Until: b.Until, Title: b.Title, Summary: b.Summary, Items: items, Pulls: b.Pulls}
	}
	if h := r.Health; h != nil {
		result.Health = &BranchHealth{State: h.State, Head: h.Head, Failing: h.Failing, Pushed: h.Pushed, Attempts: h.Attempts, Detail: h.Detail}
	}
	if inventory := r.Inventory; inventory != nil {
		result.Inventory = inventoryFromWire(inventory)
	}
	return result
}

func inventoryFromWire(inventory *botapi.Inventory) *workerInventory {
	converted := &workerInventory{
		Incremental: inventory.Incremental, StartedAt: inventory.StartedAt, Branch: inventory.Branch,
		DefaultBranch: inventory.DefaultBranch, Head: inventory.Head, Released: inventory.Released,
	}
	labels := func(in []botapi.Label) []RemoteLabel {
		var out []RemoteLabel
		for _, label := range in {
			out = append(out, RemoteLabel{Name: label.Name})
		}
		return out
	}
	ref := func(in botapi.Ref) Ref {
		out := Ref{Ref: in.Ref, SHA: in.SHA}
		out.Repo.FullName = in.Repo.FullName
		return out
	}
	for _, issue := range inventory.Issues {
		converted.Issues = append(converted.Issues, RemoteIssue{Number: issue.Number, Title: issue.Title, Body: issue.Body, URL: issue.URL, State: issue.State, Locked: issue.Locked, Labels: labels(issue.Labels), Pull: issue.Pull, Updated: issue.Updated})
	}
	for _, pull := range inventory.Pulls {
		converted.Pulls = append(converted.Pulls, Pull{
			Number: pull.Number, Title: pull.Title, Body: pull.Body, URL: pull.URL, State: pull.State, Draft: pull.Draft, Locked: pull.Locked,
			Labels: labels(pull.Labels), Comments: pull.Comments, ReviewComments: pull.ReviewComments, Head: ref(pull.Head), Base: ref(pull.Base),
			MergedAt: pull.MergedAt, MergeCommit: pull.MergeCommit, Updated: pull.Updated,
		})
	}
	for _, release := range inventory.Releases {
		converted.Releases = append(converted.Releases, RemoteRelease{Tag: release.Tag, Name: release.Name, URL: release.URL, Draft: release.Draft, Prerelease: release.Prerelease, At: release.At})
	}
	for _, commit := range inventory.Commits {
		remote := RemoteCommit{SHA: commit.SHA, URL: commit.URL}
		remote.Commit.Message = commit.Commit.Message
		converted.Commits = append(converted.Commits, remote)
	}
	return converted
}

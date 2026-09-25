package town

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Diagnostic contains only public, deliberately worded facts. Never put raw
// command output, private commands, environment or authentication values here.
type Diagnostic struct {
	Code   string `json:"code"`
	Role   Role   `json:"role,omitempty"`
	Status string `json:"status"` // passed, blocked, unknown
	Detail string `json:"detail"`
	Action string `json:"action,omitempty"`
	URL    string `json:"url,omitempty"`
}

type DiagnosticReport struct {
	At     time.Time    `json:"at"`
	Head   string       `json:"head,omitempty"`
	Base   string       `json:"base,omitempty"`
	Checks []Diagnostic `json:"checks"`
}

// Summary is the plain-text counterpart of a report's structured checks.
func (r *DiagnosticReport) Summary() string {
	parts := make([]string, 0, len(r.Checks))
	for _, check := range r.Checks {
		parts = append(parts, strings.TrimSpace(check.Detail+" "+check.Action))
	}
	return strings.Join(parts, " ")
}

func (t *Task) clearMergeWait() {
	if t.MergeWait != nil && strings.TrimSpace(t.Detail) == t.MergeWait.Summary() {
		t.Detail = ""
	}
	t.MergeWait = nil
}

func (g MergeGate) blockers(p Pull, a *Audit, branch, repo string) []Diagnostic {
	var out []Diagnostic
	link := fmt.Sprintf("https://github.com/%s/pull/%d", repo, p.Number)
	add := func(code, status, detail, action, suffix string) {
		out = append(out, Diagnostic{Code: code, Status: status, Detail: detail, Action: action, URL: link + suffix})
	}
	if branch == "" || p.Base.Ref != branch || g.BaseRef != branch {
		add("target_branch", "blocked", fmt.Sprintf("Targets %s (gate: %s), but this town covers %s.", p.Base.Ref, g.BaseRef, branch), "Check the PR target and town branch; Town only merges into its configured branch.", "")
	}
	if !a.Clean(g.Base, g.Head) || p.Head.SHA != g.Head || p.Base.SHA != g.Base {
		add("town_review", "blocked", "Town review is missing, incomplete, or stale for the observed base and head.", "Let Repo Bot refresh the revision and Review Bot certify it before merging.", "/files")
	}
	if p.State != "open" || g.State != "OPEN" {
		add("pr_state", "blocked", "The pull request is no longer open, or its state is unavailable.", "Check its state on GitHub and let Repo Bot reconcile it.", "")
	}
	if p.Draft || g.Draft {
		add("draft", "blocked", "The pull request is a draft.", "Mark it ready for review on GitHub when it is ready.", "")
	}
	if p.Locked {
		add("locked", "blocked", "The pull request is locked.", "Ask a repository maintainer to resolve the lock.", "")
	}
	if !g.PolicyKnown {
		add("merge_policy", "unknown", "Repository merge policy is unavailable.", "Check GitHub access with bt doctor and retry the read.", "")
	} else if !g.SquashAllowed {
		add("merge_strategy", "blocked", "Squash merging is disabled; Town's merge strategy is unsupported here.", "Merge manually using an allowed strategy, or ask a maintainer to enable squash merging.", "")
	}
	if g.MergeQueue != nil {
		add("merge_queue", "blocked", "The target branch uses a merge queue, which Town cannot drive.", "Use GitHub's merge queue; Repo Bot will observe the confirmed merge.", "")
	}
	switch g.Review {
	case "REVIEW_REQUIRED":
		add("approval", "blocked", "A required GitHub approval is missing.", "Request approval from an eligible reviewer.", "")
	case "CHANGES_REQUESTED":
		add("approval", "blocked", "A GitHub review requests changes.", "Address the review and request approval again.", "")
	case "", "APPROVED":
	default:
		add("approval", "unknown", "GitHub's review decision is unavailable or unrecognized.", "Inspect the PR reviews on GitHub and retry the read.", "")
	}
	if g.Mergeable == "CONFLICTING" || g.MergeState == "DIRTY" {
		add("conflicts", "blocked", "The pull request has merge conflicts.", "Have the branch owner resolve conflicts, then obtain a new Town review.", "")
	} else if g.Mergeable != "MERGEABLE" {
		add("mergeability", "unknown", "GitHub has not determined mergeability.", "Wait for GitHub to calculate it; Town will check again.", "")
	}
	if g.MergeState != "CLEAN" || (g.Checks != nil && g.Checks.State != "SUCCESS") {
		if g.Checks == nil {
			add("checks", "unknown", "GitHub check results are unavailable; they have not been treated as passed.", "Inspect required checks and workflow permissions on GitHub.", "/checks")
		} else {
			switch g.Checks.State {
			case "FAILURE", "ERROR":
				add("checks", "blocked", "GitHub reports failing checks.", "Open the check logs, fix the failure, and wait for checks on the current revision.", "/checks")
			case "PENDING", "EXPECTED":
				add("checks", "blocked", "GitHub checks are pending or have not reported yet.", "Wait for checks; if none start, inspect the required-check and workflow configuration.", "/checks")
			case "SUCCESS":
			default:
				add("checks", "unknown", "GitHub check status is unrecognized.", "Inspect the checks on GitHub and retry the read.", "/checks")
			}
		}
		switch g.MergeState {
		case "BEHIND":
			add("behind", "blocked", "The PR branch is behind its target branch.", "Have the branch owner update it, then wait for fresh checks and review.", "")
		case "BLOCKED":
			if len(out) == 0 {
				add("branch_rules", "unknown", "GitHub reports a branch-rule blocker without identifying it in the merge gate.", "Inspect the PR merge panel for unresolved threads, deployments, or other branch requirements.", "")
			}
		case "CLEAN", "DIRTY", "DRAFT", "UNSTABLE":
			if len(out) == 0 {
				add("merge_state", "unknown", "GitHub reports a non-clean merge state without its cause.", "Inspect the PR merge panel and retry after the requirement is resolved.", "")
			}
		default:
			add("merge_state", "unknown", "GitHub's merge state is unavailable or unsupported.", "Inspect the PR merge panel; merge manually if repository hooks require it.", "")
		}
	}
	return out
}

func (s *Supervisor) saveMergeWait(t *Town, task *Task, p Pull, checks []Diagnostic) error {
	return s.Store.Update(func(st *State) error {
		town := st.Towns[t.ID]
		if town == nil || town.Deleted || town.Branch() != t.Branch() {
			return nil
		}
		current := town.Tasks[task.ID]
		// Inventory may have moved the task while the gate read was in flight.
		// A result for that earlier state cannot replace its new explanation.
		if current == nil || current.Head != task.Head || current.Base != task.Base || current.Stage != task.Stage || current.House != task.House || current.Description != task.Description || current.Blocked != task.Blocked || current.Offbranch != task.Offbranch || !reflect.DeepEqual(current.Audit, task.Audit) {
			return nil
		}
		if len(checks) == 0 {
			current.clearMergeWait()
			return nil
		}
		current.MergeWait = &DiagnosticReport{At: s.now(), Head: p.Head.SHA, Base: p.Base.SHA, Checks: checks}
		current.Detail = current.MergeWait.Summary()
		return nil
	})
}

func (s *Supervisor) unavailableMergeWait(t *Town, task *Task, p Pull, err error) error {
	check := Diagnostic{Code: "github_unavailable", Status: "unknown", Detail: "GitHub merge information is unavailable; no merge was attempted.", Action: "Run bt doctor --repo " + t.Config.Repo + " to check access, then let Town retry the read.", URL: fmt.Sprintf("https://github.com/%s/pull/%d", t.Config.Repo, task.Number)}
	return errors.Join(err, s.saveMergeWait(t, task, p, []Diagnostic{check}))
}

// SetupGitHub is a read-only diagnostic capability, separate from write APIs.
type SetupGitHub interface {
	SetupAccess(context.Context, string) error
}

func (g GitHubClient) SetupAccess(ctx context.Context, repo string) error {
	var result struct {
		FullName string `json:"full_name"`
	}
	if err := g.api(ctx, "GET", "repos/"+repo, nil, &result); err != nil {
		return err
	}
	if !strings.EqualFold(result.FullName, repo) {
		return errors.New("repository identity not confirmed")
	}
	return nil
}

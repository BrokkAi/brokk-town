package issuebot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

func TestIssueJobSummaryAndTargetedRetryPreserveSavedEvidence(t *testing.T) {
	root := t.TempDir()
	checkout, state, err := botapi.Dirs(root, "repo", "issue")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(checkout, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	saved := State{Format: 1, Remote: "https://github.com/acme/fixture.git", Branch: "main", Directory: checkout, Repo: "acme/fixture", Host: "github.com", Jobs: map[int]*Job{
		7: {Issue: Issue{Number: 7}, Status: "blocked", Tries: 3, RetryAt: time.Now().Add(time.Hour), ClaimPending: true, Failure: "uncertain publication", Result: &Result{Status: "blocked", Detail: "saved evidence"}},
		8: {Issue: Issue{Number: 8}, Status: "blocked", Tries: 2},
	}}
	data, _ := json.Marshal(saved)
	if err := os.WriteFile(filepath.Join(state, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// The summary and the retry go through the bot contract: the request names
	// the workspace, and the bot finds its own saved state under its root.
	request := botapi.Request{Town: "repo", Role: "issue", Remote: saved.Remote, Branch: saved.Branch, Repo: saved.Repo, Host: saved.Host}
	query := func(mode string, issue int) map[int]*botapi.IssueJob {
		t.Helper()
		r := request
		r.Mode, r.Issue = mode, issue
		result, err := Worker(root).Run(context.Background(), r, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result.Jobs
	}
	jobs := query("jobs", 0)
	if jobs[7].Status != "blocked" || !jobs[7].ClaimPending || jobs[7].Result.Detail != "saved evidence" {
		t.Fatalf("summary lost evidence: %+v", jobs[7])
	}
	jobs = query("retry-issue", 7)
	if jobs[7].Status != "pending" || jobs[7].Tries != 0 || !jobs[7].RetryAt.IsZero() || jobs[8].Tries != 2 {
		t.Fatalf("incorrect retry: %+v", jobs)
	}
	after, err := ReadState(Config{StateDirectory: state, Remote: saved.Remote, Branch: saved.Branch, Directory: saved.Directory, GitHub: GitHubConfig{Repo: saved.Repo, Host: saved.Host}})
	if err != nil {
		t.Fatal(err)
	}
	if !after.Jobs[7].ClaimPending || after.Jobs[7].Result.Detail != "saved evidence" {
		t.Fatal("retry lost durable evidence")
	}
}

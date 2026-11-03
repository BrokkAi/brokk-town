package botserver

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// demoBot answers every run with a canned result its role's service accepts,
// after reporting progress and a log record the way a real run does. It never
// runs git, gh or an agent, so a demo deployment exercises the whole network
// path without credentials. Storage is the real bot's, over its own root.
type demoBot struct {
	worker botapi.Worker
}

func (d demoBot) Storage(ctx context.Context, request botapi.StorageRequest) (botapi.StorageResult, error) {
	return d.worker.Storage(ctx, request)
}

func (d demoBot) Run(ctx context.Context, request botapi.Request, emit func(botapi.Event)) (botapi.Result, error) {
	if request.Role != d.worker.Role {
		return botapi.Result{}, fmt.Errorf("%s cannot run the %q role", d.worker.Name, request.Role)
	}
	emit(botapi.Event{Progress: &botapi.Progress{Phase: "demo", Task: "Answering " + request.Repo + " with a canned result"}})
	emit(botapi.Event{Log: &botapi.Log{Time: time.Now().UTC(), Level: "INFO", Message: "demo run", Attrs: map[string]string{"bot": d.worker.Name, "mode": request.Mode}}})
	select {
	case <-ctx.Done():
		return botapi.Result{}, context.Cause(ctx)
	case <-time.After(200 * time.Millisecond):
	}
	return demoResult(request), nil
}

func demoResult(request botapi.Request) botapi.Result {
	zero := 0.0
	result := botapi.Result{Usage: &botapi.Usage{}, CostUSD: &zero}
	switch request.Role {
	case "issue":
		result.Issue = &botapi.IssueResult{Owned: []botapi.IssueOwnership{}}
	case "review":
		result.Review = &botapi.ReviewResult{Status: "submitted", Detail: "Demo review: no findings.", Complete: true, Findings: map[string]string{}, Severities: map[string]string{}, ExactBase: request.BaseSHA, ExactHead: request.HeadSHA}
	case "simplifier":
		if request.Issue > 0 || request.PR > 0 {
			mode := request.Mode
			if mode == "" {
				mode = "suggest"
			}
			result.Simplification = &botapi.Simplification{Mode: mode, Decision: "decline", Summary: "Demo assessment", Detail: "Demo assessment: nothing to simplify."}
		}
	case "hall":
		if request.Mode == "bulletin" && request.Since != nil && request.Until != nil {
			result.Bulletin = &botapi.Bulletin{Since: *request.Since, Until: *request.Until, Title: "Demo status report", Summary: "Nothing happened in this demo window.", Items: []botapi.BulletinItem{}, Pulls: []int{}}
		} else {
			result.Judgment = &botapi.Judgment{Decision: "admit", Reason: "Demo decision: admitted."}
		}
	case "repo":
		branch := request.Branch
		if branch == "" {
			branch = "main"
		}
		sum := sha1.Sum([]byte(request.Repo + "\x00" + branch))
		result.Inventory = &botapi.Inventory{Branch: branch, DefaultBranch: branch, Head: hex.EncodeToString(sum[:]), Issues: []botapi.Issue{}, Pulls: []botapi.Pull{}, Releases: []botapi.Release{}}
		result.Health = &botapi.BranchHealth{State: "green", Head: hex.EncodeToString(sum[:])}
	}
	return result
}

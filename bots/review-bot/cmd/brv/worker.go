package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"

	bot "github.com/BrokkAi/review-bot"
	"github.com/BrokkAi/review-bot/internal/worker"
)

func workerCommand(ctx context.Context, args []string, version string) error {
	fs := flag.NewFlagSet("brv worker", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: brv worker --socket PATH\n\nServe versioned one-shot PR reviews to Brokk Town over a private Unix socket.")
		fs.PrintDefaults()
	}
	socket := fs.String("socket", "", "private Unix-domain socket path (required)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *socket == "" {
		return fmt.Errorf("worker requires exactly one --socket PATH")
	}
	return worker.Serve(ctx, *socket, worker.Initialize{
		Protocol: worker.ProtocolVersion, MinimumProtocol: worker.MinimumProtocol,
		Bot: "review-bot", Version: version, Capabilities: []string{"policy", "run", "progress", "exact-revision-review", "finding-severity"},
	}, func(ctx context.Context, request worker.Request, progress func(worker.Progress)) (worker.Result, error) {
		if request.PR < 1 {
			return worker.Result{}, fmt.Errorf("review worker requires a positive PR number")
		}
		cfg := bot.DefaultConfig()
		cfg.Remote = request.Remote
		cfg.Branch = request.Branch
		cfg.Directory = request.Directory
		cfg.StateDirectory = request.StateDirectory
		cfg.Agent = request.Agent
		cfg.GitHub.Repo = request.Repo
		cfg.GitHub.Host = request.Host
		cfg.PR = request.PR
		cfg.Verify = request.Verify
		if policy := request.Policy; policy != nil {
			cfg.Labels = appendLabels(cfg.Labels, policy.Labels)
			cfg.ExcludeLabels = appendLabels(cfg.ExcludeLabels, policy.ExcludeLabels)
			if policy.Only > 0 && cfg.PR == 0 {
				cfg.PR = policy.Only
			}
			if policy.Focus != "" {
				cfg.Focus = policy.Focus
			}
			if policy.Limit > 0 {
				cfg.MaxFindings = policy.Limit
			}
			if policy.Attempts > 0 {
				cfg.Attempts = policy.Attempts
			}
			if len(policy.Verify) > 0 {
				cfg.Verify = policy.Verify
			}
		}
		ctx = bot.WithProgress(ctx, func(p bot.Progress) {
			progress(worker.Progress{Phase: p.Phase, Task: p.Task})
		})
		if err := bot.Run(ctx, cfg, slog.Default(), true); err != nil {
			return worker.Result{}, err
		}
		saved, err := bot.ReadState(cfg)
		if err != nil {
			return worker.Result{}, err
		}
		if saved == nil {
			return worker.Result{}, fmt.Errorf("review did not produce a durable result")
		}
		result := reviewResult(saved, request)
		return worker.Result{Review: &result}, nil
	}, slog.Default())
}

func findingID(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:24]
}

// reviewResult only certifies a submitted review of the requested revision.
// Other revisions' findings must never be attributed to this dispatch.
func reviewResult(saved *bot.State, request worker.Request) worker.ReviewResult {
	result := worker.ReviewResult{Status: "stale", Detail: "No submitted review matches the requested revision; refresh PR eligibility and revisions", Findings: map[string]string{}, Severities: map[string]string{}, ExactBase: request.BaseSHA, ExactHead: request.HeadSHA}
	for _, job := range saved.Jobs {
		if job.PR.Number != request.PR || job.DryRun || job.PR.Head.SHA != request.HeadSHA || job.PR.Base.SHA != request.BaseSHA {
			continue
		}
		result.Status, result.Detail = job.Status, job.Failure
		if job.Status != "submitted" {
			continue
		}
		result.Complete = true
		for _, candidate := range job.Candidates {
			if candidate.Verdict == "invalid" {
				continue
			}
			id := findingID(candidate.Finding.Path + candidate.Finding.Title + candidate.Finding.Trigger)
			result.Findings[id] = fmt.Sprintf("[%s] %s: %s\n%s\nTrigger: %s\nEvidence: %s\nVerifier: %s", candidate.Finding.Severity, candidate.Finding.Path, candidate.Finding.Title, candidate.Finding.Explanation, candidate.Finding.Trigger, strings.Join(candidate.Finding.Evidence, "; "), candidate.Reason)
			result.Severities[id] = candidate.Finding.Severity
		}
	}
	return result
}

// appendLabels adds Town's filter to the bot's own default without duplicating
// an entry the configuration already carries.
func appendLabels(existing, extra []string) []string {
	if len(extra) == 0 {
		return existing
	}
	seen := map[string]bool{}
	for _, l := range existing {
		seen[strings.ToLower(strings.TrimSpace(l))] = true
	}
	for _, l := range extra {
		key := strings.ToLower(strings.TrimSpace(l))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		existing = append(existing, strings.TrimSpace(l))
	}
	return existing
}

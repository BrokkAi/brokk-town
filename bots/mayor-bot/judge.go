package mayorbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// JudgeRequest names one Town Hall arrival. Arrival is the town's own
// description of it: kind, title, any Simplifier advice or town review, or the
// arrival. The Mayor treats it as data and adds the GitHub source.
type JudgeRequest struct {
	Issue   int
	PR      int
	HeadSHA string
	BaseSHA string
	Arrival json.RawMessage
}

const maxArrival = 256 << 10

// arrivalKind reads the one field of the arrival the Mayor needs itself.
func arrivalKind(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > maxArrival || !json.Valid(raw) {
		return "", errors.New("arrival must be a bounded JSON object")
	}
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", errors.New("arrival must be a JSON object")
	}
	switch header.Kind {
	case "issue", "pr":
		return header.Kind, nil
	}
	return "", fmt.Errorf("unknown arrival kind %q", header.Kind)
}

// Judge decides one arrival in a detached worktree of the exact revision it
// concerns: the pull request head, or the branch head for issues and bot
// updates. It performs no GitHub writes; the town applies the decision.
func Judge(ctx context.Context, cfg Config, request JudgeRequest, log *slog.Logger) (Judgment, error) {
	if err := prepareConfig(&cfg); err != nil {
		return Judgment{}, err
	}
	if log == nil {
		log = slog.Default()
	}
	unlock, err := lockConfig(cfg)
	if err != nil {
		return Judgment{}, err
	}
	defer unlock()
	return engine{config: cfg, source: githubClient{cfg}, log: log, agent: func(c Config) Agent { return agentProcess{c, log} }, sleep: pause, observe: observe(ctx)}.judge(ctx, request)
}

func (e engine) judge(ctx context.Context, request JudgeRequest) (Judgment, error) {
	kind, err := arrivalKind(request.Arrival)
	if err != nil {
		return Judgment{}, err
	}
	if request.Issue < 0 || request.PR < 0 || (request.Issue > 0 && request.PR > 0) || (kind == "issue") != (request.Issue > 0) || (kind == "pr") != (request.PR > 0) {
		return Judgment{}, errors.New("arrival kind does not match the issue or pull request number")
	}
	e.observe(Progress{Phase: "loading", Task: "Refreshing repository and arrival"})
	base := checkout{config: e.config}
	if err = base.open(ctx); err != nil {
		return Judgment{}, err
	}
	head, err := base.head(ctx)
	if err != nil {
		return Judgment{}, err
	}
	var worktree checkout
	revision, source := head, any(nil)
	switch kind {
	case "issue":
		i, err := e.source.issue(ctx, request.Issue)
		if err != nil {
			return Judgment{}, err
		}
		i.Body = bounded(i.Body, 32<<10)
		for c := range i.Comments {
			i.Comments[c] = bounded(i.Comments[c], 16<<10)
		}
		source = i
		if worktree, err = base.itemWorktree(ctx, fmt.Sprintf("issue-%d", request.Issue), head); err != nil {
			return Judgment{}, err
		}
	case "pr":
		p, err := e.source.pull(ctx, request.PR)
		if err != nil {
			return Judgment{}, err
		}
		if request.HeadSHA != "" && p.Head.SHA != request.HeadSHA {
			return Judgment{}, fmt.Errorf("pull request head moved: town asked about %s, GitHub reports %s", request.HeadSHA, p.Head.SHA)
		}
		if revision, err = base.fetchItem(ctx, fmt.Sprintf("refs/pull/%d/head", request.PR), p.Head.SHA); err != nil {
			return Judgment{}, err
		}
		p.Body = bounded(p.Body, 32<<10)
		for c := range p.Discussion {
			p.Discussion[c] = bounded(p.Discussion[c], 16<<10)
		}
		source = p
		if worktree, err = base.itemWorktree(ctx, fmt.Sprintf("pr-%d", request.PR), revision); err != nil {
			return Judgment{}, err
		}
	default:
		if worktree, err = base.itemWorktree(ctx, "hall", head); err != nil {
			return Judgment{}, err
		}
	}
	var arrival any
	_ = json.Unmarshal(request.Arrival, &arrival)
	instructions, err := instructionFiles(worktree.config)
	if err != nil {
		return Judgment{}, err
	}
	e.observe(Progress{Phase: "judging", Task: "Weighing the arrival against the repository"})
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.config.Timeout))
	defer cancel()
	text, err := execute(ctx, e.agent(worktree.config), instructions+judgePrompt(kind, arrival, source), e.log, e.sleep)
	if err != nil {
		return Judgment{}, err
	}
	if err = worktree.verify(ctx, revision); err != nil {
		return Judgment{}, err
	}
	e.observe(Progress{Phase: "reporting", Task: "Returning the decision"})
	return parseJudgment(text, kind)
}
func prepareConfig(cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	for _, path := range []*string{&cfg.Directory, &cfg.StateDirectory} {
		absolute, err := filepathAbs(*path)
		if err != nil {
			return err
		}
		*path, err = canonical(absolute)
		if err != nil {
			return err
		}
	}
	if cfg.GitHubRepo() == "" {
		return errors.New("GitHub repository required; set github.repo for a local mirror")
	}
	return nil
}

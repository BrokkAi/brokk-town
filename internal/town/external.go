package town

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

var botCommands = map[Role]string{
	Bug:     "bbb",
	Feature: "bfb",
	Issue:   "bib",
	Review:  "brv",
	Release: "brb",
}

var botVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type externalBot struct {
	role    Role
	command string
	version string
	hash    string
}

func (b *BotWorkers) externalBot(ctx context.Context, role Role) (externalBot, error) {
	name, ok := botCommands[role]
	if !ok {
		return externalBot{}, fmt.Errorf("unsupported worker %s", role)
	}
	if b.BotCommands != nil {
		if override, ok := b.BotCommands[role]; ok && strings.TrimSpace(override) != "" {
			name = strings.TrimSpace(override)
		}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return externalBot{}, fmt.Errorf("%s bot %q is not installed on the service PATH", role, name)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return externalBot{}, fmt.Errorf("resolve %s bot: %w", role, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return externalBot{}, fmt.Errorf("%s bot is not a regular executable", role)
	}
	bot := externalBot{role: role, command: resolved}
	if bot.hash, err = fileHash(resolved); err != nil {
		return externalBot{}, err
	}
	output, err := osrun.Run(ctx, "", nil, resolved, "version")
	if err != nil {
		return externalBot{}, fmt.Errorf("read %s bot version: %w", role, err)
	}
	if !botVersionPattern.MatchString(output) {
		return externalBot{}, fmt.Errorf("%s bot returned an invalid version %q", role, truncate(output, 128))
	}
	bot.version = output
	return bot, bot.unchanged()
}

func (b externalBot) unchanged() error {
	resolved, err := filepath.EvalSymlinks(b.command)
	if err != nil {
		return fmt.Errorf("%s bot disappeared during dispatch: %w", b.role, err)
	}
	if resolved != b.command {
		return fmt.Errorf("%s bot path changed during dispatch", b.role)
	}
	hash, err := fileHash(resolved)
	if err != nil || hash != b.hash {
		return fmt.Errorf("%s bot executable changed during dispatch", b.role)
	}
	return nil
}

func (b externalBot) verify(ctx context.Context) error {
	if err := b.unchanged(); err != nil {
		return err
	}
	output, err := osrun.Run(ctx, "", nil, b.command, "version")
	if err != nil {
		return fmt.Errorf("recheck %s bot version: %w", b.role, err)
	}
	if output != b.version {
		return fmt.Errorf("%s bot version changed during dispatch: started %s, finished %s", b.role, b.version, output)
	}
	return b.unchanged()
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type externalGitHub struct {
	Repo string `json:"repo"`
	Host string `json:"host"`
}

type externalConfig struct {
	Remote           string             `json:"remote"`
	Branch           string             `json:"branch"`
	Directory        string             `json:"directory"`
	StateDirectory   string             `json:"state_directory"`
	InstructionFiles []string           `json:"instruction_files"`
	Agent            runner.AgentConfig `json:"agent"`
	GitHub           externalGitHub     `json:"github"`
	Poll             string             `json:"poll"`
	Timeout          string             `json:"timeout"`
	RetryDelay       string             `json:"retry_delay"`
	Attempts         int                `json:"attempts"`

	ClaimTimeout        string   `json:"claim_timeout,omitempty"`
	Daily               string   `json:"daily,omitempty"`
	MinimumGap          string   `json:"minimum_gap,omitempty"`
	Quiet               string   `json:"quiet,omitempty"`
	BurstWindow         string   `json:"burst_window,omitempty"`
	Burst               int      `json:"burst,omitempty"`
	VerificationTimeout string   `json:"verification_timeout,omitempty"`
	MaxIssues           int      `json:"max_issues,omitempty"`
	MaxFindings         int      `json:"max_findings,omitempty"`
	PR                  int      `json:"pr,omitempty"`
	Draft               *bool    `json:"draft,omitempty"`
	Verify              []string `json:"verify,omitempty"`
}

func writeExternalConfig(state string, agent runner.AgentConfig, bot externalBot, t *Town, dir, remote string, pr int) (string, error) {
	branch := t.Config.Branch
	if branch == "" {
		branch = "master"
	}
	c := externalConfig{
		Remote: remote, Branch: branch, Directory: dir, StateDirectory: state,
		InstructionFiles: []string{"AGENTS.md", "CONTRIBUTING.md", "README.md"},
		Agent:            agent, GitHub: externalGitHub{Repo: t.Config.Repo, Host: "github.com"},
		Poll: "5m", Timeout: "2h", RetryDelay: "15m", Attempts: 3,
		Verify: t.Config.Verify,
	}
	switch bot.role {
	case Bug, Feature:
		c.Poll, c.MaxIssues = "30m", 3
	case Issue:
		falseValue := false
		c.ClaimTimeout, c.Draft = "15m", &falseValue
	case Review:
		c.MaxFindings, c.PR = 10, pr
	case Release:
		c.InstructionFiles = []string{"AGENTS.md", "RELEASING.md", "RELEASE.md", "CONTRIBUTING.md"}
		c.Daily, c.MinimumGap, c.Quiet, c.BurstWindow, c.Burst, c.VerificationTimeout = "24h", "2h", "15m", "2h", 5, "30m"
	default:
		return "", fmt.Errorf("unsupported worker %s", bot.role)
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(state, ".town-bot-config-*")
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(c)
	if err == nil {
		err = f.Chmod(0600)
	}
	if err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

type botOutput struct {
	mu      sync.Mutex
	tail    osrun.Tail
	pending []byte
	observe func(Progress)
}

func newBotOutput(observe func(Progress)) *botOutput {
	return &botOutput{tail: osrun.Tail{Capacity: 64 << 10}, observe: observe}
}

func (o *botOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	_, _ = o.tail.Write(p)
	if len(o.pending)+len(p) > 64<<10 {
		o.pending = append(o.pending[:0], p...)
		if len(o.pending) > 64<<10 {
			o.pending = o.pending[len(o.pending)-(64<<10):]
		}
	} else {
		o.pending = append(o.pending, p...)
	}
	for {
		index := bytes.IndexByte(o.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(string(o.pending[:index]))
		o.pending = o.pending[index+1:]
		if line != "" {
			o.observeLine(line)
		}
	}
	return n, nil
}

func (o *botOutput) observeLine(line string) {
	var record struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(line), &record) != nil || record.Msg == "" {
		return
	}
	task := record.Msg
	if record.Error != "" {
		task += ": " + record.Error
	}
	phase := "running"
	switch {
	case strings.Contains(strings.ToLower(record.Msg), "investigat"):
		phase = "investigating"
	case strings.Contains(strings.ToLower(record.Msg), "review"):
		phase = "reviewing"
	case strings.Contains(strings.ToLower(record.Msg), "publish"):
		phase = "publishing"
	case record.Level == "ERROR":
		phase = "failed"
	}
	if o.observe != nil {
		o.observe(Progress{Phase: phase, Task: truncate(task, 4096)})
	}
}

func (o *botOutput) text() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	text, _ := o.tail.Text()
	return text
}

func runExternalBot(ctx context.Context, bot externalBot, config string, observe func(Progress), log *slog.Logger) error {
	output := newBotOutput(observe)
	stdout := &osrun.Tail{Capacity: 64 << 10}
	cmd := osrun.StartCommand(ctx, "", []string{bot.command, "--config", config, "--once", "--json"}, nil)
	cmd.Stdout = stdout
	cmd.Stderr = output
	err := cmd.Run()
	detail := output.text()
	if out, cut := stdout.Text(); cut {
		err = errors.Join(err, errors.New(bot.command+" stdout exceeded 64 KiB"))
		_ = out
	} else if strings.TrimSpace(out) != "" {
		log.Info("External bot output", "role", string(bot.role), "output", truncate(out, 4096))
	}
	if err != nil {
		return fmt.Errorf("%s bot %s failed: %w\n%s", bot.role, bot.version, err, truncate(detail, 8192))
	}
	if detail != "" {
		log.Info("External bot diagnostics", "role", string(bot.role), "output", truncate(detail, 4096))
	}
	return nil
}

type issueExternalState struct {
	Format    int                         `json:"format"`
	Remote    string                      `json:"remote"`
	Branch    string                      `json:"branch"`
	Directory string                      `json:"directory"`
	Repo      string                      `json:"repo"`
	Host      string                      `json:"host"`
	Jobs      map[string]issueExternalJob `json:"jobs"`
}

type issueExternalJob struct {
	Issue struct {
		Number int `json:"number"`
	} `json:"issue"`
	Branch string `json:"branch"`
	Status string `json:"status"`
	URL    string `json:"url"`
}

func readIssueExternalState(path, remote, branch, directory, repo string) (*issueExternalState, error) {
	data, err := os.ReadFile(filepath.Join(path, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state issueExternalState
	if err = json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("invalid issue-bot state: %w", err)
	}
	if state.Format != 1 || state.Remote != remote || state.Branch != branch || state.Directory != directory || state.Repo != repo || state.Host != "github.com" {
		return nil, errors.New("issue-bot state identity does not match this dispatch")
	}
	for key, job := range state.Jobs {
		number, err := strconv.Atoi(key)
		if err != nil || number < 1 || job.Issue.Number != number {
			return nil, errors.New("invalid saved issue job")
		}
	}
	return &state, nil
}

func issueOwnership(state *issueExternalState, repo string) map[int]Ownership {
	owned := map[int]Ownership{}
	if state == nil {
		return owned
	}
	for _, job := range state.Jobs {
		if job.Status != "submitted" {
			continue
		}
		u, err := url.Parse(job.URL)
		if err != nil || u.Host != "github.com" {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 4 || !strings.EqualFold(strings.Join(parts[:2], "/"), repo) || parts[2] != "pull" {
			continue
		}
		if number, err := strconv.Atoi(parts[3]); err == nil && number > 0 {
			owned[number] = Ownership{Branch: job.Branch, Issue: job.Issue.Number}
		}
	}
	return owned
}

type reviewExternalState struct {
	Format    int                 `json:"format"`
	Remote    string              `json:"remote"`
	Branch    string              `json:"branch"`
	Directory string              `json:"directory"`
	Repo      string              `json:"repo"`
	Host      string              `json:"host"`
	Jobs      []reviewExternalJob `json:"jobs"`
}

type reviewExternalJob struct {
	Key        string                    `json:"key"`
	PR         reviewExternalPull        `json:"pr"`
	DryRun     bool                      `json:"dry_run"`
	Status     string                    `json:"status"`
	Candidates []reviewExternalCandidate `json:"candidates"`
	Payload    *reviewExternalPayload    `json:"payload"`
	Actor      string                    `json:"actor"`
}

type reviewExternalPull struct {
	Number int                  `json:"number"`
	Title  string               `json:"title"`
	URL    string               `json:"url"`
	Base   reviewExternalGitRef `json:"base"`
	Head   reviewExternalGitRef `json:"head"`
}

type reviewExternalGitRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type reviewExternalCandidate struct {
	Finding reviewExternalFinding `json:"finding"`
	Verdict string                `json:"verdict"`
	Reason  string                `json:"reason"`
}

type reviewExternalFinding struct {
	Path        string   `json:"path"`
	Title       string   `json:"title"`
	Trigger     string   `json:"trigger"`
	Explanation string   `json:"explanation"`
	Evidence    []string `json:"evidence"`
}

type reviewExternalPayload struct {
	Commit string `json:"commit"`
	Event  string `json:"event"`
	Body   string `json:"body"`
}

func readReviewExternalState(path, remote, branch, directory, repo string) (*reviewExternalState, error) {
	data, err := os.ReadFile(filepath.Join(path, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state reviewExternalState
	if err = json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("invalid review-bot state: %w", err)
	}
	if state.Format != 1 || state.Remote != remote || state.Branch != branch || state.Directory != directory || state.Repo != repo || state.Host != "github.com" {
		return nil, errors.New("review-bot state identity does not match this dispatch")
	}
	seen := map[string]bool{}
	for _, job := range state.Jobs {
		if !SHA(job.PR.Head.SHA) || !SHA(job.PR.Base.SHA) || job.PR.Number < 1 || job.Key != reviewRevisionKey(repo, job.PR) {
			return nil, errors.New("invalid saved review job")
		}
		key := job.Key + ":" + strconv.FormatBool(job.DryRun)
		if seen[key] {
			return nil, errors.New("duplicate saved review job")
		}
		seen[key] = true
		switch job.Status {
		case "pending", "failed", "posting", "submitted", "dry_run", "stale":
		default:
			return nil, errors.New("invalid saved review status")
		}
		if job.Status == "posting" || job.Status == "submitted" {
			marker := "<!-- review-bot:v1:" + job.Key + " -->"
			if job.DryRun || job.Actor == "" || job.Payload == nil || job.Payload.Commit != job.PR.Head.SHA || job.Payload.Event != "COMMENT" || !strings.Contains(job.Payload.Body, marker) {
				return nil, errors.New("invalid saved review publication intent")
			}
		}
	}
	return &state, nil
}

func reviewRevisionKey(repo string, p reviewExternalPull) string {
	value := fmt.Sprintf("github.com/%s#%d:%s:%s:%s", strings.ToLower(repo), p.Number, p.Base.Ref, p.Base.SHA, p.Head.SHA)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func reviewKnownFindings(state *reviewExternalState) map[string]string {
	known := map[string]string{}
	for _, job := range state.Jobs {
		for _, candidate := range job.Candidates {
			if candidate.Verdict == "invalid" {
				continue
			}
			id := Key(candidate.Finding.Path + candidate.Finding.Title + candidate.Finding.Trigger)
			known[id] = fmt.Sprintf("%s: %s\n%s\nTrigger: %s\nEvidence: %s\nVerifier: %s", candidate.Finding.Path, candidate.Finding.Title, candidate.Finding.Explanation, candidate.Finding.Trigger, strings.Join(candidate.Finding.Evidence, "; "), candidate.Reason)
		}
	}
	return known
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…[truncated]"
}

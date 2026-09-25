package town

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	bundle "github.com/BrokkAi/brokk-town"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

const workerProtocolVersion = 1

// workerRequeueCapability marks an issue worker that can start an issue over
// after Town closed its pull request.
const workerRequeueCapability = "requeue"

// workerPolicyCapability marks a worker that reads Town's work policy:
// label filters, a pinned item, a focus, limits and per-house verification.
// Town refuses to dispatch a configured policy to a worker without it rather
// than letting the bot run unfiltered.
const workerPolicyCapability = "policy"

var workerBotNames = map[Role]string{
	Bug: "bug-bot", Feature: "feature-bot", Issue: "issue-bot", Review: "review-bot", Release: "release-bot", Simplifier: "simplifier-bot", Repo: "repo-bot", Hall: "mayor-bot",
}
var workerCapabilities = map[Role][]string{
	Bug:        {"run", "progress", "bug-scan"},
	Feature:    {"run", "progress", "feature-research"},
	Issue:      {"run", "progress", "issue-result", "exact-issue"},
	Review:     {"run", "progress", "exact-revision-review"},
	Release:    {"run", "progress", "release"},
	Simplifier: {"run", "progress", "simplifier-review"},
	Repo:       {"run", "progress", "repo-inventory", "branch-health"},
	Hall:       {"run", "progress", "mayor-judgment", "mayor-bulletin"},
}
var workerVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

var errStopWorker = errors.New("worker stop requested")

func stopRequested(ctx context.Context) bool { return errors.Is(context.Cause(ctx), errStopWorker) }

type externalBot struct {
	role    Role
	command string
	args    []string
	version string
	hash    string
}

type workerInitialize struct {
	Protocol        int      `json:"protocol"`
	MinimumProtocol int      `json:"minimum_protocol"`
	Bot             string   `json:"bot"`
	Version         string   `json:"version"`
	Capabilities    []string `json:"capabilities"`
}

func (i workerInitialize) has(capability string) bool {
	for _, c := range i.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

type workerRequest struct {
	Protocol       int                `json:"protocol"`
	Remote         string             `json:"remote"`
	Branch         string             `json:"branch"`
	Directory      string             `json:"directory"`
	StateDirectory string             `json:"state_directory"`
	Repo           string             `json:"repo"`
	Host           string             `json:"host"`
	Agent          runner.AgentConfig `json:"agent"`
	Verify         []string           `json:"verify,omitempty"`
	Issue          int                `json:"issue,omitempty"`
	PR             int                `json:"pr,omitempty"`
	BaseSHA        string             `json:"base_sha,omitempty"`
	HeadSHA        string             `json:"head_sha,omitempty"`
	Mode           string             `json:"mode,omitempty"`
	// SinceHead and Commits are the repo worker's inventory inputs: the branch
	// head Town last observed, and the revisions it still needs release
	// ancestry for. Town's task graph never crosses the protocol.
	SinceHead string   `json:"since_head,omitempty"`
	Commits   []string `json:"commits,omitempty"`
	// SupersededPR asks the issue worker to start the issue over because Town
	// closed this pull request after review. Requires the "requeue" capability.
	SupersededPR int `json:"superseded_pr,omitempty"`
	// Arrival is the town's description of the item a Mayor judgment decides;
	// Since and Until bound the window a Mayor bulletin summarizes.
	Arrival json.RawMessage `json:"arrival,omitempty"`
	Since   *time.Time      `json:"since,omitempty"`
	Until   *time.Time      `json:"until,omitempty"`
	// Policy is the operator's work selection and limits for this house.
	// Requires the "policy" capability whenever it changes anything.
	Policy *BotPolicy `json:"policy,omitempty"`
}

type workerProgress struct {
	Phase string `json:"phase"`
	Task  string `json:"task"`
}

type workerIssueOwnership struct {
	PR     int    `json:"pr"`
	Branch string `json:"branch"`
	Issue  int    `json:"issue"`
}

type workerIssueResult struct {
	Owned []workerIssueOwnership `json:"owned"`
}

type workerReviewResult struct {
	Status     string            `json:"status,omitempty"`
	Detail     string            `json:"detail,omitempty"`
	Complete   bool              `json:"complete"`
	Findings   map[string]string `json:"findings,omitempty"`
	Severities map[string]string `json:"severities,omitempty"`
	ExactBase  string            `json:"exact_base,omitempty"`
	ExactHead  string            `json:"exact_head,omitempty"`
}

type workerResult struct {
	terminal       bool
	Jobs           map[int]*issueJobSummary `json:"jobs,omitempty"`
	Issue          *workerIssueResult       `json:"issue,omitempty"`
	Review         *workerReviewResult      `json:"review,omitempty"`
	Simplification *workerSimplification    `json:"simplification,omitempty"`
	Judgment       *Judgment                `json:"judgment,omitempty"`
	Bulletin       *Bulletin                `json:"bulletin,omitempty"`
	Inventory      *workerInventory         `json:"inventory,omitempty"`
	Health         *BranchHealth            `json:"health,omitempty"`
	Usage          *OutcomeUsage            `json:"usage,omitempty"`
	CostUSD        *float64                 `json:"cost_usd,omitempty"`
	// retried records an accepted POST /v1/retry before this run.
	retried bool
}

// workerInventory is the repo worker's observation of the repository. Its
// fields are the remote types Town already reconciles, so one observation is
// read once and applied without a translation layer in between.
type workerInventory struct {
	Branch        string          `json:"branch"`
	DefaultBranch string          `json:"default_branch"`
	Head          string          `json:"head"`
	Issues        []RemoteIssue   `json:"issues"`
	Pulls         []Pull          `json:"pulls"`
	Releases      []RemoteRelease `json:"releases"`
	Commits       []RemoteCommit  `json:"commits,omitempty"`
	Released      map[string]bool `json:"released,omitempty"`
}

func (i workerInventory) snapshot() RepoSnapshot {
	return RepoSnapshot{
		Branch: i.Branch, DefaultBranch: i.DefaultBranch, Head: i.Head,
		Issues: i.Issues, Pulls: i.Pulls, Releases: i.Releases, Commits: i.Commits, Released: i.Released,
	}
}

type workerSimplification struct {
	Mode     string `json:"mode"`
	Decision string `json:"decision"`
	Summary  string `json:"summary,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type workerEvent struct {
	Type     string          `json:"type"`
	Seq      uint64          `json:"seq"`
	Progress *workerProgress `json:"progress,omitempty"`
	Result   *workerResult   `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func (b *BotWorkers) externalBot(ctx context.Context, cfg Config, role Role) (externalBot, error) {
	spec, ok := bundle.Bots()[string(role)]
	if !ok {
		return externalBot{}, fmt.Errorf("unsupported worker %s", role)
	}
	exe, err := os.Executable()
	if err != nil {
		return externalBot{}, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return externalBot{}, err
	}
	path := filepath.Join(filepath.Dir(exe), spec.Command)
	if b.botCommands[role] != "" {
		path = b.botCommands[role]
	}
	var args []string
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return externalBot{}, fmt.Errorf("resolve %s bot: %w", role, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return externalBot{}, fmt.Errorf("%s bot is not a regular executable", role)
	}
	bot := externalBot{role: role, command: resolved, args: args}
	if bot.hash, err = fileHash(resolved); err != nil {
		return externalBot{}, err
	}
	version, err := osrun.Run(ctx, "", nil, append([]string{resolved}, append(args, "version")...)...)
	if err != nil {
		return externalBot{}, fmt.Errorf("read %s bot version: %w", role, err)
	}
	if !workerVersionPattern.MatchString(version) {
		return externalBot{}, fmt.Errorf("%s bot returned an invalid version %q", role, truncate(version, 128))
	}
	if b.botCommands[role] == "" && strings.TrimPrefix(version, "v") != spec.Version {
		return externalBot{}, fmt.Errorf("%s bundle version mismatch: got %s, expected %s", role, version, spec.Version)
	}
	bot.version = version
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

func workerClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socketPath)
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func postWorkerRetry(ctx context.Context, client *http.Client, request workerRequest) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://worker/v1/retry", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("request worker retry: %w", err)
	}
	defer response.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	switch response.StatusCode {
	case http.StatusOK, http.StatusConflict:
		return nil
	default:
		return fmt.Errorf("worker retry returned HTTP %d: %s", response.StatusCode, truncate(string(detail), 1024))
	}
}

func getWorkerInitialize(ctx context.Context, client *http.Client, patience time.Duration) (workerInitialize, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://worker/v1/initialize", nil)
	if err != nil {
		return workerInitialize{}, err
	}
	for deadline := time.Now().Add(patience); time.Now().Before(deadline); {
		response, err := client.Do(request)
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return workerInitialize{}, fmt.Errorf("worker initialization returned HTTP %d", response.StatusCode)
			}
			if response.Header.Get("X-Brokk-Worker-Protocol") != fmt.Sprint(workerProtocolVersion) {
				return workerInitialize{}, errors.New("worker returned an unexpected protocol header")
			}
			var info workerInitialize
			if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&info); err != nil {
				return workerInitialize{}, fmt.Errorf("decode worker initialization: %w", err)
			}
			return info, nil
		}
		select {
		case <-ctx.Done():
			return workerInitialize{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return workerInitialize{}, errors.New("worker initialization timed out")
}

func validateWorkerInitialize(bot externalBot, info workerInitialize) error {
	if info.Bot != workerBotNames[bot.role] {
		return fmt.Errorf("expected %s worker, got %q", workerBotNames[bot.role], info.Bot)
	}
	if info.Version != bot.version {
		return fmt.Errorf("%s worker version changed during startup: CLI %s, service %s", bot.role, bot.version, info.Version)
	}
	if info.Protocol != workerProtocolVersion || info.MinimumProtocol > workerProtocolVersion || info.MinimumProtocol < 1 {
		return fmt.Errorf("%s worker protocol is incompatible: supports %d through %d", bot.role, info.MinimumProtocol, info.Protocol)
	}
	for _, capability := range workerCapabilities[bot.role] {
		if !info.has(capability) {
			return fmt.Errorf("%s worker %s does not advertise %q", bot.role, info.Version, capability)
		}
	}
	return nil
}

func postWorkerRun(ctx context.Context, client *http.Client, request workerRequest, observe func(Progress)) (workerResult, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return workerResult{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://worker/v1/runs", bytes.NewReader(body))
	if err != nil {
		return workerResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/x-ndjson")
	response, err := client.Do(httpRequest)
	if err != nil {
		return workerResult{}, fmt.Errorf("start worker run: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return workerResult{}, fmt.Errorf("worker run returned HTTP %d: %s", response.StatusCode, truncate(string(detail), 4096))
	}
	return consumeWorkerEvents(response.Body, 0, observe)
}

func consumeWorkerEvents(body io.Reader, after uint64, observe func(Progress)) (workerResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	var result workerResult
	var runErr error
	seenFinal := false
	sequence := after
	for scanner.Scan() {
		var event workerEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return result, fmt.Errorf("invalid worker event: %w", err)
		}
		if event.Seq != sequence+1 {
			return result, fmt.Errorf("worker event sequence gap: got %d, want %d", event.Seq, sequence+1)
		}
		sequence = event.Seq
		if seenFinal {
			return result, errors.New("worker sent an event after the final event")
		}
		switch event.Type {
		case "progress":
			if event.Progress == nil {
				return result, errors.New("worker progress event omitted progress")
			}
			phase, task := strings.TrimSpace(event.Progress.Phase), strings.TrimSpace(event.Progress.Task)
			if phase == "" {
				phase = "running"
			}
			observe(Progress{Phase: phase, Task: truncate(task, 4096), Seq: event.Seq})
		case "result":
			if event.Result == nil {
				return result, errors.New("worker result event omitted result")
			}
			result = *event.Result
		case "error":
			runErr = errors.New(truncate(event.Error, 8192))
			seenFinal = true
		case "complete":
			seenFinal = true
		case "canceled":
			runErr = context.Canceled
			seenFinal = true
		default:
			return result, fmt.Errorf("unknown worker event %q", event.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read worker event stream: %w", err)
	}
	if !seenFinal {
		return result, errors.New("worker event stream ended without a final event")
	}
	result.terminal = seenFinal && !errors.Is(runErr, context.Canceled)
	return result, runErr
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…[truncated]"
}

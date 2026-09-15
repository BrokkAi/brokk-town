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
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

const workerProtocolVersion = 1

var workerPackageNames = map[Role]string{
	Bug: "@brokkai/bug-bot", Feature: "@brokkai/feature-bot", Issue: "@brokkai/issue-bot", Review: "@brokkai/review-bot", Release: "@brokkai/release-bot",
}
var workerDefaultVersions = map[Role]string{
	Bug: "0.3.1", Feature: "0.1.1", Issue: "0.5.2", Review: "0.2.1", Release: "0.5.1",
}
var workerBotNames = map[Role]string{
	Bug: "bug-bot", Feature: "feature-bot", Issue: "issue-bot", Review: "review-bot", Release: "release-bot",
}
var workerCapabilities = map[Role][]string{
	Bug:     {"run", "progress", "bug-scan"},
	Feature: {"run", "progress", "feature-research"},
	Issue:   {"run", "progress", "issue-result", "exact-issue"},
	Review:  {"run", "progress", "exact-revision-review"},
	Release: {"run", "progress", "release"},
}
var workerVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

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
	Complete  bool              `json:"complete"`
	Findings  map[string]string `json:"findings,omitempty"`
	ExactBase string            `json:"exact_base,omitempty"`
	ExactHead string            `json:"exact_head,omitempty"`
}

type workerResult struct {
	Issue  *workerIssueResult  `json:"issue,omitempty"`
	Review *workerReviewResult `json:"review,omitempty"`
	// retried records that the worker accepted a POST /v1/retry before the run.
	retried bool
}

type workerEvent struct {
	Type     string          `json:"type"`
	Seq      uint64          `json:"seq"`
	Progress *workerProgress `json:"progress,omitempty"`
	Result   *workerResult   `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func (b *BotWorkers) externalBot(ctx context.Context, cfg Config, role Role) (externalBot, error) {
	packageName, ok := workerPackageNames[role]
	if !ok {
		return externalBot{}, fmt.Errorf("unsupported worker %s", role)
	}
	packageSpec := packageName + "@" + cfg.BotVersion(role)
	name := "npx"
	args := []string{"--yes", packageSpec}
	if b.BotCommands != nil {
		if override, ok := b.BotCommands[role]; ok && strings.TrimSpace(override) != "" {
			name = strings.TrimSpace(override)
			args = nil
		}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return externalBot{}, fmt.Errorf("start %s bot: %q is not installed on the service PATH", role, name)
	}
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

func (b externalBot) verify(ctx context.Context) error {
	if err := b.unchanged(); err != nil {
		return err
	}
	version, err := osrun.Run(ctx, "", nil, append([]string{b.command}, append(b.args, "version")...)...)
	if err != nil {
		return fmt.Errorf("recheck %s bot version: %w", b.role, err)
	}
	if version != b.version {
		return fmt.Errorf("%s bot version changed during dispatch: started %s, finished %s", b.role, b.version, version)
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

// runWorker starts one worker service, negotiates its version and capabilities,
// optionally lifts the bot's attempt budget through POST /v1/retry, streams one
// run, and stops the service. retry requires the worker's "retry" capability.
func runWorker(ctx context.Context, bot externalBot, request workerRequest, retry bool, observe func(Progress), log *slog.Logger) (workerResult, error) {
	socketDir, err := os.MkdirTemp("", "bt-worker-")
	if err != nil {
		return workerResult{}, err
	}
	defer os.RemoveAll(socketDir)
	socketPath := filepath.Join(socketDir, "worker.sock")
	serviceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stderr := &osrun.Tail{Capacity: 64 << 10}
	stdout := &osrun.Tail{Capacity: 16 << 10}
	command := append([]string{bot.command}, bot.args...)
	command = append(command, "worker", "--socket", socketPath)
	cmd := osrun.StartCommand(serviceCtx, "", command, nil)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err = cmd.Start(); err != nil {
		return workerResult{}, fmt.Errorf("start %s bot worker: %w", bot.role, err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socketPath)
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	info, err := getWorkerInitialize(ctx, client)
	if err != nil {
		cancel()
		_ = <-waitDone
		return workerResult{}, errors.Join(err, workerDiagnostics(bot, stdout, stderr))
	}
	if err = validateWorkerInitialize(bot, info); err != nil {
		cancel()
		_ = <-waitDone
		return workerResult{}, err
	}
	retried := false
	if retry {
		if !slices.Contains(info.Capabilities, "retry") {
			cancel()
			_ = <-waitDone
			return workerResult{}, fmt.Errorf("%s worker %s does not advertise %q; pin a release that supports retry", bot.role, info.Version, "retry")
		}
		observe(Progress{Phase: "starting", Task: "Resetting the " + string(bot.role) + " bot's attempt budget"})
		if err = postWorkerRetry(ctx, client, request); err != nil {
			cancel()
			_ = <-waitDone
			return workerResult{}, err
		}
		retried = true
	}
	result, runErr := postWorkerRun(ctx, client, request, observe)
	result.retried = retried
	if ctx.Err() == nil {
		if err = shutdownWorker(ctx, client); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("stop worker service: %w", err))
		}
	}
	select {
	case processErr := <-waitDone:
		if processErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("worker service exited: %w", processErr), workerDiagnostics(bot, stdout, stderr))
		}
	case <-time.After(5 * time.Second):
		cancel()
		processErr := <-waitDone
		runErr = errors.Join(runErr, fmt.Errorf("worker service did not stop cleanly: %w", processErr), workerDiagnostics(bot, stdout, stderr))
	}
	if err = bot.verify(ctx); err != nil {
		runErr = errors.Join(runErr, err)
	}
	return result, runErr
}

// postWorkerRetry asks the worker to reset its pending job's attempt budget for
// the workspace in request. The worker answers 409 when there is no pending
// job; that consumes the request too, since the following run starts fresh.
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

func shutdownWorker(ctx context.Context, client *http.Client) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://worker/v1/shutdown", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, truncate(string(detail), 1024))
	}
	return nil
}

func getWorkerInitialize(ctx context.Context, client *http.Client) (workerInitialize, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://worker/v1/initialize", nil)
	if err != nil {
		return workerInitialize{}, err
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
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
	capabilities := map[string]bool{}
	for _, capability := range info.Capabilities {
		capabilities[capability] = true
	}
	for _, capability := range workerCapabilities[bot.role] {
		if !capabilities[capability] {
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
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	var result workerResult
	var runErr error
	seenFinal := false
	var sequence uint64
	for scanner.Scan() {
		var event workerEvent
		if err = json.Unmarshal(scanner.Bytes(), &event); err != nil {
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
			observe(Progress{Phase: phase, Task: truncate(task, 4096)})
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
	if err = scanner.Err(); err != nil {
		return result, fmt.Errorf("read worker event stream: %w", err)
	}
	if !seenFinal {
		return result, errors.New("worker event stream ended without a final event")
	}
	return result, runErr
}

func workerDiagnostics(bot externalBot, stdout, stderr *osrun.Tail) error {
	out, outCut := stdout.Text()
	detail, errCut := stderr.Text()
	if !outCut && strings.TrimSpace(out) == "" && !errCut && strings.TrimSpace(detail) == "" {
		return nil
	}
	return fmt.Errorf("%s bot %s diagnostics:\nstdout:\n%s\nstderr:\n%s", bot.role, bot.version, truncate(out, 4096), truncate(detail, 8192))
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…[truncated]"
}

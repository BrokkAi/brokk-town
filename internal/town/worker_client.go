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
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

const workerProtocolVersion = 1

// workerDetachCapability marks a worker that keeps running when its run stream
// loses the Town client, buffers every event, and serves GET /v1/attach so a
// restarted service can resume where the previous one stopped reading.
const workerDetachCapability = "detach"

var workerPackageNames = map[Role]string{
	Bug: "@brokkai/bug-bot", Feature: "@brokkai/feature-bot", Issue: "@brokkai/issue-bot", Review: "@brokkai/review-bot", Release: "@brokkai/release-bot", Simplifier: "@brokkai/simplifier-bot", Repo: "@brokkai/repo-bot",
}
var workerDefaultVersions = map[Role]string{
	Bug: "0.3.1", Feature: "0.1.1", Issue: "0.5.2", Review: "0.2.1", Release: "0.5.1", Simplifier: "0.1.0", Repo: "0.1.0",
}
var workerBotNames = map[Role]string{
	Bug: "bug-bot", Feature: "feature-bot", Issue: "issue-bot", Review: "review-bot", Release: "release-bot", Simplifier: "simplifier-bot", Repo: "repo-bot",
}
var workerCapabilities = map[Role][]string{
	Bug:        {"run", "progress", "bug-scan"},
	Feature:    {"run", "progress", "feature-research"},
	Issue:      {"run", "progress", "issue-result", "exact-issue"},
	Review:     {"run", "progress", "exact-revision-review"},
	Release:    {"run", "progress", "release"},
	Simplifier: {"run", "progress", "simplifier-review"},
	Repo:       {"run", "progress", "repo-inventory", "branch-health"},
}
var workerVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// errStopWorker is the cancellation cause for an operator stop, retry, or town
// deletion. Only that cause ends the bot process; a plain cancellation means the
// service itself is stopping and the process must be left for the next service.
var errStopWorker = errors.New("worker stop requested")

// errWorkerDetached reports that the bot process was left running with its
// durable handle intact because the service is shutting down.
var errWorkerDetached = errors.New("worker left running for the next town service")

// errWorkerNeverStarted means an adopted worker was still idle: the previous
// service stopped between recording the handle and submitting the run. Nothing
// happened, so it wraps context.Canceled and the house is simply rescheduled.
var errWorkerNeverStarted = fmt.Errorf("%w: the worker never received its run request", context.Canceled)

// WorkerOutcomeUnknownError means a bot ran across a service restart without a
// way to report its result. The work may or may not have landed on GitHub.
type WorkerOutcomeUnknownError struct {
	Bot, Version, Reason string
	processRunning       bool
}

func (e *WorkerOutcomeUnknownError) Error() string {
	return fmt.Sprintf("%s %s %s; its outcome is uncertain. Repo-bot reconciles GitHub state; retry if the work did not land.", e.Bot, e.Version, e.Reason)
}

func stopRequested(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errStopWorker)
}

// detachRequested is true only for a plain cancellation of a live dispatch,
// which is how service shutdown reaches workers. Stops and deadlines kill.
func detachRequested(ctx context.Context) bool {
	if ctx.Err() == nil {
		return false
	}
	cause := context.Cause(ctx)
	return errors.Is(cause, context.Canceled) && !errors.Is(cause, errStopWorker)
}

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
	Issue          *workerIssueResult    `json:"issue,omitempty"`
	Review         *workerReviewResult   `json:"review,omitempty"`
	Simplification *workerSimplification `json:"simplification,omitempty"`
	Inventory      *workerInventory      `json:"inventory,omitempty"`
	Health         *BranchHealth         `json:"health,omitempty"`
	Usage          *OutcomeUsage         `json:"usage,omitempty"`
	CostUSD        *float64              `json:"cost_usd,omitempty"`
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

// exitWait reports whether the worker process ended within timeout.
type exitWait func(timeout time.Duration) (exited bool, err error)

func childExit(waitDone <-chan error) exitWait {
	return func(timeout time.Duration) (bool, error) {
		select {
		case err := <-waitDone:
			return true, err
		case <-time.After(timeout):
			return false, nil
		}
	}
}

// orphanExit polls a process this service did not start. The socket handshake
// has already proven the process identity, so the PID is only a liveness signal.
func orphanExit(pid int) exitWait {
	return func(timeout time.Duration) (bool, error) {
		deadline := time.Now().Add(timeout)
		for osrun.Alive(pid) {
			if !time.Now().Before(deadline) {
				return false, nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		return true, nil
	}
}

// runWorker starts one detached bot process, records its durable handle through
// started before any work is requested, and streams the run. Service shutdown
// leaves the process running and returns errWorkerDetached; an operator stop or
// the dispatch deadline kills it.
func runWorker(ctx context.Context, bot externalBot, request workerRequest, retry bool, deadline time.Time, observe func(Progress), started func(WorkerRun) error) (workerResult, error) {
	socketDir, err := os.MkdirTemp("", "bt-worker-")
	if err != nil {
		return workerResult{}, err
	}
	cleanup := func() { _ = os.RemoveAll(socketDir) }
	socketPath := filepath.Join(socketDir, "worker.sock")
	outputPath := filepath.Join(socketDir, "worker.log")
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		cleanup()
		return workerResult{}, err
	}
	command := append([]string{bot.command}, bot.args...)
	command = append(command, "worker", "--socket", socketPath)
	cmd := osrun.StartDetached("", command, nil, output)
	if err = cmd.Start(); err != nil {
		output.Close()
		cleanup()
		return workerResult{}, fmt.Errorf("start %s bot worker: %w", bot.role, err)
	}
	output.Close()
	pid := cmd.Process.Pid
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	exit := childExit(waitDone)
	abort := func() {
		_ = osrun.KillGroup(pid)
		_, _ = exit(5 * time.Second)
		cleanup()
	}
	client := workerClient(socketPath)
	info, err := getWorkerInitialize(ctx, client, 10*time.Second)
	if err != nil {
		abort()
		return workerResult{}, errors.Join(err, workerDiagnostics(bot, outputPath))
	}
	if err = validateWorkerInitialize(bot, info); err != nil {
		abort()
		return workerResult{}, err
	}
	run := WorkerRun{
		Bot: info.Bot, Version: bot.version, Command: bot.command, Args: bot.args, Hash: bot.hash,
		PID: pid, Socket: socketPath, Output: outputPath, Detachable: info.has(workerDetachCapability),
		Started: time.Now(), Deadline: deadline,
		Issue: request.Issue, PR: request.PR, BaseSHA: request.BaseSHA, HeadSHA: request.HeadSHA,
		Mode: request.Mode,
	}
	if started != nil {
		if err = started(run); err != nil {
			abort()
			return workerResult{}, fmt.Errorf("record %s worker run: %w", bot.role, err)
		}
	}
	// Nothing has been asked of the bot yet, so a cancellation here has nothing
	// to preserve: end the process rather than leaving an idle one behind.
	if ctx.Err() != nil {
		abort()
		return workerResult{}, ctx.Err()
	}
	retried := false
	if retry {
		if !info.has("retry") {
			abort()
			return workerResult{}, fmt.Errorf("%s worker %s does not advertise %q; pin a release that supports retry", bot.role, info.Version, "retry")
		}
		observe(Progress{Phase: "starting", Task: "Resetting the " + string(bot.role) + " bot's attempt budget"})
		if err = postWorkerRetry(ctx, client, request); err != nil {
			abort()
			return workerResult{}, err
		}
		retried = true
	}
	result, runErr := postWorkerRun(ctx, client, request, observe)
	result.retried = retried
	if runErr != nil && ctx.Err() != nil {
		if detachRequested(ctx) {
			return workerResult{retried: retried}, errWorkerDetached
		}
		abort()
		return result, runErr
	}
	return finishWorker(bot, client, pid, exit, cleanup, outputPath, result, runErr)
}

// adoptWorker reconnects to a bot process recorded by an earlier service. A
// detach-capable worker replays its buffered events; any other worker is asked
// to shut down when it can, and its outcome is reported as uncertain because
// nothing observed it. Identity is proven over the socket before any kill.
func adoptWorker(ctx context.Context, role Role, run WorkerRun, observe func(Progress)) (workerResult, error) {
	bot := externalBot{role: role, command: run.Command, args: run.Args, version: run.Version, hash: run.Hash}
	cleanup := func() { _ = os.RemoveAll(filepath.Dir(run.Socket)) }
	client := workerClient(run.Socket)
	exit := orphanExit(run.PID)
	kill := func() {
		_ = osrun.KillGroup(run.PID)
		_, _ = exit(5 * time.Second)
		cleanup()
	}
	if !run.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, run.Deadline)
		defer cancel()
	}
	// Adoption must authenticate the recorded socket before it can safely kill
	// the process. Give only that initialization probe its own bounded lifetime
	// so a stop arriving while the request is in flight cannot interrupt proof
	// of identity. All later work continues to observe the original stop cause.
	probe, cancelProbe := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	info, err := getWorkerInitialize(probe, client, 2*time.Second)
	cancelProbe()
	if err != nil {
		// A live PID may still own this socket. Keep its endpoint available for
		// a later adoption attempt rather than unlinking the only safe way to
		// authenticate it before a kill.
		reason := "was running when the service restarted and could not be authenticated when it came back"
		alive := osrun.Alive(run.PID)
		if !alive {
			cleanup()
			reason = "was running when the service restarted and had already exited when it came back"
		}
		return workerResult{}, &WorkerOutcomeUnknownError{Bot: run.Bot, Version: run.Version, Reason: reason, processRunning: alive}
	}
	if err = validateWorkerInitialize(bot, info); err != nil {
		if stopRequested(ctx) && osrun.Alive(run.PID) {
			return workerResult{}, &WorkerOutcomeUnknownError{
				Bot: run.Bot, Version: run.Version, processRunning: true,
				Reason: fmt.Sprintf("was still running when Town tried to stop it but its identity could not be authenticated: %v", err),
			}
		}
		cleanup()
		return workerResult{}, fmt.Errorf("adopt %s worker: %w", role, err)
	}
	if stopRequested(ctx) {
		kill()
		return workerResult{}, context.Canceled
	}
	if !run.Detachable {
		observe(Progress{Phase: "waiting", Task: fmt.Sprintf("%s %s cannot report across a service restart; letting it finish", run.Bot, run.Version)})
		shutdownCtx, cancelShutdown := context.WithTimeout(ctx, 15*time.Second)
		_ = shutdownWorker(shutdownCtx, client)
		cancelShutdown()
		for osrun.Alive(run.PID) {
			select {
			case <-ctx.Done():
				if detachRequested(ctx) {
					return workerResult{}, errWorkerDetached
				}
				kill()
				return workerResult{}, ctx.Err()
			case <-time.After(time.Second):
			}
		}
		cleanup()
		return workerResult{}, &WorkerOutcomeUnknownError{Bot: run.Bot, Version: run.Version, Reason: "finished a run that started before the service restarted"}
	}
	result, runErr := attachWorker(ctx, client, run.Seq, observe)
	if runErr != nil && ctx.Err() != nil {
		if detachRequested(ctx) {
			return workerResult{}, errWorkerDetached
		}
		kill()
		return result, runErr
	}
	return finishWorker(bot, client, run.PID, exit, cleanup, run.Output, result, runErr)
}

// finishWorker runs the graceful end of a completed dispatch on its own bounded
// context: shutdown request, process exit, executable recheck, and cleanup.
func finishWorker(bot externalBot, client *http.Client, pid int, exit exitWait, cleanup func(), outputPath string, result workerResult, runErr error) (workerResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := shutdownWorker(ctx, client); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("stop worker service: %w", err))
	}
	exited, processErr := exit(5 * time.Second)
	if !exited {
		_ = osrun.KillGroup(pid)
		_, processErr = exit(5 * time.Second)
		runErr = errors.Join(runErr, fmt.Errorf("worker service did not stop cleanly: %w", processErr), workerDiagnostics(bot, outputPath))
	} else if processErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("worker service exited: %w", processErr), workerDiagnostics(bot, outputPath))
	}
	if err := bot.verify(ctx); err != nil {
		runErr = errors.Join(runErr, err)
	}
	cleanup()
	return result, runErr
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

// attachWorker resumes a detach-capable worker's event stream after the last
// sequence number Town durably observed. The worker replays later events and
// keeps streaming until its terminal event.
func attachWorker(ctx context.Context, client *http.Client, after uint64, observe func(Progress)) (workerResult, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://worker/v1/attach?after="+strconv.FormatUint(after, 10), nil)
	if err != nil {
		return workerResult{}, err
	}
	httpRequest.Header.Set("Accept", "application/x-ndjson")
	response, err := client.Do(httpRequest)
	if err != nil {
		return workerResult{}, fmt.Errorf("attach to worker run: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return workerResult{}, errWorkerNeverStarted
	}
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return workerResult{}, fmt.Errorf("worker attach returned HTTP %d: %s", response.StatusCode, truncate(string(detail), 4096))
	}
	return consumeWorkerEvents(response.Body, after, observe)
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
	return result, runErr
}

func workerDiagnostics(bot externalBot, outputPath string) error {
	out, _ := osrun.TailFile(outputPath, 64<<10)
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return fmt.Errorf("%s bot %s diagnostics:\n%s", bot.role, bot.version, truncate(out, 8192))
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…[truncated]"
}

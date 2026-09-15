package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/BrokkAi/brokk-town/internal/harness"
	"github.com/BrokkAi/brokk-town/internal/town"
	"github.com/BrokkAi/brokk-town/internal/web"
)

var version = "dev"

// connection is the service's advertisement to local clients. Version,
// executable, and start time let a newer client roll the service forward;
// managed records whether a login-session supervisor owns the process.
type connection struct {
	URL        string    `json:"url"`
	Token      string    `json:"token"`
	PID        int       `json:"pid"`
	Version    string    `json:"version,omitempty"`
	Executable string    `json:"executable,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	Managed    bool      `json:"managed,omitempty"`
}

// errRestart asks main to replace this process with the binary at its own
// path, keeping the PID so a login-session supervisor sees one continuous job.
var errRestart = errors.New("restart requested")

// executablePath resolves the real binary behind any launcher symlink, such as
// the npm shim, so registrations and restarts never depend on PATH.
var executablePath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, e := filepath.EvalSymlinks(exe); e == nil {
		exe = resolved
	}
	return exe, nil
}

// serviceToken persists the local access key so browser bookmarks, open tabs,
// and a polling TUI survive service restarts. Delete the file to rotate it.
func serviceToken(dir string) (string, error) {
	path := filepath.Join(dir, "token")
	if b, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(b))
		if _, e := hex.DecodeString(token); e == nil && len(token) == 64 {
			return token, nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	token := hex.EncodeToString(key)
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return "", err
	}
	return token, nil
}

// describeLock enriches the store's single-writer error with the running
// service's identity so the operator knows what holds the state directory.
func describeLock(dir string, err error) error {
	if !strings.Contains(err.Error(), "another town service is running") {
		return err
	}
	if conn, e := readConnection(dir); e == nil && conn.PID > 0 {
		return fmt.Errorf("%w (pid %d at %s; stop it with bt service stop, or Ctrl+C if it runs in a terminal)", err, conn.PID, conn.URL)
	}
	return err
}

func stateHome() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "brokk-town")
}
func buildVersion() string {
	v := version
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
	}
	return v
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := run(ctx, os.Args[1:])
	cancel()
	if errors.Is(err, errRestart) {
		// The store lock, listener, and connection file are already released.
		// Exec keeps the PID, so supervisors and the npm launcher see one job.
		exe, e := executablePath()
		if e == nil {
			e = syscall.Exec(exe, os.Args, os.Environ())
		}
		err = fmt.Errorf("restart failed: %w", e)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "bt:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	command := "tui"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	if command == "version" {
		fmt.Println(buildVersion())
		return nil
	}
	if command == "service" {
		return runService(ctx, args)
	}
	fs := flag.NewFlagSet("bt "+command, flag.ContinueOnError)
	dir := fs.String("state-dir", stateHome(), "private state directory")
	listen := fs.String("listen", defaultListen, "loopback HTTP address for the service; remembered for later starts")
	demo := fs.Bool("demo", false, "isolated simulated town (serve only)")
	repo := fs.String("repo", "", "GitHub OWNER/REPO")
	role := fs.String("role", "all", "bot to control or configure: bug, feature, issue, review, release; repo/all for controls; omit for town defaults in settings")
	task := fs.String("task", "", "task ID for retry; omit with --role release to reset the release bot's exhausted attempt budget")
	config := fs.String("config", "", "optional JSON array or object with max_workers and towns (serve only)")
	agentHarness := fs.String("harness", "", "ACP registry ID, anvil, muse-acp, draupnir, or custom (add/settings)")
	harnessVersion := fs.String("harness-version", "", "select an exact catalog version (add/settings)")
	refreshHarnesses := fs.Bool("refresh", false, "refresh the official ACP registry (harnesses)")
	model := fs.String("model", "", "ACP model ID; empty uses harness default (add/settings)")
	effort := fs.String("effort", "", "ACP reasoning effort; empty uses harness default (add/settings)")
	agentCommand := fs.String("agent-command", "", "custom ACP command as a JSON argument array (add/settings)")
	inherit := fs.Bool("inherit", false, "restore a bot's town defaults (settings --role BOT)")
	kind := fs.String("kind", "feature", "feature or bug (request)")
	title := fs.String("title", "", "GitHub issue title (request)")
	bodyFile := fs.String("body-file", "", "issue description file, or - for stdin (request)")
	requestID := fs.String("request-id", "", "saved submission ID (request/check-request)")
	maxWorkers := fs.Int("max-workers", 0, "maximum active bot workers across all towns (capacity)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Brokk Town — one local service, a browser town, and a terminal control panel.\n\nUsage: bt [tui|web|status|service|capacity|add|delete|harnesses|settings|request|check-request|start|pause|stop|retry|admit|decline|serve|version] [options]\n\nRun bt for the terminal panel or bt web for the browser address; either starts the town service when it is down and keeps it registered with your login session.\nAdd --demo for a simulated town. Use bt service to inspect, stop, or unregister the service, and bt serve to run it in the foreground.\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	base, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	abs := runtimeDir(base, *demo)
	if command == "serve" {
		return serve(ctx, abs, *listen, *demo, *config, *repo)
	}
	address, err := resolveListen(base, *demo, *listen, flagSet(fs, "listen"))
	if err != nil {
		return err
	}
	if command == "capacity" {
		if *maxWorkers < 1 || *maxWorkers > town.MaximumMaxWorkers {
			return fmt.Errorf("--max-workers is required and must be between 1 and %d", town.MaximumMaxWorkers)
		}
		conn, err := ensureService(ctx, base, *demo, address)
		if err != nil {
			return err
		}
		var capacity town.Capacity
		if err := request(ctx, conn, "POST", "/api/capacity", town.ServiceConfig{MaxWorkers: *maxWorkers}, &capacity); err != nil {
			return err
		}
		fmt.Printf("Capacity: %d active · limit %d\n", capacity.Active, capacity.Limit)
		return nil
	}
	agent := map[string]any{}
	roleSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "role":
			roleSet = true
		case "harness":
			agent["harness"] = *agentHarness
		case "harness-version":
			agent["version"] = *harnessVersion
		case "model":
			agent["model"] = *model
		case "effort":
			agent["effort"] = *effort
		}
	})
	if *agentCommand != "" {
		var args []string
		if err := json.Unmarshal([]byte(*agentCommand), &args); err != nil {
			return fmt.Errorf("--agent-command must be a JSON argument array: %w", err)
		}
		agent["command"] = args
	}
	if *inherit {
		if command != "settings" || !roleSet || *role == "all" || *role == "repo" || len(agent) != 0 {
			return errors.New("--inherit requires settings --role BOT and cannot be combined with agent settings")
		}
		agent["inherit"] = true
	}
	conn, err := ensureService(ctx, base, *demo, address)
	if err != nil {
		return err
	}
	switch command {
	case "harnesses":
		var catalog harness.Listing
		method, path := "GET", "/api/harnesses"
		var body any
		if *refreshHarnesses {
			method, path, body = "POST", "/api/harnesses/refresh", map[string]any{}
		}
		if err := request(ctx, conn, method, path, body, &catalog); err != nil {
			return err
		}
		fmt.Println("Official ACP registry:", catalog.Source)
		if catalog.Demo {
			fmt.Println("Demo uses the bundled registry offline.")
		} else if catalog.Stale {
			fmt.Println("Using a bundled or cached catalog; run bt harnesses --refresh to update.")
		}
		for _, a := range catalog.Agents {
			availability := ""
			if !a.Available {
				availability = " [unavailable on this platform]"
			}
			fmt.Printf("%-25s %-16s %s (%s)%s\n", a.ID, a.Version, a.Name, a.Source, availability)
		}
		fmt.Println("custom — supply an ACP command with --agent-command")
		return nil
	case "tui":
		return tui(ctx, conn)
	case "web":
		fmt.Printf("%s/#token=%s\n", conn.URL, conn.Token)
		return nil
	case "status":
		var state any
		if err := request(ctx, conn, "GET", "/api/state", nil, &state); err != nil {
			return err
		}
		b, _ := json.MarshalIndent(state, "", "  ")
		fmt.Println(string(b))
		return nil
	case "add":
		if *repo == "" {
			return errors.New("--repo OWNER/REPO is required")
		}
		var result any
		return request(ctx, conn, "POST", "/api/towns", map[string]any{"repo": *repo, "agent": agent}, &result)
	case "settings":
		if *repo == "" {
			return errors.New("--repo OWNER/REPO is required")
		}
		var result any
		settingsRole := ""
		if roleSet {
			if !town.ValidRole(town.Role(*role)) || *role == "repo" {
				return errors.New("settings --role must be bug, feature, issue, review, or release; omit --role to edit town defaults")
			}
			settingsRole = *role
		}
		return request(ctx, conn, "POST", "/api/settings", map[string]any{"town": strings.ToLower(*repo), "role": settingsRole, "agent": agent}, &result)
	case "request":
		if *repo == "" || *title == "" || *bodyFile == "" {
			return errors.New("--repo, --title, and --body-file are required")
		}
		var data []byte
		var err error
		if *bodyFile == "-" {
			data, err = io.ReadAll(io.LimitReader(os.Stdin, 30001))
		} else {
			f, e := os.Open(*bodyFile)
			if e != nil {
				return e
			}
			defer f.Close()
			data, err = io.ReadAll(io.LimitReader(f, 30001))
		}
		if err != nil {
			return err
		}
		if *requestID == "" {
			key := make([]byte, 16)
			if _, err = rand.Read(key); err != nil {
				return err
			}
			*requestID = hex.EncodeToString(key)
		}
		fmt.Println("Submission ID:", *requestID, "(reuse with --request-id if the connection is lost)")
		var result town.IssueRequest
		if err = request(ctx, conn, "POST", "/api/requests", map[string]string{"town": strings.ToLower(*repo), "id": *requestID, "kind": *kind, "title": *title, "body": string(data)}, &result); err != nil {
			return err
		}
		fmt.Println("Submission:", result.Status, "— view its progress in Town or bt status")
		return nil
	case "check-request":
		if *repo == "" || *requestID == "" {
			return errors.New("--repo and --request-id are required")
		}
		var result any
		return request(ctx, conn, "POST", "/api/requests/check", map[string]string{"town": strings.ToLower(*repo), "id": *requestID}, &result)
	case "start", "pause", "stop", "retry", "delete", "admit", "decline":
		if *repo == "" {
			return errors.New("--repo OWNER/REPO is required")
		}
		if (command == "admit" || command == "decline") && *task == "" {
			return errors.New("--task is required for a Mayoral decision")
		}
		if command == "admit" || command == "decline" {
			*role = "hall"
		}
		var result any
		return request(ctx, conn, "POST", "/api/control", map[string]string{"town": strings.ToLower(*repo), "role": *role, "action": command, "task": *task}, &result)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}
func readConnection(dir string) (connection, error) {
	var c connection
	b, err := os.ReadFile(filepath.Join(dir, "connection.json"))
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}
func request(ctx context.Context, c connection, method, path string, body, out any) error {
	var input io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		input = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, input)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	timeout := 35 * time.Second
	if path == "/api/update" {
		timeout = 130 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&v)
		return fmt.Errorf("town service: %s", v.Error)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}
func serve(ctx context.Context, dir, address string, demo bool, configFile, repo string) error {
	if err := loopbackAddress(address); err != nil {
		return err
	}
	store, err := town.Open(dir, demo)
	if err != nil {
		return describeLock(dir, err)
	}
	defer store.Close()
	if configFile != "" {
		data, e := os.ReadFile(configFile)
		if e != nil {
			return e
		}
		configs, maxWorkers, e := decodeConfigFile(data)
		if e != nil {
			return e
		}
		if demo {
			return errors.New("real config is not accepted in demo mode")
		}
		if e = store.Update(func(s *town.State) error {
			if maxWorkers != nil {
				cfg := town.ServiceConfig{MaxWorkers: *maxWorkers}
				if err := cfg.Validate(); err != nil {
					return err
				}
				s.ServiceConfig = cfg
			}
			for _, cfg := range configs {
				if err := cfg.Validate(); err != nil {
					return err
				}
				id := strings.ToLower(cfg.Repo)
				if t := s.Towns[id]; t != nil {
					if cfg.Branch == "" {
						cfg.Branch = t.Config.Branch
					}
					if t.Config.Branch != cfg.Branch && t.Initialized {
						return errors.New("cannot change an initialized town branch; use a separate state directory")
					}
					t.Config = cfg
				} else {
					if _, err := s.Add(cfg); err != nil {
						return err
					}
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	if repo != "" {
		if demo {
			return errors.New("real repository is not accepted in demo mode")
		}
		if _, ok := store.Snapshot().Towns[strings.ToLower(repo)]; !ok {
			if err = store.Update(func(s *town.State) error { _, err := s.Add(town.DefaultConfig(repo)); return err }); err != nil {
				return err
			}
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	token, err := serviceToken(dir)
	if err != nil {
		return err
	}
	exe, _ := executablePath()
	managed := os.Getenv("BROKK_TOWN_MANAGED") == "1"
	conn := connection{URL: "http://" + listener.Addr().String(), Token: token, PID: os.Getpid(), Version: buildVersion(), Executable: exe, Started: time.Now(), Managed: managed}
	data, _ := json.Marshal(conn)
	if err = os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600); err != nil {
		return err
	}
	if err = os.Chmod(filepath.Join(dir, "connection.json"), 0600); err != nil {
		return err
	}
	gh := town.GitHubClient{}
	workers := &town.BotWorkers{Root: dir, Store: store, GitHub: gh}
	supervisor := town.NewSupervisor(store, gh, workers)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var available atomic.Pointer[town.UpdateNotice]
	var upgradeMu sync.Mutex
	var restarting atomic.Bool
	server := &web.Server{Store: store, Supervisor: supervisor, Token: conn.Token, Origin: conn.URL, Version: buildVersion(), TaskGitHub: gh, Update: available.Load}
	server.Upgrade = func(upgradeCtx context.Context) error {
		upgradeMu.Lock()
		defer upgradeMu.Unlock()
		notice := available.Load()
		if notice == nil {
			return errors.New("no Town update is available")
		}
		installCtx, installCancel := context.WithTimeout(upgradeCtx, 2*time.Minute)
		defer installCancel()
		if e := town.InstallUpdate(installCtx, dir, exe, notice.Latest); e != nil {
			return fmt.Errorf("upgrade failed: %w", e)
		}
		available.Store(nil)
		return nil
	}
	// Restart replaces this process with whatever binary now sits at its own
	// path. The delay lets the HTTP response reach the client first.
	server.Restart = func() {
		if restarting.CompareAndSwap(false, true) {
			time.AfterFunc(time.Second, cancel)
		}
	}
	// Registry I/O stays off HTTP, TUI, and render loops. Failure is deliberately
	// quiet: inability to check must never prevent a local town from starting.
	go func() {
		client := &http.Client{Timeout: 8 * time.Second}
		check := func() {
			checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
			defer checkCancel()
			notice, e := town.CheckUpdate(checkCtx, client, buildVersion())
			if e == nil {
				if notice != nil {
					notice.Command = town.UpdateCommand(exe, notice.Latest)
				}
				available.Store(notice)
			}
		}
		check()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check()
			}
		}
	}()
	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, BaseContext: func(net.Listener) context.Context { return ctx }}
	results := make(chan error, 2)
	remaining := 2
	go func() { results <- httpServer.Serve(listener) }()
	if demo {
		go func() { results <- town.Demo(ctx, store) }()
	} else {
		go func() { results <- supervisor.Run(ctx) }()
	}
	fmt.Printf("Brokk Town %s\nBrowser: %s/#token=%s\nTerminal: bt tui --state-dir %s\n", buildVersion(), conn.URL, conn.Token, dir)
	if demo {
		fmt.Println("DEMO: simulated events only; no GitHub or agent processes.")
	}
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-results:
		remaining--
	}
	cancel()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if e := httpServer.Shutdown(shutdown); e != nil {
		_ = httpServer.Close()
	}
	// Keep the state lock until both HTTP and all workers have stopped. Every
	// subprocess receives cancellation; releasing early could permit two writers.
	for range remaining {
		<-results
	}
	_ = os.Remove(filepath.Join(dir, "connection.json"))
	if restarting.Load() && (errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed)) {
		return errRestart
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// loopbackAddress rejects anything but a literal loopback IP. The service has
// no remote authentication; an SSH tunnel is the supported remote path.
func loopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("bind to a loopback IP; use an SSH tunnel for remote access")
	}
	return nil
}

// decodeConfigFile accepts the original JSON array and the current object form
// with one global max_workers value. Keeping the array form means existing town
// files remain usable while the object form can persist service capacity beside
// the town list.
func decodeConfigFile(data []byte) ([]town.Config, *int, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var configs []town.Config
		d := json.NewDecoder(strings.NewReader(trimmed))
		d.DisallowUnknownFields()
		if err := d.Decode(&configs); err != nil {
			return nil, nil, err
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, nil, errors.New("expected one config array")
		}
		return configs, nil, nil
	}
	var file struct {
		MaxWorkers json.RawMessage `json:"max_workers"`
		Towns      []town.Config   `json:"towns"`
	}
	d := json.NewDecoder(strings.NewReader(trimmed))
	d.DisallowUnknownFields()
	if err := d.Decode(&file); err != nil {
		return nil, nil, err
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, nil, errors.New("expected one config object")
	}
	if file.Towns == nil {
		return nil, nil, errors.New("config object requires a towns array")
	}
	var limit *int
	if len(file.MaxWorkers) > 0 {
		if string(file.MaxWorkers) == "null" {
			return nil, nil, errors.New("max_workers cannot be null")
		}
		var value int
		if err := json.Unmarshal(file.MaxWorkers, &value); err != nil {
			return nil, nil, errors.New("max_workers must be an integer")
		}
		if err := (town.ServiceConfig{MaxWorkers: value}).Validate(); err != nil {
			return nil, nil, err
		}
		limit = &value
	}
	return file.Towns, limit, nil
}

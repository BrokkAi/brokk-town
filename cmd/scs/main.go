package main

import (
	"bytes"
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
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
	"github.com/BrokkAi/brokk-town/internal/town"
	"github.com/BrokkAi/brokk-town/internal/web"
)

var version = "dev"

// connection advertises the running local service and its identity.
type connection struct {
	URL     string `json:"url"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version,omitempty"`
}

// executablePath resolves the real binary behind any launcher symlink, such as
// the npm shim, so scs -d starts the binary itself.
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
// survive service restarts. Delete the file to rotate it.
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
	if !strings.Contains(err.Error(), "another SlopCop Squad service is running") {
		return err
	}
	if conn, e := readConnection(dir); e == nil && conn.PID > 0 {
		return fmt.Errorf("%w (pid %d at %s; stop it with scs shutdown, or Ctrl+C if it runs in a terminal)", err, conn.PID, conn.URL)
	}
	return err
}

func stateHome() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" && runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
	}
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "slopcop-squad")
	// Before the rename to SlopCop Squad the default was brokk-town. An install
	// from then keeps its state there until the operator moves it.
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		legacy := filepath.Join(base, "brokk-town")
		if info, err := os.Stat(legacy); err == nil && info.IsDir() {
			return legacy
		}
	}
	return dir
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

// shutdownSignals end the process through the ordinary shutdown sequence.
// SIGHUP belongs here: closing a terminal or dropping an SSH connection would
// otherwise kill the service outright, leaving its workers running in their own
// process groups and a stale connection.json advertising a dead URL.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

func main() {
	readyPipe = takeReadyPipe()
	ctx, cancel := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer cancel()
	err := run(ctx, os.Args[1:])
	cancel()
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "scs:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	// Bare scs, with only flags, runs Town; a first word names a process command.
	command := ""
	explicit := false
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
		explicit = true
	}
	fs := flag.NewFlagSet(strings.TrimSpace("scs "+command), flag.ContinueOnError)
	fl := addCLIFlags(fs)
	fs.Usage = func() {
		if !explicit {
			printRootHelp(fs.Output(), fs)
			return
		}
		printCommandHelp(fs.Output(), fs, command)
	}
	if command == "help" {
		if len(args) == 0 {
			printRootHelp(os.Stdout, fs)
			return nil
		}
		if findCommand(args[0]) != nil {
			printCommandHelp(os.Stdout, fs, args[0])
			return nil
		}
		return fmt.Errorf("unknown command %q for \"scs\"\nRun 'scs --help' for usage", args[0])
	}
	cmd := &rootCommand
	if explicit {
		cmd = findCommand(command)
	}
	if cmd == nil {
		return fmt.Errorf("unknown command %q for \"scs\"\nRun 'scs --help' for usage", command)
	}
	if wantsHelp(args) {
		if !explicit {
			printRootHelp(os.Stdout, fs)
			return nil
		}
		printCommandHelp(os.Stdout, fs, command)
		return nil
	}
	if command == "version" {
		fmt.Println(buildVersion())
		return nil
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s\nRun '%s --help' for usage", strings.Join(fs.Args(), " "), fs.Name())
	}
	if err := checkFlags(fs, cmd, explicit); err != nil {
		return err
	}
	base, err := filepath.Abs(*fl.dir)
	if err != nil {
		return err
	}
	abs := runtimeDir(base, *fl.demo)
	switch command {
	case "":
		if *fl.daemon {
			return startBackground(ctx, base, *fl.demo, *fl.listen, *fl.config)
		}
		return serve(ctx, abs, *fl.listen, *fl.demo, *fl.config)
	case "status":
		return printStatus(ctx, abs, *fl.json)
	case "shutdown":
		conn, alive := serviceAlive(ctx, abs)
		if !alive {
			return errors.New("SlopCop Squad is not running")
		}
		if err := stopProcess(ctx, conn); err != nil {
			return err
		}
		fmt.Println("SlopCop Squad stopped")
		return nil
	case "web":
		conn, err := requireService(ctx, abs)
		if err != nil {
			return err
		}
		fmt.Printf("%s/#token=%s\n", conn.URL, conn.Token)
		return nil
	default:
		return fmt.Errorf("unknown command %q for \"scs\"\nRun 'scs --help' for usage", command)
	}
}

// browserLink returns the browser address with its access key only when
// stdout is an interactive terminal. Redirected output, such as the background
// service's log, gets a pointer to scs web so the key never lands in a file.
func browserLink(conn connection, demo bool) string {
	if stdoutIsTerminal() {
		return conn.URL + "/#token=" + conn.Token
	}
	if demo {
		return "run scs web --demo for the link"
	}
	return "run scs web for the link"
}

var stdoutIsTerminal = func() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// serveBanner is what the foreground service prints once it is listening.
func serveBanner(conn connection, demo bool) string {
	return fmt.Sprintf("SlopCop Squad %s\nBrowser: %s\n", buildVersion(), browserLink(conn, demo))
}

// backgroundBanner is what scs -d prints once the detached service is ready.
func backgroundBanner(conn connection, demo bool, logs string) string {
	return fmt.Sprintf("SlopCop Squad running (pid %d)\nBrowser: %s\nLogs: %s\n", conn.PID, browserLink(conn, demo), logs)
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
		// Send text as written: HTML escaping would inflate <, > and & sixfold
		// and push a valid body past the service's request size limit.
		var b bytes.Buffer
		encoder := json.NewEncoder(&b)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(body); err != nil {
			return err
		}
		input = &b
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
		return fmt.Errorf("SlopCop Squad service: %s", v.Error)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}
func serve(ctx context.Context, dir, address string, demo bool, configFile string) error {
	if err := loopbackAddress(address); err != nil {
		return err
	}
	store, err := town.Open(dir, demo)
	if err != nil {
		return describeLock(dir, err)
	}
	defer store.Close()
	if notice := store.Notice(); notice != "" {
		fmt.Fprintf(os.Stderr, "scs: %s\n", notice)
	}
	if configFile != "" {
		data, e := os.ReadFile(configFile)
		if e != nil {
			return e
		}
		configs, service, e := decodeConfigFile(data)
		if e != nil {
			return e
		}
		if demo {
			return errors.New("real config is not accepted in demo mode")
		}
		var restored []string
		if e = store.Update(func(s *town.State) error {
			restored = nil
			if service.MaxWorkers != nil {
				if err := (town.ServiceConfig{MaxWorkers: *service.MaxWorkers}).Validate(); err != nil {
					return err
				}
				s.ServiceConfig.MaxWorkers = *service.MaxWorkers
			}
			if service.QuietHours != nil {
				if err := town.ValidateQuietHours(*service.QuietHours); err != nil {
					return err
				}
				s.ServiceConfig.QuietHours = nil
				if len(*service.QuietHours) > 0 {
					s.ServiceConfig.QuietHours = *service.QuietHours
				}
			}
			if service.AttentionHook != nil {
				s.ServiceConfig.AttentionHook = *service.AttentionHook
			}
			for _, entry := range configs {
				cfg := entry.Config
				if err := cfg.Validate(); err != nil {
					return err
				}
				id := strings.ToLower(cfg.Repo)
				if t := s.Towns[id]; t != nil {
					if !entry.BranchStated {
						// An omitted branch keeps the town's own setting, which
						// may be empty: such a town follows the repository default.
						cfg.Branch = t.Config.Branch
					}
					if t.Initialized && cfg.Branch != "" && cfg.Branch != t.Branch() {
						return fmt.Errorf("cannot change precinct %s from branch %s to %s in place; use a separate state directory, or set branch to \"\" to follow the repository default", id, t.Branch(), cfg.Branch)
					}
					if t.Deleted {
						// A town listed in the config file is live: a deleted one is
						// restored under the listed settings, as adding it would.
						if err := t.Restore(cfg); err != nil {
							return err
						}
						s.Event(t.ID, "town", "operator", "repo", "", "Precinct restored from the config file; recovery records retained", time.Now())
						restored = append(restored, t.ID)
						continue
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
		for _, id := range restored {
			fmt.Fprintf(os.Stderr, "scs: restored deleted precinct %s with the config file's settings\n", id)
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	gh := town.GitHubClient{}
	workers := &town.BotWorkers{Root: dir, Store: store, GitHub: gh}
	supervisor := town.NewSupervisor(store, gh, workers)
	supervisor.Mjolnir = mjolnir.New(dir, demo, mjolnir.Environment())
	workers.Mjolnir = supervisor.Mjolnir
	if command := mjolnir.Setting("SLOPCOP_SQUAD_MJOLNIR_COMMAND"); command != "" {
		if json.Unmarshal([]byte(command), &workers.MjolnirCommand) != nil || len(workers.MjolnirCommand) == 0 || workers.MjolnirCommand[0] == "" {
			return errors.New("SLOPCOP_SQUAD_MJOLNIR_COMMAND must be a JSON command array for mj")
		}
	}
	token, err := serviceToken(dir)
	if err != nil {
		return err
	}
	conn := connection{URL: "http://" + listener.Addr().String(), Token: token, PID: os.Getpid(), Version: buildVersion()}
	data, _ := json.Marshal(conn)
	if err = os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600); err != nil {
		return err
	}
	if err = os.Chmod(filepath.Join(dir, "connection.json"), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	server := &web.Server{Store: store, Supervisor: supervisor, Token: conn.Token, Origin: conn.URL, Version: buildVersion(), TaskGitHub: gh, Shutdown: func() { cancel(context.Canceled) }}
	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, BaseContext: func(net.Listener) context.Context { return ctx }}
	results := make(chan error, 2)
	remaining := 2
	go func() { results <- httpServer.Serve(listener) }()
	if demo {
		go func() { results <- town.Demo(ctx, store) }()
	} else {
		go func() { results <- supervisor.Run(ctx) }()
	}
	fmt.Print(serveBanner(conn, demo))
	if demo {
		fmt.Println("DEMO: simulated events only; no GitHub or agent processes.")
	}
	signalReady()
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-results:
		remaining--
	}
	cancel(err)
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

// townEntry is one town from a config file together with whether that file
// stated a branch at all. An omitted branch keeps the town's current setting; an
// explicit empty branch clears it, so the town follows the repository default
// again. Towns saved by an older Town have the branch observed at their first
// inventory pinned in configuration, and this is how an operator releases it.
type townEntry struct {
	town.Config
	BranchStated bool
}

// decodeTowns decodes the towns array strictly, then records which entries
// stated a branch. Unknown fields remain rejected, inside towns as well.
func decodeTowns(raw []byte) ([]townEntry, error) {
	var configs []town.Config
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&configs); err != nil {
		return nil, err
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("towns must be one array")
	}
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != len(configs) {
		return nil, errors.New("could not read the towns array")
	}
	entries := make([]townEntry, len(configs))
	for i, cfg := range configs {
		_, stated := fields[i]["branch"]
		entries[i] = townEntry{Config: cfg, BranchStated: stated}
	}
	return entries, nil
}

// fileService is what the object form says about the whole service. A nil
// field was absent, and leaves the saved setting as it was.
type fileService struct {
	AttentionHook *town.AttentionHook
	MaxWorkers    *int
	// QuietHours replaces the service default quiet hours; an empty list
	// removes them.
	QuietHours *[]town.QuietWindow
}

func decodeConfigFile(data []byte) ([]townEntry, fileService, error) {
	var none fileService
	var file struct {
		AttentionHook json.RawMessage `json:"attention_hook"`
		MaxWorkers    json.RawMessage `json:"max_workers"`
		QuietHours    json.RawMessage `json:"quiet_hours"`
		Towns         json.RawMessage `json:"towns"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&file); err != nil {
		return nil, none, err
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, none, errors.New("expected one config object")
	}
	if len(file.Towns) == 0 || string(file.Towns) == "null" {
		return nil, none, errors.New("config object requires a towns array")
	}
	towns, err := decodeTowns(file.Towns)
	if err != nil {
		return nil, none, err
	}
	var service fileService
	if len(file.AttentionHook) > 0 {
		var hook *town.AttentionHook
		d := json.NewDecoder(bytes.NewReader(file.AttentionHook))
		d.DisallowUnknownFields()
		if d.Decode(&hook) != nil || hook == nil {
			return nil, none, errors.New("attention_hook must be an object with enabled and command")
		}
		if err := hook.Validate(); err != nil {
			return nil, none, err
		}
		service.AttentionHook = hook
	}
	if len(file.MaxWorkers) > 0 {
		if string(file.MaxWorkers) == "null" {
			return nil, none, errors.New("max_workers cannot be null")
		}
		var value int
		if err := json.Unmarshal(file.MaxWorkers, &value); err != nil {
			return nil, none, errors.New("max_workers must be an integer")
		}
		if err := (town.ServiceConfig{MaxWorkers: value}).Validate(); err != nil {
			return nil, none, err
		}
		service.MaxWorkers = &value
	}
	if len(file.QuietHours) > 0 {
		if string(file.QuietHours) == "null" {
			return nil, none, errors.New("quiet_hours cannot be null; use [] for no service default")
		}
		windows := []town.QuietWindow{}
		qd := json.NewDecoder(bytes.NewReader(file.QuietHours))
		qd.DisallowUnknownFields()
		if err := qd.Decode(&windows); err != nil {
			return nil, none, fmt.Errorf("quiet_hours must be a list of {days, start, end} windows: %w", err)
		}
		if err := town.ValidateQuietHours(windows); err != nil {
			return nil, none, err
		}
		service.QuietHours = &windows
	}
	return towns, service, nil
}

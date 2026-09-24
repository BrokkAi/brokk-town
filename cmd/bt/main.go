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
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BrokkAi/brokk-town/internal/harness"
	"github.com/BrokkAi/brokk-town/internal/town"
	"github.com/BrokkAi/brokk-town/internal/web"
)

var version = "dev"

// connection advertises the running local service and its identity.
type connection struct {
	URL        string    `json:"url"`
	Token      string    `json:"token"`
	PID        int       `json:"pid"`
	Version    string    `json:"version,omitempty"`
	Executable string    `json:"executable,omitempty"`
	Started    time.Time `json:"started,omitempty"`
}

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

// shutdownSignals end the process through the ordinary shutdown sequence.
// SIGHUP belongs here: closing a terminal or dropping an SSH connection would
// otherwise kill the service outright, leaving its workers running in their own
// process groups and a stale connection.json advertising a dead URL.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer cancel()
	err := run(ctx, os.Args[1:])
	cancel()
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "bt:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	command := "serve"
	explicit := false
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
		explicit = true
	}
	if command == "service" {
		return runService(ctx, args)
	}
	fs := flag.NewFlagSet("bt "+command, flag.ContinueOnError)
	fl := addCLIFlags(fs)
	dir, listen, demo := fl.dir, fl.listen, fl.demo
	repo, role, task := fl.repo, fl.role, fl.task
	config := fl.config
	agentHarness, harnessVersion := fl.harness, fl.harnessVersion
	refreshHarnesses := fl.refresh
	model, effort, agentCommand := fl.model, fl.effort, fl.agentCommand
	inherit := fl.inherit
	kind, title, bodyFile, requestID := fl.kind, fl.title, fl.bodyFile, fl.requestID
	maxWorkers := fl.maxWorkers
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
		if args[0] == "service" {
			sfs := flag.NewFlagSet("bt service", flag.ContinueOnError)
			addServiceFlags(sfs)
			if len(args) > 1 && findServiceCommand(args[1]) != nil {
				printServiceVerbHelp(os.Stdout, sfs, args[1])
				return nil
			}
			printServiceHelp(os.Stdout, sfs)
			return nil
		}
		if findCommand(args[0]) != nil {
			printCommandHelp(os.Stdout, fs, args[0])
			return nil
		}
		return fmt.Errorf("unknown command %q for \"bt\"\nRun 'bt --help' for usage", args[0])
	}
	if findCommand(command) == nil {
		return fmt.Errorf("unknown command %q for \"bt\"\nRun 'bt --help' for usage", command)
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
		return fmt.Errorf("unexpected arguments: %s\nRun 'bt %s --help' for usage", strings.Join(fs.Args(), " "), command)
	}
	base, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	abs := runtimeDir(base, *demo)
	if *fl.daemon && command != "serve" {
		return errors.New("-d is only valid when starting Town")
	}
	if command == "serve" {
		if *fl.daemon {
			return startBackground(ctx, base, *demo, *listen, *config, *repo)
		}
		return serve(ctx, abs, *listen, *demo, *config, *repo)
	}
	if command == "capacity" {
		if *maxWorkers < 1 || *maxWorkers > town.MaximumMaxWorkers {
			return fmt.Errorf("--max-workers is required and must be between 1 and %d", town.MaximumMaxWorkers)
		}
		conn, err := ensureService(ctx, base, *demo)
		if err != nil {
			return err
		}
		var capacity town.Capacity
		if err := request(ctx, conn, "POST", "/api/capacity", map[string]int{"max_workers": *maxWorkers}, &capacity); err != nil {
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
	conn, err := ensureService(ctx, base, *demo)
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
		quietSet := false
		fs.Visit(func(f *flag.Flag) { quietSet = quietSet || f.Name == "quiet-hours" })
		if *repo == "" {
			if !quietSet {
				return errors.New("--repo OWNER/REPO is required")
			}
			return setServiceQuietHours(ctx, conn, fs, *fl.quietHours)
		}
		var result any
		settingsRole := ""
		if roleSet {
			if !town.ValidRole(town.Role(*role)) || *role == "repo" {
				return errors.New("settings --role must be bug, feature, issue, review, or release; omit --role to edit town defaults")
			}
			settingsRole = *role
		}
		payload := map[string]any{"town": strings.ToLower(*repo), "role": settingsRole, "agent": agent}
		if *fl.closeSeverity != "" {
			if settingsRole != "" {
				return errors.New("--review-close-severity is a town setting; omit --role")
			}
			payload["review_close_severity"] = strings.ToUpper(*fl.closeSeverity)
		}
		if *fl.mergePolicy != "" {
			if settingsRole != "" {
				return errors.New("--merge-policy is a town setting; omit --role")
			}
			payload["merge_policy"] = strings.ToLower(*fl.mergePolicy)
		}
		budgetEdited := *fl.budgetPeriod != "" || *fl.budgetAttempts != 0 || *fl.budgetMinutes != 0
		if budgetEdited {
			if settingsRole != "" {
				return errors.New("a budget is a town setting; omit --role")
			}
			period := strings.ToLower(*fl.budgetPeriod)
			if period == "none" {
				if *fl.budgetAttempts != 0 || *fl.budgetMinutes != 0 {
					return errors.New("--budget-period none removes the budget; drop the limit flags")
				}
				payload["budget"] = map[string]any{"budget": nil}
			} else {
				if period == "" {
					return errors.New("--budget-period day|week|month is required with a budget limit")
				}
				if *fl.budgetAttempts == 0 && *fl.budgetMinutes == 0 {
					return errors.New("set --budget-attempts or --budget-agent-minutes. " + town.BudgetLimitAdvice)
				}
				payload["budget"] = map[string]any{"budget": map[string]any{"period": period, "max_attempts": *fl.budgetAttempts, "max_agent_minutes": *fl.budgetMinutes}}
			}
		}
		if quietSet {
			if settingsRole != "" {
				return errors.New("quiet hours are a town setting; omit --role")
			}
			edit, err := quietHoursEdit(*fl.quietHours, true)
			if err != nil {
				return err
			}
			payload["quiet_hours"] = edit
		}
		policy, edited, err := workPolicyEdit(fl, fs)
		if err != nil {
			return err
		}
		if edited {
			if settingsRole == "" {
				return errors.New("a work policy belongs to one bot; add --role BOT")
			}
			payload["work_policy"] = map[string]any{"policy": policy}
		}
		return request(ctx, conn, "POST", "/api/settings", payload, &result)
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
	case "defer", "undefer":
		if *repo == "" || *task == "" {
			return errors.New("--repo OWNER/REPO and --task ID are required")
		}
		payload := map[string]string{"town": strings.ToLower(*repo), "role": "all", "action": command, "task": *task}
		if command == "defer" {
			until, err := deferUntil(*fl.until, time.Now())
			if err != nil {
				return err
			}
			payload["until"], payload["reason"] = until.Format(time.RFC3339), *fl.reason
		} else if *fl.until != "" || *fl.reason != "" {
			return errors.New("--until and --reason apply only to defer")
		}
		var result any
		if err := request(ctx, conn, "POST", "/api/control", payload, &result); err != nil {
			return err
		}
		if command == "defer" {
			fmt.Printf("Snoozed %s until %s\n", *task, payload["until"])
		} else {
			fmt.Printf("Cleared the snooze on %s\n", *task)
		}
		return nil
	case "start", "pause", "stop", "retry", "delete", "admit", "decline":
		if *repo == "" {
			return errors.New("--repo OWNER/REPO is required")
		}
		decision := command == "admit" || command == "decline"
		if decision && *task == "" {
			return errors.New("--task is required for a Mayoral decision")
		}
		if decision {
			*role = "hall"
		}
		var result any
		return request(ctx, conn, "POST", "/api/control", map[string]string{"town": strings.ToLower(*repo), "role": *role, "action": command, "task": *task}, &result)
	default:
		return fmt.Errorf("unknown command %q for \"bt\"\nRun 'bt --help' for usage", command)
	}
}

// quietHoursEdit turns --quiet-hours into the settings edit: a schedule, none
// for no quiet hours, or, for a town, default to follow the service default.
func quietHoursEdit(value string, forTown bool) (map[string]any, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return map[string]any{"windows": []town.QuietWindow{}}, nil
	case "default":
		if !forTown {
			return nil, errors.New("--quiet-hours default applies to a town; the service default is a schedule or none")
		}
		return map[string]any{"windows": nil}, nil
	case "":
		return nil, errors.New("--quiet-hours needs windows such as \"mon-fri 18:00-08:00\", none, or default")
	}
	windows, err := town.ParseQuietHours(value)
	if err != nil {
		return nil, fmt.Errorf("--quiet-hours: %w", err)
	}
	return map[string]any{"windows": windows}, nil
}

// setServiceQuietHours edits the service default quiet hours. It is the only
// setting bt settings takes without --repo.
func setServiceQuietHours(ctx context.Context, conn connection, fs *flag.FlagSet, value string) error {
	var other []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "quiet-hours" && !isGlobalFlag(f.Name) {
			other = append(other, "--"+f.Name)
		}
	})
	if len(other) > 0 {
		return fmt.Errorf("without --repo, settings edits only the service default --quiet-hours; %s needs --repo", strings.Join(other, ", "))
	}
	edit, err := quietHoursEdit(value, false)
	if err != nil {
		return err
	}
	var saved town.ServiceConfig
	if err := request(ctx, conn, "POST", "/api/quiet-hours", edit, &saved); err != nil {
		return err
	}
	if len(saved.QuietHours) == 0 {
		fmt.Println("Service default quiet hours: none")
	} else {
		fmt.Println("Service default quiet hours:", town.FormatQuietHours(saved.QuietHours))
	}
	return nil
}

// deferUntil reads a resume time as an RFC 3339 timestamp or as a delay from
// now. A delay accepts Go durations (90m, 4h30m) and whole days (2d). Town
// checks the result again: it must be in the future and within its limit.
func deferUntil(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("--until is required: an RFC 3339 time such as 2026-01-02T15:04:05Z, or a delay such as 4h or 2d")
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at.UTC(), nil
	}
	if days, ok := strings.CutSuffix(value, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return now.Add(time.Duration(n) * 24 * time.Hour).UTC().Truncate(time.Second), nil
		}
	}
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return now.Add(d).UTC().Truncate(time.Second), nil
	}
	return time.Time{}, fmt.Errorf("--until %q is neither an RFC 3339 time such as 2026-01-02T15:04:05Z nor a positive delay such as 90m, 4h or 2d", value)
}

// browserLink returns the browser address with its access key only when
// stdout is an interactive terminal. Redirected output, such as the background
// service's log, gets a pointer to bt web so the key never lands in a file.
func browserLink(conn connection, demo bool) string {
	if stdoutIsTerminal() {
		return conn.URL + "/#token=" + conn.Token
	}
	if demo {
		return "run bt web --demo for the link"
	}
	return "run bt web for the link"
}

var stdoutIsTerminal = func() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// serveBanner is what the foreground service prints once it is listening.
func serveBanner(conn connection, demo bool) string {
	return fmt.Sprintf("Brokk Town %s\nBrowser: %s\n", buildVersion(), browserLink(conn, demo))
}

// backgroundBanner is what bt -d prints once the detached service is ready.
func backgroundBanner(conn connection, demo bool, logs string) string {
	return fmt.Sprintf("Town running (pid %d)\nBrowser: %s\nLogs: %s\n", conn.PID, browserLink(conn, demo), logs)
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
	if notice := store.Notice(); notice != "" {
		fmt.Fprintf(os.Stderr, "bt: %s\n", notice)
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
						return fmt.Errorf("cannot change town %s from branch %s to %s in place; use a separate state directory, or set branch to \"\" to follow the repository default", id, t.Branch(), cfg.Branch)
					}
					if t.Deleted {
						// A town listed in the config file is live: a deleted one is
						// restored under the listed settings, as adding it would.
						if err := t.Restore(cfg); err != nil {
							return err
						}
						s.Event(t.ID, "town", "operator", "repo", "", "Town restored from the config file; recovery records retained", time.Now())
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
			fmt.Fprintf(os.Stderr, "bt: restored deleted town %s with the config file's settings\n", id)
		}
	}
	if repo != "" {
		if demo {
			return errors.New("real repository is not accepted in demo mode")
		}
		// --repo adds a town that is absent; a deleted one is restored with
		// the settings it kept, since --repo supplies none of its own.
		if t := store.Snapshot().Towns[strings.ToLower(repo)]; t == nil || t.Deleted {
			if err = store.Update(func(s *town.State) error {
				existing := s.Towns[strings.ToLower(repo)]
				if existing == nil {
					_, err := s.Add(town.DefaultConfig(repo))
					return err
				}
				if err := existing.Restore(existing.Config); err != nil {
					return err
				}
				s.Event(existing.ID, "town", "operator", "repo", "", "Town restored by serve --repo; recovery records retained", time.Now())
				return nil
			}); err != nil {
				return err
			}
			if t != nil {
				fmt.Fprintf(os.Stderr, "bt: restored deleted town %s with its previous settings\n", t.ID)
			}
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
	defer workers.Close()
	if !demo {
		if err := workers.SyncProcesses(ctx, store.Snapshot()); err != nil {
			return err
		}
	}
	token, err := serviceToken(dir)
	if err != nil {
		return err
	}
	exe, _ := executablePath()
	conn := connection{URL: "http://" + listener.Addr().String(), Token: token, PID: os.Getpid(), Version: buildVersion(), Executable: exe, Started: time.Now()}
	data, _ := json.Marshal(conn)
	if err = os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600); err != nil {
		return err
	}
	if err = os.Chmod(filepath.Join(dir, "connection.json"), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &web.Server{Store: store, Supervisor: supervisor, Token: conn.Token, Origin: conn.URL, Version: buildVersion(), TaskGitHub: gh}
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
		return nil, errors.New("expected one config array")
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
	MaxWorkers *int
	// QuietHours replaces the service default quiet hours; an empty list
	// removes them.
	QuietHours *[]town.QuietWindow
}

func decodeConfigFile(data []byte) ([]townEntry, fileService, error) {
	var none fileService
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		entries, err := decodeTowns([]byte(trimmed))
		return entries, none, err
	}
	var file struct {
		MaxWorkers json.RawMessage `json:"max_workers"`
		QuietHours json.RawMessage `json:"quiet_hours"`
		Towns      json.RawMessage `json:"towns"`
	}
	d := json.NewDecoder(strings.NewReader(trimmed))
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

// policyFlagNames are the settings flags that build a bot's work policy. They
// are listed once so flag presence, not a zero value, decides what was edited:
// --limit 0 is meaningless, but --labels "" has to be able to clear a filter.
var policyFlagNames = map[string]bool{
	"labels": true, "exclude-labels": true, "only": true, "focus": true,
	"limit": true, "attempts": true, "verify": true, "clear-policy": true,
	"release-daily-seconds": true, "release-minimum-gap-seconds": true,
	"release-quiet-seconds": true, "release-burst": true,
	"release-burst-window-seconds": true, "release-triage": true,
	"release-preflight": true, "release-verification-timeout-seconds": true,
}

// splitLabels turns a comma-separated flag into the list the API takes. An
// empty value clears the filter rather than sending one empty label.
func splitLabels(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func jsonCommand(flagName, value string) ([]string, error) {
	var args []string
	if err := json.Unmarshal([]byte(value), &args); err != nil {
		return nil, fmt.Errorf("--%s must be a JSON argument array: %w", flagName, err)
	}
	return args, nil
}

// workPolicyEdit builds the work policy this invocation asks for, reporting
// whether any policy flag was given at all. A nil policy with edited true is
// the request to remove the saved one.
func workPolicyEdit(fl *cliFlags, fs *flag.FlagSet) (map[string]any, bool, error) {
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		if policyFlagNames[f.Name] {
			given[f.Name] = true
		}
	})
	if len(given) == 0 {
		return nil, false, nil
	}
	if given["clear-policy"] && *fl.clearPolicy {
		// Clearing is all or nothing: pairing it with a filter would leave the
		// operator guessing which one won.
		if len(given) > 1 {
			return nil, true, errors.New("--clear-policy removes the whole policy; run it on its own")
		}
		return nil, true, nil
	}
	policy := map[string]any{}
	release := map[string]any{}
	var err error
	fs.Visit(func(f *flag.Flag) {
		if err != nil {
			return
		}
		switch f.Name {
		case "labels":
			policy["labels"] = splitLabels(*fl.labels)
		case "exclude-labels":
			policy["exclude_labels"] = splitLabels(*fl.excludeLabels)
		case "only":
			policy["only"] = *fl.only
		case "focus":
			policy["focus"] = *fl.focus
		case "limit":
			policy["limit"] = *fl.limit
		case "attempts":
			policy["attempts"] = *fl.attempts
		case "verify":
			var command []string
			if command, err = jsonCommand("verify", *fl.verify); err == nil {
				policy["verify"] = command
			}
		case "release-daily-seconds":
			release["daily_seconds"] = *fl.releaseDaily
		case "release-minimum-gap-seconds":
			release["minimum_gap_seconds"] = *fl.releaseGap
		case "release-quiet-seconds":
			release["quiet_seconds"] = *fl.releaseQuiet
		case "release-burst":
			release["burst"] = *fl.releaseBurst
		case "release-burst-window-seconds":
			var seconds int
			if seconds, err = strconv.Atoi(strings.TrimSpace(*fl.releaseWindow)); err != nil {
				err = errors.New("--release-burst-window-seconds must be a whole number of seconds")
				return
			}
			release["burst_window_seconds"] = seconds
		case "release-triage":
			switch strings.ToLower(strings.TrimSpace(*fl.releaseTriage)) {
			case "on", "true", "yes":
				release["triage"] = true
			case "off", "false", "no":
				release["triage"] = false
			default:
				err = errors.New("--release-triage must be on or off")
			}
		case "release-preflight":
			var command []string
			if command, err = jsonCommand("release-preflight", *fl.releasePreflight); err == nil {
				release["preflight"] = command
			}
		case "release-verification-timeout-seconds":
			release["verification_timeout_seconds"] = *fl.releaseVerifyTimeout
		}
	})
	if err != nil {
		return nil, true, err
	}
	if len(release) > 0 {
		policy["release"] = release
	}
	return policy, true, nil
}

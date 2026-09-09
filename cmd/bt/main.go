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
	"syscall"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
	"github.com/BrokkAi/brokk-town/internal/web"
)

var version = "dev"

type connection struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func stateHome() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "brokk-town")
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
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
		v := version
		if v == "dev" {
			if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
				v = info.Main.Version
			}
		}
		fmt.Println(v)
		return nil
	}
	fs := flag.NewFlagSet("bt "+command, flag.ContinueOnError)
	dir := fs.String("state-dir", stateHome(), "private state directory")
	listen := fs.String("listen", "127.0.0.1:8099", "loopback HTTP address (serve only)")
	demo := fs.Bool("demo", false, "isolated simulated town (serve only)")
	repo := fs.String("repo", "", "GitHub OWNER/REPO")
	role := fs.String("role", "all", "bug, issue, review, release, repo, or all")
	task := fs.String("task", "", "task ID for retry")
	config := fs.String("config", "", "optional JSON array of town configs (serve only)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Brokk Town — one local service, a browser town, and a terminal control panel.\n\nUsage: bt [tui|serve|web|status|add|start|pause|stop|retry|version] [options]\n\nRun bt serve --demo for a simulated town. Run bt serve for real repositories.\nClosing the TUI or browser leaves the service running. Stop serve with Ctrl+C.\n")
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
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if *demo {
		abs = filepath.Join(abs, "demo")
	}
	if command == "serve" {
		return serve(ctx, abs, *listen, *demo, *config, *repo)
	}
	conn, err := readConnection(abs)
	if err != nil {
		return fmt.Errorf("town service is not available; run bt serve (or bt serve --demo): %w", err)
	}
	switch command {
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
		return request(ctx, conn, "POST", "/api/towns", map[string]string{"repo": *repo}, &result)
	case "start", "pause", "stop", "retry":
		if *repo == "" {
			return errors.New("--repo OWNER/REPO is required")
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
	client := &http.Client{Timeout: 10 * time.Second}
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
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("bind to a loopback IP; use an SSH tunnel for remote access")
	}
	store, err := town.Open(dir, demo)
	if err != nil {
		return err
	}
	defer store.Close()
	if configFile != "" {
		data, e := os.ReadFile(configFile)
		if e != nil {
			return e
		}
		var configs []town.Config
		d := json.NewDecoder(strings.NewReader(string(data)))
		d.DisallowUnknownFields()
		if e = d.Decode(&configs); e != nil {
			return e
		}
		if d.Decode(new(any)) != io.EOF {
			return errors.New("expected one config array")
		}
		if demo {
			return errors.New("real config is not accepted in demo mode")
		}
		if e = store.Update(func(s *town.State) error {
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
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	conn := connection{URL: "http://" + listener.Addr().String(), Token: hex.EncodeToString(key), PID: os.Getpid()}
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
	server := &web.Server{Store: store, Supervisor: supervisor, Token: conn.Token, Origin: conn.URL}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, BaseContext: func(net.Listener) context.Context { return ctx }}
	results := make(chan error, 2)
	remaining := 2
	go func() { results <- httpServer.Serve(listener) }()
	if demo {
		go func() { results <- town.Demo(ctx, store) }()
	} else {
		go func() { results <- supervisor.Run(ctx) }()
	}
	fmt.Printf("Brokk Town %s\nBrowser: %s/#token=%s\nTerminal: bt tui --state-dir %s\n", version, conn.URL, conn.Token, dir)
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

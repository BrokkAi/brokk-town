package botserver

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Ready is the one line a bot prints on standard output once it is listening,
// so a parent that asked for port 0 learns the port it got.
type Ready struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Version string `json:"version"`
}

// Environment variables a bot binary reads. A flag of the same meaning
// outranks its variable.
const (
	EnvListen    = "BROKK_BOT_LISTEN"
	EnvRoot      = "BROKK_BOT_ROOT"
	EnvToken     = "BROKK_BOT_TOKEN"
	EnvTokenFile = "BROKK_BOT_TOKEN_FILE"
	EnvTLSCert   = "BROKK_BOT_TLS_CERT"
	EnvTLSKey    = "BROKK_BOT_TLS_KEY"
	EnvLogLevel  = "BROKK_BOT_LOG_LEVEL"
)

// Main runs one bot binary: worker builds the bot for its data root. It never
// returns.
func Main(version string, worker func(root string) botapi.Worker) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := Run(ctx, os.Args[1:], version, worker, os.Stdout, os.Stderr)
	cancel()
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, filepath.Base(os.Args[0])+":", err)
		os.Exit(1)
	}
}

// Run is Main with its process environment passed in.
func Run(ctx context.Context, args []string, version string, worker func(root string) botapi.Worker, stdout, stderr io.Writer) error {
	probe := worker("")
	fs := flag.NewFlagSet(probe.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", envOr(EnvListen, ":8080"), "address to serve the bot contract on ("+EnvListen+")")
	root := fs.String("root", os.Getenv(EnvRoot), "data root for the bot's workspaces and harnesses ("+EnvRoot+")")
	tokenFile := fs.String("token-file", os.Getenv(EnvTokenFile), "file holding the bearer token the service must present ("+EnvTokenFile+"; or the token itself in "+EnvToken+")")
	certFile := fs.String("tls-cert", os.Getenv(EnvTLSCert), "serve TLS with this certificate ("+EnvTLSCert+")")
	keyFile := fs.String("tls-key", os.Getenv(EnvTLSKey), "the certificate's private key ("+EnvTLSKey+")")
	level := fs.String("log-level", envOr(EnvLogLevel, "info"), "least severe record to send: debug, info, warn or error ("+EnvLogLevel+")")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	version = botapi.BuildVersion(version)
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(*level)); err != nil {
		return fmt.Errorf("invalid log level %q", *level)
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: logLevel})).With("bot", probe.Name)
	if *root == "" {
		return errors.New("a data root is required: pass --root or set " + EnvRoot)
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absRoot, 0o700); err != nil {
		return fmt.Errorf("create the data root: %w", err)
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		return err
	}
	if (*certFile == "") != (*keyFile == "") {
		return errors.New("TLS needs both a certificate and a key")
	}
	w := worker(absRoot)
	w.LogLevel = logLevel
	server := &Server{
		Bot:      w,
		Identity: botapi.Identity{Name: w.Name, Role: w.Role, Version: version, Contract: botapi.Version},
		Token:    token,
		Log:      log,
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	go func() {
		if *certFile != "" {
			served <- httpServer.ServeTLS(listener, *certFile, *keyFile)
			return
		}
		served <- httpServer.Serve(listener)
	}()
	ready, _ := json.Marshal(Ready{Address: listener.Addr().String(), Name: w.Name, Role: w.Role, Version: version})
	fmt.Fprintln(stdout, string(ready))
	log.Info("serving the bot contract", "address", listener.Addr().String(), "version", version, "tls", *certFile != "")
	select {
	case err = <-served:
		return err
	case <-ctx.Done():
	}
	// Runs see the canceled context, finish with what they completed and
	// send their terminal frame; then the connections close.
	stop, cancelStop := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelStop()
	if err := httpServer.Shutdown(stop); err != nil {
		_ = httpServer.Close()
	}
	log.Info("stopped")
	return nil
}

// readToken returns the bearer token from a file or the environment. A bot
// never serves without one.
func readToken(file string) (string, error) {
	token := os.Getenv(EnvToken)
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read the token file: %w", err)
		}
		token = string(data)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("a bearer token is required: set " + EnvToken + " or pass --token-file")
	}
	if len(token) < 16 {
		return "", errors.New("the bearer token must be at least 16 characters")
	}
	return token, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

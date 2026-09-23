package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	bot "github.com/BrokkAi/repo-bot"
)

// version is replaced with the release tag when building published binaries.
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, os.Args[1:], nil); err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		os.Exit(1)
	}
}
func execute(ctx context.Context, args []string, log *slog.Logger) error {
	return executeWith(ctx, args, log, bot.Watch, os.Stdout)
}

type runFunc func(context.Context, bot.Config, *slog.Logger, bool) error

func executeWith(ctx context.Context, args []string, log *slog.Logger, run runFunc, stdout io.Writer) (result error) {
	ownOutput := log == nil
	if ownOutput {
		log = slog.New(newConsole(os.Stderr))
		defer func() {
			if result != nil && ctx.Err() == nil && !errors.Is(result, context.Canceled) {
				log.Error("Stopped", "error", result)
			}
		}()
	}
	mode := "run"
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-v":
			return versionCommand(args[1:], stdout)
		case "worker":
			return workerCommand(ctx, args[1:], buildVersion())
		case "run", "once", "status":
			mode = args[0]
			args = args[1:]
		}
	}
	fs := flag.NewFlagSet("brp", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: brp [run|once|status|worker|version] [repository path or URL] [options]\n\nObserve a repository and repair its branch when its checks fail. No config file is required.")
		fs.PrintDefaults()
	}
	file := fs.String("config", "", "optional JSON configuration")
	branch := fs.String("branch", "", "base branch (default: repository default)")
	agent := fs.String("agent", "", "ACP executable (default: codex-acp or npx; empty observes without repairing)")
	model := fs.String("model", "", "agent model ID")
	effort := fs.String("effort", "", "reasoning effort")
	maxRepairs := fs.Int("max-repairs", 3, "maximum repair attempts per failing revision (1-10)")
	dryRun := fs.Bool("dry-run", false, "commit repairs locally without publishing them")
	once := fs.Bool("once", mode == "once", "observe once, then exit")
	jsonOutput := fs.Bool("json", false, "structured logs")
	plain := fs.Bool("plain", false, "scrolling console output (the default)")
	poll := fs.Duration("poll", 0, "poll interval, e.g. 15m")
	timeout := fs.Duration("timeout", 0, "budget for each repair, e.g. 1h")
	var agentArgs []string
	fs.Func("agent-arg", "argument to agent; repeat as needed", func(s string) error { agentArgs = append(agentArgs, s); return nil })
	if err := parseInterspersed(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if ownOutput && *jsonOutput {
		log = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	if *plain && *jsonOutput {
		return errors.New("--plain and --json cannot be used together")
	}
	if fs.NArg() > 1 {
		return errors.New("pass one repository path or URL")
	}
	var cfg bot.Config
	var err error
	if *file != "" {
		if fs.NArg() > 0 {
			return errors.New("use a repository argument or --config")
		}
		cfg, err = bot.ReadConfig(*file)
	} else {
		cfg, err = bot.Discover(ctx, fs.Arg(0), *branch)
	}
	if err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "branch":
			cfg.Branch = *branch
		case "agent":
			cfg.Agent.Command = []string{*agent}
		case "model":
			cfg.Agent.Model = *model
			if strings.TrimSpace(*model) == "" {
				err = errors.New("model cannot be empty")
			}
		case "effort":
			cfg.Agent.Effort = *effort
			if strings.TrimSpace(*effort) == "" {
				err = errors.New("effort cannot be empty")
			}
		case "max-repairs":
			cfg.MaxRepairs = *maxRepairs
		case "dry-run":
			cfg.DryRun = *dryRun
		case "poll":
			cfg.Poll = bot.Duration(*poll)
		case "timeout":
			cfg.Timeout = bot.Duration(*timeout)
		}
	})
	if err != nil {
		return err
	}
	if !cfg.InventoryOnly() {
		cfg.Agent.Command = append(cfg.Agent.Command, agentArgs...)
	} else if len(agentArgs) > 0 {
		return errors.New("--agent-arg needs an agent")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.GitHubRepo() == "" {
		return errors.New("GitHub remote required; set github.repo for a local mirror")
	}
	if mode == "status" {
		s, err := bot.ReadState(cfg)
		if err != nil {
			return err
		}
		return writeJSON(stdout, s)
	}
	if !cfg.InventoryOnly() {
		if err := bot.ResolveAgent(&cfg, *file == "" && *agent == ""); err != nil {
			return err
		}
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("install GitHub CLI and run gh auth login")
	}
	bot.Version = buildVersion()
	log.Info("Watching repository", "repository", cfg.GitHubRepo(), "branch", cfg.Branch, "checkout", cfg.Directory, "state", cfg.StateDirectory)
	return run(ctx, cfg, log, *once)
}

func writeJSON(output io.Writer, value any) error {
	e := json.NewEncoder(output)
	e.SetIndent("", "  ")
	return e.Encode(value)
}

func versionCommand(args []string, output io.Writer) error {
	if len(args) != 0 {
		return errors.New("version does not accept arguments")
	}
	_, err := fmt.Fprintln(output, buildVersion())
	return err
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// Accept the repository before or after flags, as users expect from CLI tools.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var options, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		options = append(options, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
		if ok && boolean.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			options = append(options, args[i])
		}
	}
	return fs.Parse(append(append(options, "--"), positional...))
}

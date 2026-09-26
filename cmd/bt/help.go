package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// This file gives bt a cobra-style help surface without the cobra
// dependency: Usage / Available Commands / Flags sections, per-command
// help, and a help command. Flag parsing stays on the standard library.

type cliFlags struct {
	storageAge                        *int
	storageCleanup                    *string
	hookEnable, hookDisable           *bool
	hookCommandFile                   *string
	executionTarget, executionProfile *string
	localExecution, inheritExecution  *bool

	daemon               *bool
	closeSeverity        *string
	mergePolicy          *string
	dir                  *string
	listen               *string
	demo                 *bool
	repo                 *string
	role                 *string
	task                 *string
	until                *string
	reason               *string
	config               *string
	harness              *string
	harnessVersion       *string
	refresh              *bool
	model                *string
	effort               *string
	agentCommand         *string
	inherit              *bool
	kind                 *string
	title                *string
	bodyFile             *string
	requestID            *string
	maxWorkers           *int
	check                *bool
	json                 *bool
	budgetPeriod         *string
	budgetAttempts       *int
	budgetMinutes        *int
	quietHours           *string
	labels               *string
	excludeLabels        *string
	only                 *int
	focus                *string
	limit                *int
	attempts             *int
	verify               *string
	clearPolicy          *bool
	releaseDaily         *int
	releaseGap           *int
	releaseQuiet         *int
	releaseBurst         *int
	releaseWindow        *string
	releaseTriage        *string
	releasePreflight     *string
	releaseVerifyTimeout *int
	releaseWorkflows     *string
	releaseAssets        *string
}

// addCLIFlags defines every client flag on one set. Parsing stays lenient
// (every flag parses for every command) while help display filters to the
// flags relevant to each command.
func addCLIFlags(fs *flag.FlagSet) *cliFlags {
	fl := &cliFlags{}
	fl.storageAge = fs.Int("older-than-hours", 168, "minimum completed artifact age for storage cleanup (default 7 days)")
	fl.storageCleanup = fs.String("cleanup", "", "explicit comma-separated artifact IDs from a fresh storage inventory to remove")
	fl.hookEnable = fs.Bool("enable", false, "enable the local attention hook")
	fl.hookDisable = fs.Bool("disable", false, "disable the local attention hook")
	fl.hookCommandFile = fs.String("command-file", "", "JSON argument array file for the private attention hook command; - reads stdin")
	fl.daemon = fs.Bool("d", false, "run Town in the background")
	fl.dir = fs.String("state-dir", stateHome(), "private state directory")
	fl.listen = fs.String("listen", defaultListen, "loopback HTTP address to serve on")
	fl.demo = fs.Bool("demo", false, "the isolated simulated town")
	fl.repo = fs.String("repo", "", "GitHub OWNER/REPO")
	fl.role = fs.String("role", "all", "bot to control or configure: bug, feature, issue, review, release, simplifier, hall (Mayor Bot); repo/all for controls (start all wakes every house, except release under manual merge policy); omit for town defaults in settings")
	fl.task = fs.String("task", "", "task ID for retry, defer, undefer, admit or decline; omit with retry --role release to reset the release bot's exhausted attempt budget")
	fl.until = fs.String("until", "", "resume time as RFC 3339 (2026-01-02T15:04:05Z) or a delay from now such as 90m, 4h or 2d (defer)")
	fl.reason = fs.String("reason", "", "short note on why the task is snoozed, at most 200 characters (defer)")
	fl.config = fs.String("config", "", "optional JSON array or object with max_workers, quiet_hours and towns, applied at startup")
	fl.harness = fs.String("harness", "", "ACP registry ID, anvil, muse-acp, draupnir, or custom (add/settings)")
	fl.harnessVersion = fs.String("harness-version", "", "select an exact catalog version (add/settings)")
	fl.executionTarget = fs.String("target", "", "Mjolnir target ID from bt execution")
	fl.executionProfile = fs.String("profile", "", "Mjolnir profile ID from bt execution")
	fl.localExecution = fs.Bool("local-execution", false, "select direct local execution")
	fl.inheritExecution = fs.Bool("inherit-execution", false, "remove this bot execution override (requires --role)")
	fl.refresh = fs.Bool("refresh", false, "refresh the official ACP registry (harnesses)")
	fl.model = fs.String("model", "", "ACP model ID; empty uses harness default (add/settings)")
	fl.effort = fs.String("effort", "", "ACP reasoning effort; empty uses harness default (add/settings)")
	fl.agentCommand = fs.String("agent-command", "", "custom ACP command as a JSON argument array (add/settings)")
	fl.inherit = fs.Bool("inherit", false, "restore a bot's town defaults (settings --role BOT)")
	fl.closeSeverity = fs.String("review-close-severity", "", "least severe finding (P1, P2 or P3) that closes a pull request after its second review; lower findings become follow-up issues (settings)")
	fl.mergePolicy = fs.String("merge-policy", "", "who merges eligible pull requests: bot (Town-created), all (external too), or manual; manual pauses Release Bot (settings)")
	fl.kind = fs.String("kind", "feature", "feature or bug (request)")
	fl.title = fs.String("title", "", "GitHub issue title (request)")
	fl.bodyFile = fs.String("body-file", "", "issue description file, or - for stdin (request)")
	fl.requestID = fs.String("request-id", "", "saved submission ID; reuse it to resubmit or --check (request)")
	fl.check = fs.Bool("check", false, "check the submission named by --request-id instead of submitting (request)")
	fl.json = fs.Bool("json", false, "print the full town state as JSON (status)")
	fl.maxWorkers = fs.Int("max-workers", 0, "maximum active bot workers across all towns; omit --repo (settings)")
	fl.budgetPeriod = fs.String("budget-period", "", "accounting period for this town's agent budget: day, week, month, or none to remove it (settings)")
	fl.budgetAttempts = fs.Int("budget-attempts", 0, "agent attempts allowed per period; 0 leaves attempts uncapped (settings)")
	fl.budgetMinutes = fs.Int("budget-agent-minutes", 0, "agent minutes allowed per period; 0 leaves time uncapped (settings)")
	fl.quietHours = fs.String("quiet-hours", "", "weekly windows when Town starts no new agent work or GitHub writes, as \"DAYS HH:MM-HH:MM\" joined by ';' (mon-fri 18:00-08:00; weekends 00:00-24:00); none for no quiet hours, default to follow the service default; omit --repo to set the service default (settings)")
	fl.labels = fs.String("labels", "", "comma-separated labels this bot's work must carry (settings --role BOT)")
	fl.excludeLabels = fs.String("exclude-labels", "", "comma-separated labels that exclude work from this bot (settings --role issue|review)")
	fl.only = fs.Int("only", 0, "restrict this bot to one issue (--role issue) or pull request (--role review) number (settings)")
	fl.focus = fs.String("focus", "", "what a discovery scan should concentrate on (settings --role bug|feature|review)")
	fl.limit = fs.Int("limit", 0, "most items one run may produce: issues filed, findings, proposals, repairs or bulletin items depending on the bot (settings --role BOT)")
	fl.attempts = fs.Int("attempts", 0, "tries this bot gives one item before giving up (settings --role BOT)")
	fl.verify = fs.String("verify", "", "verification command for this bot as a JSON argument array; overrides the town command (settings --role BOT)")
	fl.clearPolicy = fs.Bool("clear-policy", false, "remove this bot's work policy and take every item again (settings --role BOT)")
	fl.releaseDaily = fs.Int("release-daily-seconds", 0, "deadline after which unreleased commits are released (settings --role release)")
	fl.releaseGap = fs.Int("release-minimum-gap-seconds", 0, "shortest interval between two releases (settings --role release)")
	fl.releaseQuiet = fs.Int("release-quiet-seconds", 0, "how long the branch must be still before a release (settings --role release)")
	fl.releaseBurst = fs.Int("release-burst", 0, "commits inside the burst window that trigger an early release (settings --role release)")
	fl.releaseWindow = fs.String("release-burst-window-seconds", "", "burst window in seconds (settings --role release)")
	fl.releaseTriage = fs.String("release-triage", "", "on or off: ask the agent whether unreleased commits warrant an early release (settings --role release)")
	fl.releasePreflight = fs.String("release-preflight", "", "preflight command as a JSON argument array; a non-zero exit stops the release (settings --role release)")
	fl.releaseVerifyTimeout = fs.Int("release-verification-timeout-seconds", 0, "bound on the independent publication check (settings --role release)")
	fl.releaseWorkflows = fs.String("release-workflows", "", "comma-separated GitHub workflow names a release must see succeed (settings --role release)")
	fl.releaseAssets = fs.String("release-assets", "", "comma-separated file patterns a release must publish (settings --role release)")
	return fl
}

type commandInfo struct {
	name  string
	short string
	long  string
	args  string
	flags []string
}

// globalFlagNames are the persistent flags: they apply to every command and
// select which service a client talks to.
var globalFlagNames = []string{"demo", "state-dir"}

// runFlagNames start Town: bare bt in the foreground, or bt -d.
var runFlagNames = []string{"config", "d", "listen"}

// rootCommand is bare bt, which runs Town rather than a client command.
var rootCommand = commandInfo{flags: runFlagNames}

var cliCommands = []commandInfo{
	{name: "doctor", short: "Check a town's setup without starting work", long: "Read-only checks for commands, GitHub access, agent availability and verifier setup. Results are saved in Town and shown in the browser. Agents and verification commands are never run. Use --json for structured results.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "json"}},
	{name: "status", short: "Show whether Town is running and its towns", long: "Show whether Town is running, where, and which towns it serves. Use --json for the full town state.", args: "[flags]", flags: []string{"json"}},
	{name: "web", short: "Print the browser address for the town", long: "Print the browser address, with its access key, for the running town.", args: "[flags]", flags: nil},
	{name: "shutdown", short: "Stop Town and its bots", long: "Stop the running Town service and all its bot processes. Use it for a town started with bt -d; Ctrl+C stops one in the foreground.", args: "[flags]", flags: nil},
	{name: "add", short: "Add a town", long: "Add a town for a GitHub repository.", args: "--repo OWNER/REPO [flags]", flags: []string{"agent-command", "effort", "harness", "harness-version", "model", "repo"}},
	{name: "delete", short: "Delete a town", long: "Delete a town. GitHub state stays intact.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo"}},
	{name: "choices", short: "List a saved profile’s models and efforts", long: "Read model and effort choices for a town or bot profile. Mjolnir selections use the daemon catalog; direct local selections prepare a prompt-free local harness session. --model selects which model’s effort choices to read. This does not save settings.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role", "model", "json"}},
	{name: "storage", short: "Inspect and clean completed local artifacts", long: "Read a dry-run disk inventory by town and bot. Cleanup requires explicit artifact IDs from that inventory, all workers paused, and no unresolved writes. Changed, dirty, unknown or active artifacts are retained. Task and write identities are never removed. Demo cleanup is disabled.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "older-than-hours", "cleanup", "json"}},
	{name: "attention-hook", short: "Configure the local attention hook", long: "Show whether the service attention hook is configured and enabled. --enable/--disable changes it; --command-file FILE saves a private JSON command/argv array (- reads stdin). Disabled by default. The hook receives public identities and a reason code as JSON on stdin, runs for at most ten seconds independently of quiet hours, and never runs in demo. Command output is discarded.", args: "[flags]", flags: []string{"enable", "disable", "command-file", "json"}},
	{name: "execution", short: "List or select execution targets", long: "List cached Mjolnir launch options. --refresh queues a background refresh. Use --repo and --target/--profile to save a selection, --local-execution to run directly here, or --inherit-execution --role BOT to restore inheritance. Mjolnir-backed execution is held until remote checkout and evidence support are available.", args: "[flags]", flags: []string{"json", "refresh", "repo", "role", "target", "profile", "local-execution", "inherit-execution"}},
	{name: "harnesses", short: "List available agent harnesses", long: "List the official ACP registry. Use --refresh to update the cached catalog. Works whether or not Town is running.", args: "[flags]", flags: []string{"refresh"}},
	{name: "settings", short: "Configure Town, a town or a bot", long: "Configure service-wide settings, a town's defaults or one bot house. Omit --repo for service-wide settings (--max-workers, --quiet-hours); omit --role to edit town defaults.\n\n" +
		"A budget bounds agent attempts and agent minutes per accounting period. Town cannot cap token or dollar spend: no bundled agent harness reports usage back through the worker protocol.\n\n" +
		"Quiet hours are weekly windows on this machine's local clock in which Town starts no new agent work and makes none of its own GitHub writes; running work finishes and the repository is still watched. A window ending at or before its start runs past midnight. Without --repo, --quiet-hours sets the default every town follows unless it sets its own.\n\n" +
		"With --role, the work-policy flags choose what that bot takes on: label filters, one selected issue or pull request, a discovery focus, per-run limits, attempts, and its own verification command. Each flag is refused by a bot that cannot honour it.", args: "[--repo OWNER/REPO] [flags]", flags: []string{"agent-command", "attempts", "budget-agent-minutes", "budget-attempts", "budget-period", "clear-policy", "effort", "exclude-labels", "focus", "harness", "harness-version", "inherit", "labels", "limit", "max-workers", "merge-policy", "model", "only", "release-burst", "release-burst-window-seconds", "release-daily-seconds", "release-minimum-gap-seconds", "release-assets", "release-preflight", "release-quiet-seconds", "release-triage", "release-verification-timeout-seconds", "release-workflows", "quiet-hours", "repo", "review-close-severity", "role", "verify"}},
	{name: "request", short: "Submit or check a GitHub issue request", long: "Submit a GitHub issue as work for a town, or check a submission with --check --request-id ID.", args: "--repo OWNER/REPO (--title TITLE --body-file FILE | --check --request-id ID) [flags]", flags: []string{"body-file", "check", "kind", "repo", "request-id", "title"}},
	{name: "start", short: "Start a town or bot house", long: "Start a town or one bot house.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role"}},
	{name: "pause", short: "Pause a town or bot house", long: "Pause a town or one bot house.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role"}},
	{name: "stop", short: "Stop a town or bot house", long: "Stop a town or one bot house. Town itself keeps running; use bt shutdown to stop it.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role"}},
	{name: "retry", short: "Retry a task", long: "Retry a task. Omit --task with --role release to reset the release bot's exhausted attempt budget.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role", "task"}},
	{name: "defer", short: "Snooze a task until a chosen time", long: "Snooze one issue or pull request until a chosen time. Its house keeps working the rest of the queue; no agent starts and no merge happens for the snoozed task until the resume time, when it rejoins the queue on its own.", args: "--repo OWNER/REPO --task ID --until TIME [flags]", flags: []string{"reason", "repo", "task", "until"}},
	{name: "undefer", short: "Clear a task's snooze", long: "Clear a task's snooze so its house can take it up at once.", args: "--repo OWNER/REPO --task ID [flags]", flags: []string{"repo", "task"}},
	{name: "admit", short: "Admit a Mayoral decision", long: "Admit a pending Mayoral decision, or admit work Simplifier declined in auto mode.", args: "--repo OWNER/REPO --task ID [flags]", flags: []string{"repo", "task"}},
	{name: "decline", short: "Decline a Mayoral decision", long: "Decline a pending Mayoral decision.", args: "--repo OWNER/REPO --task ID [flags]", flags: []string{"repo", "task"}},
	{name: "version", short: "Print the version", long: "Print the bt version.", args: "", flags: nil},
}

func findCommand(name string) *commandInfo {
	for i := range cliCommands {
		if cliCommands[i].name == name {
			return &cliCommands[i]
		}
	}
	return nil
}

// wantsHelp reports a cobra-style help request anywhere in the remaining
// args: -h, -help, --help, including --help=true forms.
func wantsHelp(args []string) bool {
	for _, a := range args {
		t := strings.TrimLeft(a, "-")
		if i := strings.Index(t, "="); i >= 0 {
			t = t[:i]
		}
		if t == "h" || t == "help" {
			return true
		}
	}
	return false
}

func isGlobalFlag(name string) bool {
	for _, g := range globalFlagNames {
		if g == name {
			return true
		}
	}
	return false
}

type flagRow struct {
	left  string
	usage string
}

func flagRows(fs *flag.FlagSet, names []string) []flagRow {
	rows := make([]flagRow, 0, len(names))
	for _, name := range names {
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		typeName, usage := flag.UnquoteUsage(f)
		spec := "--" + f.Name
		if len(f.Name) == 1 {
			spec = "-" + f.Name
		}
		if typeName != "" {
			spec += " " + typeName
		}
		text := usage
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
			def := f.DefValue
			if typeName == "string" {
				def = fmt.Sprintf("%q", def)
			}
			if text != "" {
				text += " "
			}
			text += fmt.Sprintf("(default %s)", def)
		}
		left := "      " + spec
		if len(f.Name) == 1 {
			left = "  " + spec
		}
		rows = append(rows, flagRow{left: left, usage: text})
	}
	return rows
}

func writeFlagSection(out io.Writer, fs *flag.FlagSet, names []string, heading string, helpFor string) {
	writeFlagSectionWithHelp(out, fs, names, heading, helpFor, true)
}

func writeFlagSectionWithHelp(out io.Writer, fs *flag.FlagSet, names []string, heading string, helpFor string, includeHelp bool) {
	names = append([]string(nil), names...)
	sort.Strings(names)
	rows := flagRows(fs, names)
	if includeHelp {
		rows = append(rows, flagRow{left: "  -h, --help", usage: "help for " + helpFor})
	}
	width := 0
	for _, r := range rows {
		if len(r.left) > width {
			width = len(r.left)
		}
	}
	fmt.Fprintln(out, heading+":")
	for _, r := range rows {
		fmt.Fprintf(out, "%-*s  %s\n", width, r.left, r.usage)
	}
}

func writeCommands(out io.Writer, cmds []commandInfo) {
	width := 0
	for _, c := range cmds {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	for _, c := range cmds {
		fmt.Fprintf(out, "  %-*s  %s\n", width, c.name, c.short)
	}
}

// printRootHelp renders the top-level help: description, usage, commands,
// flags, and the --help pointer.
func printRootHelp(out io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(out, "Brokk Town — a local service with a browser and CLI.")
	fmt.Fprintln(out, "Run bt in the foreground, or bt -d in the background. Ctrl+C or bt shutdown stops Town and its bots.")
	fmt.Fprintln(out, "Use bt web for the browser address. Client commands need a running Town.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  bt [command] [flags]")
	fmt.Fprintln(out, "  bt [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Available Commands:")
	withHelp := append(append([]commandInfo(nil), cliCommands...), commandInfo{name: "help", short: "Show help for a command"})
	writeCommands(out, withHelp)
	fmt.Fprintln(out)
	writeFlagSection(out, fs, append(append([]string(nil), runFlagNames...), globalFlagNames...), "Flags", "bt")
	fmt.Fprintln(out)
	fmt.Fprintln(out, `Use "bt [command] --help" for more information about a command.`)
}

// printCommandHelp renders help for one command with its own flags plus the
// shared global flags.
func printCommandHelp(out io.Writer, fs *flag.FlagSet, name string) {
	c := findCommand(name)
	if c == nil {
		printRootHelp(out, fs)
		return
	}
	fmt.Fprintln(out, c.long)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	usage := "  bt " + c.name
	if c.args != "" {
		usage += " " + c.args
	}
	fmt.Fprintln(out, usage)
	if name == "version" {
		return
	}
	if len(c.flags) > 0 {
		fmt.Fprintln(out)
		writeFlagSectionWithHelp(out, fs, c.flags, "Flags", "bt "+c.name, false)
	}
	fmt.Fprintln(out)
	writeFlagSection(out, fs, globalFlagNames, "Global Flags", "bt "+c.name)
}

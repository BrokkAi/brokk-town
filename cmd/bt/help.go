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
	daemon               *bool
	closeSeverity        *string
	dir                  *string
	listen               *string
	demo                 *bool
	repo                 *string
	role                 *string
	task                 *string
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
	budgetPeriod         *string
	budgetAttempts       *int
	budgetMinutes        *int
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
	fl.daemon = fs.Bool("d", false, "run Town in the background")
	fl.dir = fs.String("state-dir", stateHome(), "private state directory")
	fl.listen = fs.String("listen", defaultListen, "loopback HTTP address for service startup")
	fl.demo = fs.Bool("demo", false, "isolated simulated town (serve only)")
	fl.repo = fs.String("repo", "", "GitHub OWNER/REPO")
	fl.role = fs.String("role", "all", "bot to control or configure: bug, feature, issue, review, release, simplifier, hall (Mayor Bot); repo/all for controls (start all wakes every house, except release under manual merge policy); omit for town defaults in settings")
	fl.task = fs.String("task", "", "task ID for retry; omit with --role release to reset the release bot's exhausted attempt budget")
	fl.config = fs.String("config", "", "optional JSON array or object with max_workers and towns (serve only)")
	fl.harness = fs.String("harness", "", "ACP registry ID, anvil, muse-acp, draupnir, or custom (add/settings)")
	fl.harnessVersion = fs.String("harness-version", "", "select an exact catalog version (add/settings)")
	fl.refresh = fs.Bool("refresh", false, "refresh the official ACP registry (harnesses)")
	fl.model = fs.String("model", "", "ACP model ID; empty uses harness default (add/settings)")
	fl.effort = fs.String("effort", "", "ACP reasoning effort; empty uses harness default (add/settings)")
	fl.agentCommand = fs.String("agent-command", "", "custom ACP command as a JSON argument array (add/settings)")
	fl.inherit = fs.Bool("inherit", false, "restore a bot's town defaults (settings --role BOT)")
	fl.closeSeverity = fs.String("review-close-severity", "", "least severe finding (P1, P2 or P3) that closes a pull request after its second review; lower findings become follow-up issues (settings)")
	fl.kind = fs.String("kind", "feature", "feature or bug (request)")
	fl.title = fs.String("title", "", "GitHub issue title (request)")
	fl.bodyFile = fs.String("body-file", "", "issue description file, or - for stdin (request)")
	fl.requestID = fs.String("request-id", "", "saved submission ID (request/check-request)")
	fl.maxWorkers = fs.Int("max-workers", 0, "maximum active bot workers across all towns (capacity)")
	fl.budgetPeriod = fs.String("budget-period", "", "accounting period for this town's agent budget: day, week, month, or none to remove it (settings)")
	fl.budgetAttempts = fs.Int("budget-attempts", 0, "agent attempts allowed per period; 0 leaves attempts uncapped (settings)")
	fl.budgetMinutes = fs.Int("budget-agent-minutes", 0, "agent minutes allowed per period; 0 leaves time uncapped (settings)")
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

type serviceFlags struct {
	dir    *string
	listen *string
	demo   *bool
}

func addServiceFlags(fs *flag.FlagSet) *serviceFlags {
	fl := &serviceFlags{}
	fl.dir = fs.String("state-dir", stateHome(), "private state directory")
	fl.listen = fs.String("listen", defaultListen, "loopback HTTP address for service startup")
	fl.demo = fs.Bool("demo", false, "the isolated simulated town")
	return fl
}

type commandInfo struct {
	name  string
	short string
	long  string
	args  string
	flags []string
}

// globalFlagNames are the persistent flags: they apply to every command.
var globalFlagNames = []string{"demo", "listen", "state-dir"}

var cliCommands = []commandInfo{
	{name: "web", short: "Print the browser address for the town", long: "Print the browser address for the running town.", args: "[flags]", flags: nil},
	{name: "status", short: "Show town state as JSON", long: "Show the town state as JSON.", args: "[flags]", flags: nil},
	{name: "service", short: "Manage the town service", long: "Inspect or stop the town service. See bt service --help for the available actions.", args: "[command] [flags]", flags: nil},
	{name: "capacity", short: "Set the maximum active bot workers", long: "Set the maximum active bot workers across all towns.", args: "--max-workers N [flags]", flags: []string{"max-workers"}},
	{name: "add", short: "Add a town", long: "Add a town for a GitHub repository.", args: "--repo OWNER/REPO [flags]", flags: []string{"agent-command", "effort", "harness", "harness-version", "model", "repo"}},
	{name: "delete", short: "Delete a town", long: "Delete a town. GitHub state stays intact.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role"}},
	{name: "harnesses", short: "List available agent harnesses", long: "List the official ACP registry. Use --refresh to update the cached catalog.", args: "[flags]", flags: []string{"refresh"}},
	{name: "settings", short: "Configure a town or bot", long: "Configure a town's defaults or one bot house. Omit --role to edit town defaults.\n\n" +
		"A budget bounds agent attempts and agent minutes per accounting period. Town cannot cap token or dollar spend: no bundled agent harness reports usage back through the worker protocol.\n\n" +
		"With --role, the work-policy flags choose what that bot takes on: label filters, one selected issue or pull request, a discovery focus, per-run limits, attempts, and its own verification command. Each flag is refused by a bot that cannot honour it.", args: "--repo OWNER/REPO [flags]", flags: []string{"agent-command", "attempts", "budget-agent-minutes", "budget-attempts", "budget-period", "clear-policy", "effort", "exclude-labels", "focus", "harness", "harness-version", "inherit", "labels", "limit", "model", "only", "release-burst", "release-burst-window-seconds", "release-daily-seconds", "release-minimum-gap-seconds", "release-assets", "release-preflight", "release-quiet-seconds", "release-triage", "release-verification-timeout-seconds", "release-workflows", "repo", "review-close-severity", "role", "verify"}},
	{name: "request", short: "Submit a GitHub issue request", long: "Submit a GitHub issue as work for a town.", args: "--repo OWNER/REPO --title TITLE --body-file FILE [flags]", flags: []string{"body-file", "kind", "repo", "request-id", "title"}},
	{name: "check-request", short: "Check a submitted request", long: "Check the status of a submitted request.", args: "--repo OWNER/REPO --request-id ID [flags]", flags: []string{"repo", "request-id"}},
	{name: "start", short: "Start a town or bot house", long: "Start a town or one bot house.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role"}},
	{name: "pause", short: "Pause a town or bot house", long: "Pause a town or one bot house.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role"}},
	{name: "stop", short: "Stop a town or bot house", long: "Stop a town or one bot house.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role"}},
	{name: "retry", short: "Retry a task", long: "Retry a task. Omit --task with --role release to reset the release bot's exhausted attempt budget.", args: "--repo OWNER/REPO [flags]", flags: []string{"repo", "role", "task"}},
	{name: "admit", short: "Admit a Mayoral decision", long: "Admit a pending Mayoral decision.", args: "--repo OWNER/REPO --task ID [flags]", flags: []string{"repo", "task"}},
	{name: "decline", short: "Decline a Mayoral decision", long: "Decline a pending Mayoral decision.", args: "--repo OWNER/REPO --task ID [flags]", flags: []string{"repo", "task"}},
	{name: "serve", short: "Run the town service in the foreground", long: "Run the town service in the foreground.", args: "[flags]", flags: []string{"config", "repo"}},
	{name: "version", short: "Print the version", long: "Print the bt version.", args: "", flags: nil},
}

var serviceCommands = []commandInfo{
	{name: "status", short: "Show service status and logs", long: "Show service status and log locations.", args: "[flags]"},
	{name: "stop", short: "Stop Town and its bots", long: "Stop Town and all its bot processes.", args: "[flags]"},
}

func findCommand(name string) *commandInfo {
	for i := range cliCommands {
		if cliCommands[i].name == name {
			return &cliCommands[i]
		}
	}
	return nil
}

func findServiceCommand(name string) *commandInfo {
	for i := range serviceCommands {
		if serviceCommands[i].name == name {
			return &serviceCommands[i]
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
		rows = append(rows, flagRow{left: "      " + spec, usage: text})
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
	fmt.Fprintln(out, "Run bt in the foreground, or bt -d in the background. Ctrl+C stops Town and its bots.")
	fmt.Fprintln(out, "Use bt web for the browser address. Client commands require a running service.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  bt [command] [flags]")
	fmt.Fprintln(out, "  bt [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Available Commands:")
	withHelp := append(append([]commandInfo(nil), cliCommands...), commandInfo{name: "help", short: "Show help for a command"})
	writeCommands(out, withHelp)
	fmt.Fprintln(out)
	writeFlagSection(out, fs, append([]string{"d", "config", "repo"}, globalFlagNames...), "Flags", "bt")
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
	if name == "service" {
		printServiceHelp(out, fs)
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

func printServiceHelp(out io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(out, "Inspect or stop the running Town service. Start it with bt or bt -d.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  bt service [command] [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Available Commands:")
	writeCommands(out, serviceCommands)
	fmt.Fprintln(out)
	writeFlagSection(out, fs, globalFlagNames, "Flags", "bt service")
	fmt.Fprintln(out)
	fmt.Fprintln(out, `Use "bt service [command] --help" for more information about a command.`)
}

func printServiceVerbHelp(out io.Writer, fs *flag.FlagSet, verb string) {
	c := findServiceCommand(verb)
	if c == nil {
		printServiceHelp(out, fs)
		return
	}
	fmt.Fprintln(out, c.long)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	usage := "  bt service " + c.name
	if c.args != "" {
		usage += " " + c.args
	}
	fmt.Fprintln(out, usage)
	fmt.Fprintln(out)
	writeFlagSection(out, fs, globalFlagNames, "Flags", "bt service "+c.name)
}

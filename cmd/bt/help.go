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
	daemon *bool
	dir    *string
	listen *string
	demo   *bool
	config *string
	json   *bool
}

// addCLIFlags defines the process and connection flags shared by the small
// command surface. Day-to-day operation lives in the browser and local API.
func addCLIFlags(fs *flag.FlagSet) *cliFlags {
	fl := &cliFlags{}
	fl.daemon = fs.Bool("d", false, "run Town in the background")
	fl.dir = fs.String("state-dir", stateHome(), "private state directory")
	fl.listen = fs.String("listen", defaultListen, "loopback HTTP address to serve on")
	fl.demo = fs.Bool("demo", false, "the isolated simulated town")
	fl.config = fs.String("config", "", "optional JSON array or object with max_workers, quiet_hours and towns, applied at startup")
	fl.json = fs.Bool("json", false, "print the full town state as JSON (status)")
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
	{name: "status", short: "Show whether Town is running and its towns", long: "Show whether Town is running, where, and which towns it serves. Use --json for the full town state.", args: "[flags]", flags: []string{"json"}},
	{name: "web", short: "Print the browser address for the town", long: "Print the browser address, with its access key, for the running town.", args: "[flags]", flags: nil},
	{name: "shutdown", short: "Stop Town and its bots", long: "Stop the running Town service and all its bot processes. Use it for a town started with bt -d; Ctrl+C stops one in the foreground.", args: "[flags]", flags: nil},
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
	fmt.Fprintln(out, "Brokk Town — a local service with a browser.")
	fmt.Fprintln(out, "Run bt in the foreground, or bt -d in the background. Ctrl+C or bt shutdown stops Town and its bots.")
	fmt.Fprintln(out, "Use bt web for the browser address. Manage towns, houses, tasks and settings in the browser.")
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

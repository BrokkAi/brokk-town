package main

import (
	"context"
	"flag"
	"strings"
	"testing"
)

func testFlagSet(t *testing.T) *flag.FlagSet {
	t.Helper()
	fs := flag.NewFlagSet("sct test", flag.ContinueOnError)
	addCLIFlags(fs)
	return fs
}

func TestRootHelpHasCobraSections(t *testing.T) {
	var out strings.Builder
	printRootHelp(&out, testFlagSet(t))
	help := out.String()
	for _, section := range []string{"Usage:", "Available Commands:", "Flags:", `Use "sct [command] --help"`} {
		if !strings.Contains(help, section) {
			t.Fatalf("root help missing %q:\n%s", section, help)
		}
	}
	for _, cmd := range []string{"status", "web", "shutdown", "version", "help"} {
		if !strings.Contains(help, "  "+cmd+" ") {
			t.Fatalf("root help omits command %q:\n%s", cmd, help)
		}
	}
	// The root lists the startup and shared connection flags, with a --help row.
	for _, f := range []string{"--state-dir", "--listen", "--demo", "--config", "\n  -d ", "-h, --help"} {
		if !strings.Contains(help, f) {
			t.Fatalf("root help omits flag %q:\n%s", f, help)
		}
	}
}

func TestCommandHelpFiltersToRelevantFlags(t *testing.T) {
	var out strings.Builder
	printCommandHelp(&out, testFlagSet(t), "status")
	status := out.String()
	for _, want := range []string{"Usage:", "sct status", "Flags:", "Global Flags:", "--json", "--state-dir"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status help missing %q:\n%s", want, status)
		}
	}
	for _, other := range []string{"--config", "--listen", "run SlopCop Squad in the background"} {
		if strings.Contains(status, other) {
			t.Fatalf("status help leaks %q:\n%s", other, status)
		}
	}

	out.Reset()
	printCommandHelp(&out, testFlagSet(t), "web")
	if strings.Contains(out.String(), "--json") {
		t.Fatalf("web help offers --json:\n%s", out.String())
	}
}

func TestHelpRequestsNeedNoService(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"help", "status"},
		{"status", "--help"}, {"web", "-h"}, {"shutdown", "--help"}, {"version", "--help"},
	} {
		if err := run(ctx, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestUnknownCommandsPointAtHelp(t *testing.T) {
	ctx := context.Background()
	if err := run(ctx, []string{"bogus"}); err == nil || !strings.Contains(err.Error(), "unknown command") || !strings.Contains(err.Error(), "sct --help") {
		t.Fatalf("bogus command: %v", err)
	}
	if err := run(ctx, []string{"help", "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("help bogus: %v", err)
	}
	for _, removed := range []string{"service", "serve", "capacity", "check-request", "add", "settings", "start", "retry", "guide"} {
		if err := run(ctx, []string{removed}); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("removed command %s: %v", removed, err)
		}
	}
	if err := run(ctx, []string{"--state-dir", t.TempDir(), "extra"}); err == nil || !strings.Contains(err.Error(), "Run 'sct --help'") {
		t.Fatalf("extra arg to bare sct: %v", err)
	}
	if err := run(ctx, []string{"status", "extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") || !strings.Contains(err.Error(), "sct status --help") {
		t.Fatalf("extra arg: %v", err)
	}
}

// A flag the command does not take fails before any service is contacted,
// instead of parsing and being silently ignored.
func TestCommandsRejectFlagsTheyDoNotTake(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--state-dir", dir, "-d"}, "-d: not a flag of sct status"},
		{[]string{"web", "--state-dir", dir, "--json"}, "--json: not a flag of sct web"},
		{[]string{"--state-dir", dir, "--json"}, "--json: not a flag of sct"},
	} {
		err := run(ctx, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}

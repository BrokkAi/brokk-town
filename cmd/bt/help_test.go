package main

import (
	"context"
	"flag"
	"strings"
	"testing"
)

func testFlagSet(t *testing.T) *flag.FlagSet {
	t.Helper()
	fs := flag.NewFlagSet("bt test", flag.ContinueOnError)
	addCLIFlags(fs)
	return fs
}

func TestRootHelpHasCobraSections(t *testing.T) {
	var out strings.Builder
	printRootHelp(&out, testFlagSet(t))
	help := out.String()
	for _, section := range []string{"Usage:", "Available Commands:", "Flags:", `Use "bt [command] --help"`} {
		if !strings.Contains(help, section) {
			t.Fatalf("root help missing %q:\n%s", section, help)
		}
	}
	for _, cmd := range []string{"status", "shutdown", "add", "request", "help"} {
		if !strings.Contains(help, "  "+cmd+" ") {
			t.Fatalf("root help omits command %q:\n%s", cmd, help)
		}
	}
	// serve is the hidden alias for bare bt.
	if strings.Contains(help, "  serve ") {
		t.Fatalf("root help lists the serve alias:\n%s", help)
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
	printCommandHelp(&out, testFlagSet(t), "add")
	add := out.String()
	for _, want := range []string{"Usage:", "bt add", "Flags:", "Global Flags:", "--repo", "--harness"} {
		if !strings.Contains(add, want) {
			t.Fatalf("add help missing %q:\n%s", want, add)
		}
	}
	for _, other := range []string{"--max-workers", "--body-file"} {
		if strings.Contains(add, other) {
			t.Fatalf("add help leaks %q:\n%s", other, add)
		}
	}

	// --listen only starts Town; client commands never show it.
	if strings.Contains(add, "--listen") {
		t.Fatalf("add help shows --listen:\n%s", add)
	}

	out.Reset()
	printCommandHelp(&out, testFlagSet(t), "delete")
	if strings.Contains(out.String(), "--role") {
		t.Fatalf("delete help offers --role:\n%s", out.String())
	}
}

func TestHelpRequestsNeedNoService(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"help", "add"}, {"help", "serve"},
		{"add", "--help"}, {"settings", "-h"}, {"shutdown", "--help"}, {"version", "--help"},
	} {
		if err := run(ctx, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestUnknownCommandsPointAtHelp(t *testing.T) {
	ctx := context.Background()
	if err := run(ctx, []string{"bogus"}); err == nil || !strings.Contains(err.Error(), "unknown command") || !strings.Contains(err.Error(), "bt --help") {
		t.Fatalf("bogus command: %v", err)
	}
	if err := run(ctx, []string{"help", "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("help bogus: %v", err)
	}
	for _, removed := range []string{"service", "capacity", "check-request"} {
		if err := run(ctx, []string{removed}); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("removed command %s: %v", removed, err)
		}
	}
	if err := run(ctx, []string{"status", "extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") || !strings.Contains(err.Error(), "bt status --help") {
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
		{[]string{"start", "--state-dir", dir, "--repo", "a/b", "--harness", "x"}, "--harness: not a flag of bt start"},
		{[]string{"add", "--state-dir", dir, "--repo", "a/b", "--listen", "127.0.0.1:1"}, "--listen: not a flag of bt add"},
		{[]string{"delete", "--state-dir", dir, "--repo", "a/b", "--role", "bug"}, "--role: not a flag of bt delete"},
		{[]string{"status", "--state-dir", dir, "-d"}, "-d: not a flag of bt status"},
		{[]string{"--state-dir", dir, "--repo", "a/b"}, "--repo: not a flag of bt"},
		{[]string{"serve", "--state-dir", dir, "--max-workers", "2"}, "--max-workers: not a flag of bt serve"},
	} {
		err := run(ctx, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}

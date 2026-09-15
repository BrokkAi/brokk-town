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

func testServiceFlagSet(t *testing.T) *flag.FlagSet {
	t.Helper()
	fs := flag.NewFlagSet("bt service test", flag.ContinueOnError)
	addServiceFlags(fs)
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
	for _, cmd := range []string{"tui", "service", "add", "request", "serve", "help"} {
		if !strings.Contains(help, cmd) {
			t.Fatalf("root help omits command %q:\n%s", cmd, help)
		}
	}
	// The root lists the shared connection flags once, with a --help row.
	for _, f := range []string{"--state-dir", "--listen", "--demo", "-h, --help"} {
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

	out.Reset()
	printCommandHelp(&out, testFlagSet(t), "capacity")
	capacity := out.String()
	if !strings.Contains(capacity, "--max-workers") || strings.Contains(capacity, "--repo") {
		t.Fatalf("capacity flags wrong:\n%s", capacity)
	}
}

func TestServiceHelpListsVerbs(t *testing.T) {
	var out strings.Builder
	printServiceHelp(&out, testServiceFlagSet(t))
	help := out.String()
	for _, section := range []string{"Usage:", "bt service [command]", "Available Commands:", "Flags:", `Use "bt service [command] --help"`} {
		if !strings.Contains(help, section) {
			t.Fatalf("service help missing %q:\n%s", section, help)
		}
	}
	for _, verb := range []string{"status", "on", "off", "stop", "restart"} {
		if !strings.Contains(help, verb) {
			t.Fatalf("service help omits verb %q:\n%s", verb, help)
		}
	}
}

func TestHelpRequestsNeedNoService(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"help", "add"}, {"help", "service"}, {"help", "service", "status"},
		{"add", "--help"}, {"capacity", "-h"}, {"service", "--help"}, {"service", "status", "--help"},
		{"service", "help"}, {"version", "--help"},
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
	if err := run(ctx, []string{"service", "bogus", "--state-dir", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "unknown service command") || !strings.Contains(err.Error(), "bt service --help") {
		t.Fatalf("bogus service verb: %v", err)
	}
	if err := run(ctx, []string{"status", "extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") || !strings.Contains(err.Error(), "bt status --help") {
		t.Fatalf("extra arg: %v", err)
	}
}

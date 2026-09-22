package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func runService(ctx context.Context, args []string) error {
	verb := "status"
	if len(args) > 0 && args[0][0] != '-' {
		verb = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("bt service", flag.ContinueOnError)
	fl := addServiceFlags(fs)
	if verb == "help" || wantsHelp(args) {
		printServiceHelp(os.Stdout, fs)
		return nil
	}
	if findServiceCommand(verb) == nil {
		return fmt.Errorf("unknown service command %q; run bt service --help", verb)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	base, err := filepath.Abs(*fl.dir)
	if err != nil {
		return err
	}
	dir := runtimeDir(base, *fl.demo)
	conn, alive := serviceAlive(ctx, dir)
	if verb == "status" {
		if alive {
			fmt.Printf("Town %s running (pid %d at %s)\n", conn.Version, conn.PID, conn.URL)
		} else {
			fmt.Println("Town is stopped")
		}
		fmt.Println("Logs:", filepath.Join(dir, "logs"))
		return nil
	}
	if !alive {
		return fmt.Errorf("Town is not running")
	}
	return stopProcess(ctx, dir, conn)
}

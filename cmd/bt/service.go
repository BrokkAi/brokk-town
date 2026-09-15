package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/daemon"
)

// runService handles `bt service VERB`: the small, optional surface over a
// lifecycle that otherwise needs no attention.
func runService(ctx context.Context, args []string) error {
	verb := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("bt service "+verb, flag.ContinueOnError)
	dir := fs.String("state-dir", stateHome(), "private state directory")
	listenFlag := fs.String("listen", defaultListen, "loopback HTTP address for the service; remembered for later starts")
	demo := fs.Bool("demo", false, "the isolated simulated town")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: bt service [status|on|off|stop|restart] [options]\n\nEvery bt command starts the town service when it is down and registers it with your login session.\n  status   show the service, its registration, and log locations\n  on       register the service with launchd or systemd --user (the default)\n  off      keep the service out of your login session; bt still starts it on demand\n  stop     stop the service until the next bt command\n  restart  restart the service in place\n")
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
	base, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	address, err := resolveListen(base, *demo, *listenFlag, flagSet(fs, "listen"))
	if err != nil {
		return err
	}
	listen := &address
	runtime := runtimeDir(base, *demo)
	switch verb {
	case "status":
		return serviceStatus(ctx, base, *demo)
	case "on":
		if *demo {
			return errors.New("the demo town is never registered with the login session")
		}
		if err := setRegistration(base, true); err != nil {
			return err
		}
		if conn, alive := serviceAlive(ctx, runtime); alive && !conn.Managed {
			if err := stopProcess(ctx, runtime, conn); err != nil {
				return err
			}
		}
		m, err := newManager(warn)
		if err != nil {
			return err
		}
		spec, err := buildSpec(base, *listen)
		if err != nil {
			return err
		}
		if err := m.Ensure(ctx, spec); err != nil {
			return err
		}
		conn, err := waitReady(ctx, runtime, time.Time{})
		if err != nil {
			return err
		}
		fmt.Printf("Registered with %s: %s\nTown: %s\n", m.Name(), m.UnitPath(), conn.URL)
		return nil
	case "off":
		if err := setRegistration(base, false); err != nil {
			return err
		}
		conn, alive := serviceAlive(ctx, runtime)
		if m, err := newManager(warn); err == nil {
			if err := m.Uninstall(ctx); err != nil {
				return err
			}
		}
		if alive && conn.Managed {
			// Uninstalling stops the supervised job; keep the town available.
			if err := spawnService(base, *demo, *listen); err != nil {
				return err
			}
			if _, err := waitReady(ctx, runtime, conn.Started); err != nil {
				return err
			}
		}
		fmt.Println("Login registration is off. bt still starts the town on demand; it will not return by itself after logout or reboot.")
		return nil
	case "stop":
		// Unload the supervised job first, even when no service answers: a job
		// that is crash-looping or mid-restart has no connection file.
		unloaded := false
		if !*demo {
			if m, err := newManager(warn); err == nil {
				if status, _ := m.Status(ctx); status.Loaded {
					if err := m.Stop(ctx); err != nil {
						return err
					}
					unloaded = true
				}
			}
		}
		conn, readErr := readConnection(runtime)
		running := readErr == nil && processAlive(conn.PID)
		if running {
			if err := stopProcess(ctx, runtime, conn); err != nil {
				return err
			}
		}
		if !unloaded && !running {
			fmt.Println("The town service is not running.")
			return nil
		}
		fmt.Println("Stopped the town service. The next bt command starts it again; use bt service off to keep it out of your login session.")
		return nil
	case "restart":
		conn, alive := serviceAlive(ctx, runtime)
		if !alive {
			conn, err = ensureService(ctx, base, *demo, *listen)
		} else {
			conn, err = rollService(ctx, base, *demo, *listen, conn)
		}
		if err != nil {
			return err
		}
		fmt.Printf("Town %s at %s (pid %d)\n", conn.Version, conn.URL, conn.PID)
		return nil
	}
	return fmt.Errorf("unknown service command %q", verb)
}

func serviceStatus(ctx context.Context, base string, demo bool) error {
	runtime := runtimeDir(base, demo)
	stdout, stderr := logPaths(runtime)
	registration := "on"
	if !registrationEnabled(base) {
		registration = "off"
	}
	if demo {
		registration = "never (demo)"
	}
	var status daemon.Status
	var manager daemon.Manager
	if m, err := newManager(warn); err == nil {
		manager = m
		status, _ = m.Status(ctx)
	}
	conn, alive := serviceAlive(ctx, runtime)
	fmt.Printf("Registration: %s\n", registration)
	if manager != nil {
		detail := "not installed"
		if status.Installed {
			detail = status.Detail
			if status.PID > 0 {
				detail += fmt.Sprintf(" (pid %d)", status.PID)
			}
		}
		fmt.Printf("%-13s %s — %s\n", manager.Name()+":", manager.UnitPath(), detail)
	}
	switch {
	case alive:
		mode := "started on demand"
		if conn.Managed {
			mode = "login session"
		}
		fmt.Printf("Town:         %s %s, pid %d, %s\n", conn.Version, conn.URL, conn.PID, mode)
		if conn.Executable != "" {
			fmt.Printf("Executable:   %s\n", conn.Executable)
			if _, err := os.Stat(conn.Executable); err != nil {
				fmt.Println("              (missing; the service cannot restart itself until bt is reinstalled)")
			}
		}
		if status.Loaded && status.PID > 0 && status.PID != conn.PID {
			fmt.Printf("Note:         pid %d holds the state directory, not the registered job (pid %d); stop it before the registered service can run\n", conn.PID, status.PID)
		}
	case conn.PID > 0:
		fmt.Printf("Town:         not responding (stale connection file from pid %d); the next bt command starts it\n", conn.PID)
	default:
		fmt.Println("Town:         not running; the next bt command starts it")
	}
	fmt.Printf("Logs:         %s\n              %s\n", stdout, stderr)
	if !alive {
		return errors.New("town service is not running")
	}
	return nil
}

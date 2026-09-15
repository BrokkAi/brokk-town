// Package daemon registers the town service with the user's login session so
// it restarts after crashes and returns after a reboot without any command.
// macOS uses a launchd agent; Linux uses a systemd user unit. Both backends
// compile everywhere so their templates and command sequences are tested on
// every platform; only New selects one at runtime.
package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// EnvVar is one environment entry written into the unit. Supervisors start
// jobs without a login shell, so PATH and HOME must be captured explicitly.
type EnvVar struct {
	Key   string
	Value string
}

// Spec describes the job to register. Command holds the absolute executable
// and its arguments; Env is sorted by Ensure so rendering is deterministic.
type Spec struct {
	Command    []string
	WorkingDir string
	Env        []EnvVar
	StdoutPath string
	StderrPath string
}

// Status is the supervisor's view of the job.
type Status struct {
	Installed bool // The unit file exists.
	Loaded    bool // The supervisor knows the job.
	Running   bool
	PID       int
	Detail    string
}

// Runner executes a supervisor command and returns its stdout. Errors carry
// stderr so callers can recognize "not loaded" conditions.
type Runner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// Manager is one supervisor backend.
type Manager interface {
	Name() string
	UnitPath() string
	Render(Spec) ([]byte, error)
	// Ensure writes the unit when its content changed, loads it when it is
	// not loaded, and starts the job. It is idempotent.
	Ensure(ctx context.Context, spec Spec) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Uninstall(ctx context.Context) error
	Status(ctx context.Context) (Status, error)
}

// Options select and configure a backend.
type Options struct {
	GOOS       string
	Home       string
	ConfigHome string // XDG_CONFIG_HOME; defaults to Home/.config.
	UID        int
	Runner     Runner       // nil runs the real supervisor commands.
	Warn       func(string) // Receives non-fatal notes; nil discards them.
}

// ErrUnsupported reports a platform without a supported login supervisor.
var ErrUnsupported = errors.New("no supported login-session supervisor")

// ErrNotLoaded reports an operation on a job the supervisor does not know.
var ErrNotLoaded = errors.New("service is not registered")

func New(o Options) (Manager, error) {
	if o.Runner == nil {
		o.Runner = osRunner{}
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
	}
	if o.Home == "" {
		return nil, errors.New("home directory is required")
	}
	switch o.GOOS {
	case "darwin":
		return &launchd{home: o.Home, uid: o.UID, run: o.Runner}, nil
	case "linux":
		configHome := o.ConfigHome
		if configHome == "" {
			configHome = filepath.Join(o.Home, ".config")
		}
		return &systemd{configHome: configHome, run: o.Runner, warn: o.Warn}, nil
	}
	return nil, fmt.Errorf("%w on %s", ErrUnsupported, o.GOOS)
}

type osRunner struct{}

func (osRunner) Run(ctx context.Context, args ...string) (string, error) {
	return osrun.Run(ctx, "", nil, args...)
}

// normalize sorts the environment so equal specs render identically.
func normalize(spec Spec) Spec {
	env := append([]EnvVar(nil), spec.Env...)
	sort.Slice(env, func(i, j int) bool { return env[i].Key < env[j].Key })
	spec.Env = env
	return spec
}

func validate(spec Spec) error {
	if len(spec.Command) == 0 || !filepath.IsAbs(spec.Command[0]) {
		return errors.New("spec requires an absolute executable path")
	}
	if !filepath.IsAbs(spec.WorkingDir) || !filepath.IsAbs(spec.StdoutPath) || !filepath.IsAbs(spec.StderrPath) {
		return errors.New("spec requires absolute directory and log paths")
	}
	for _, e := range spec.Env {
		if e.Key == "" || strings.ContainsAny(e.Key, "=\n") || strings.Contains(e.Value, "\n") {
			return fmt.Errorf("invalid environment entry %q", e.Key)
		}
	}
	return nil
}

// writeUnit installs content at path when it differs. It reports whether the
// file changed so callers can decide whether the supervisor must reload it.
func writeUnit(path string, content []byte, mode os.FileMode) (changed bool, err error) {
	if current, e := os.ReadFile(path); e == nil && bytes.Equal(current, content) {
		return false, nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	staged, err := os.CreateTemp(filepath.Dir(path), ".brokk-town.*")
	if err != nil {
		return false, err
	}
	name := staged.Name()
	defer os.Remove(name)
	if _, err = staged.Write(content); err != nil {
		staged.Close()
		return false, err
	}
	if err = staged.Chmod(mode); err != nil {
		staged.Close()
		return false, err
	}
	if err = staged.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(name, path)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

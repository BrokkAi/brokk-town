package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

// SystemdUnit is the user unit name.
const SystemdUnit = "brokk-town.service"

// systemd registers a user unit. Restart=always with no start limit lets the
// job recover once a foreground service releases the state lock. KillMode
// process signals only the service itself: bot processes it deliberately
// leaves running across a restart must never be killed with the unit's cgroup.
type systemd struct {
	configHome string
	run        Runner
	warn       func(string)
}

var unitTemplate = template.Must(template.New("unit").Funcs(template.FuncMap{"quote": systemdQuote, "specifiers": escapeSpecifiers}).Parse(`[Unit]
Description=Brokk Town local service
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart={{range $i, $a := .Command}}{{if $i}} {{end}}{{quote $a}}{{end}}
WorkingDirectory={{specifiers .WorkingDir}}
{{- range .Env}}
Environment={{quote (printf "%s=%s" .Key .Value)}}
{{- end}}
Restart=always
RestartSec=5
TimeoutStopSec=30
KillMode=process
UMask=0077
StandardOutput=append:{{specifiers .StdoutPath}}
StandardError=append:{{specifiers .StderrPath}}

[Install]
WantedBy=default.target
`))

// systemdQuote produces one double-quoted word for ExecStart/Environment,
// escaping the quote and backslash characters and doubling % specifiers.
func systemdQuote(s string) string {
	s = escapeSpecifiers(s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func escapeSpecifiers(s string) string { return strings.ReplaceAll(s, "%", "%%") }

func (s *systemd) Name() string { return "systemd --user" }
func (s *systemd) UnitPath() string {
	return filepath.Join(s.configHome, "systemd", "user", SystemdUnit)
}

func (s *systemd) Render(spec Spec) ([]byte, error) {
	spec = normalize(spec)
	if err := validate(spec); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err := unitTemplate.Execute(&buf, spec)
	return buf.Bytes(), err
}

func (s *systemd) ctl(ctx context.Context, args ...string) (string, error) {
	return s.run.Run(ctx, append([]string{"systemctl", "--user"}, args...)...)
}

func (s *systemd) Ensure(ctx context.Context, spec Spec) error {
	content, err := s.Render(spec)
	if err != nil {
		return err
	}
	fresh := !exists(s.UnitPath())
	changed, err := writeUnit(s.UnitPath(), content, 0644)
	if err != nil {
		return err
	}
	if _, err = s.ctl(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if _, err = s.ctl(ctx, "enable", SystemdUnit); err != nil {
		return fmt.Errorf("systemctl enable: %w", err)
	}
	// restart applies a changed definition to a running job; start leaves a
	// running job alone.
	action := "start"
	if changed && !fresh {
		action = "restart"
	}
	if _, err = s.ctl(ctx, action, SystemdUnit); err != nil {
		return fmt.Errorf("systemctl %s: %w", action, err)
	}
	if fresh {
		// Without lingering the user manager stops at logout and does not
		// start at boot. This needs polkit approval on some distributions.
		if _, e := s.run.Run(ctx, "loginctl", "enable-linger"); e != nil {
			s.warn("loginctl enable-linger failed; the town stops at logout until you run it yourself: " + e.Error())
		}
	}
	return nil
}

func (s *systemd) Restart(ctx context.Context) error {
	if !exists(s.UnitPath()) {
		return ErrNotLoaded
	}
	if _, err := s.ctl(ctx, "restart", SystemdUnit); err != nil {
		return fmt.Errorf("systemctl restart: %w", err)
	}
	return nil
}

func (s *systemd) Stop(ctx context.Context) error {
	if _, err := s.ctl(ctx, "stop", SystemdUnit); err != nil && !notLoaded(err) {
		return fmt.Errorf("systemctl stop: %w", err)
	}
	return nil
}

func (s *systemd) Uninstall(ctx context.Context) error {
	if _, err := s.ctl(ctx, "disable", "--now", SystemdUnit); err != nil && !notLoaded(err) {
		return fmt.Errorf("systemctl disable: %w", err)
	}
	if err := os.Remove(s.UnitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = s.ctl(ctx, "daemon-reload")
	return nil
}

func (s *systemd) Status(ctx context.Context) (Status, error) {
	status := Status{Installed: exists(s.UnitPath())}
	out, err := s.ctl(ctx, "show", "-p", "LoadState", "-p", "ActiveState", "-p", "SubState", "-p", "MainPID", "-p", "ExecMainStatus", SystemdUnit)
	if err != nil {
		status.Detail = "not loaded"
		return status, nil
	}
	values := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			values[key] = value
		}
	}
	status.Loaded = values["LoadState"] == "loaded"
	status.Running = values["ActiveState"] == "active"
	status.PID, _ = strconv.Atoi(values["MainPID"])
	status.Detail = values["ActiveState"]
	if values["SubState"] != "" {
		status.Detail += " (" + values["SubState"] + ")"
	}
	if !status.Running && values["ExecMainStatus"] != "" && values["ExecMainStatus"] != "0" {
		status.Detail += "; last exit code " + values["ExecMainStatus"]
	}
	if !status.Loaded {
		status.Detail = "not loaded"
	}
	return status, nil
}

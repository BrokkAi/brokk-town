package daemon

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

// LaunchdLabel is the agent label under the user's gui domain.
const LaunchdLabel = "ai.brokk.town"

// launchd registers a LaunchAgent. KeepAlive restarts the job after any
// exit, including the clean exit a self-upgrade performs; ThrottleInterval
// bounds a crash loop; ExitTimeOut covers the service's graceful shutdown.
type launchd struct {
	home string
	uid  int
	run  Runner
}

var plistTemplate = template.Must(template.New("plist").Funcs(template.FuncMap{"xml": escapeXML}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{xml .Label}}</string>
	<key>ProgramArguments</key>
	<array>
{{- range .Spec.Command}}
		<string>{{xml .}}</string>
{{- end}}
	</array>
	<key>WorkingDirectory</key>
	<string>{{xml .Spec.WorkingDir}}</string>
	<key>EnvironmentVariables</key>
	<dict>
{{- range .Spec.Env}}
		<key>{{xml .Key}}</key>
		<string>{{xml .Value}}</string>
{{- end}}
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>ExitTimeOut</key>
	<integer>30</integer>
	<key>StandardOutPath</key>
	<string>{{xml .Spec.StdoutPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{xml .Spec.StderrPath}}</string>
</dict>
</plist>
`))

func escapeXML(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func (l *launchd) Name() string { return "launchd" }
func (l *launchd) UnitPath() string {
	return filepath.Join(l.home, "Library", "LaunchAgents", LaunchdLabel+".plist")
}
func (l *launchd) domain() string { return "gui/" + strconv.Itoa(l.uid) }
func (l *launchd) target() string { return l.domain() + "/" + LaunchdLabel }

func (l *launchd) Render(spec Spec) ([]byte, error) {
	spec = normalize(spec)
	if err := validate(spec); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err := plistTemplate.Execute(&buf, struct {
		Label string
		Spec  Spec
	}{LaunchdLabel, spec})
	return buf.Bytes(), err
}

func (l *launchd) loaded(ctx context.Context) bool {
	_, err := l.run.Run(ctx, "launchctl", "print", l.target())
	return err == nil
}

func (l *launchd) Ensure(ctx context.Context, spec Spec) error {
	content, err := l.Render(spec)
	if err != nil {
		return err
	}
	// launchd refuses agents that are writable by anyone but the owner.
	changed, err := writeUnit(l.UnitPath(), content, 0644)
	if err != nil {
		return err
	}
	loaded := l.loaded(ctx)
	if loaded && changed {
		// A loaded job keeps its old definition until it is bootstrapped again.
		if err = l.bootout(ctx); err != nil {
			return err
		}
		loaded = false
	}
	if !loaded {
		if _, err = l.run.Run(ctx, "launchctl", "bootstrap", l.domain(), l.UnitPath()); err != nil {
			return fmt.Errorf("launchctl bootstrap: %w", err)
		}
	}
	// RunAtLoad starts a freshly bootstrapped job; kickstart covers a job that
	// was already loaded but stopped, and is a no-op when it is running.
	if _, err = l.run.Run(ctx, "launchctl", "kickstart", l.target()); err != nil {
		return fmt.Errorf("launchctl kickstart: %w", err)
	}
	return nil
}

func (l *launchd) Restart(ctx context.Context) error {
	if !l.loaded(ctx) {
		return ErrNotLoaded
	}
	if _, err := l.run.Run(ctx, "launchctl", "kickstart", "-k", l.target()); err != nil {
		return fmt.Errorf("launchctl kickstart: %w", err)
	}
	return nil
}

// bootout unloads the job. With KeepAlive a plain kill is undone at once, so
// stopping means unloading; the plist stays for the next login or Ensure.
func (l *launchd) bootout(ctx context.Context) error {
	_, err := l.run.Run(ctx, "launchctl", "bootout", l.target())
	if err != nil && !notLoaded(err) {
		return fmt.Errorf("launchctl bootout: %w", err)
	}
	return nil
}

func notLoaded(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "could not find service") || strings.Contains(text, "no such process") ||
		strings.Contains(text, "not loaded") || strings.Contains(text, "not found") || strings.Contains(text, "input/output error")
}

func (l *launchd) Stop(ctx context.Context) error { return l.bootout(ctx) }

func (l *launchd) Uninstall(ctx context.Context) error {
	if err := l.bootout(ctx); err != nil {
		return err
	}
	if err := os.Remove(l.UnitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *launchd) Status(ctx context.Context) (Status, error) {
	status := Status{Installed: exists(l.UnitPath())}
	out, err := l.run.Run(ctx, "launchctl", "print", l.target())
	if err != nil {
		status.Detail = "not loaded"
		return status, nil
	}
	status.Loaded = true
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}
		switch key {
		case "state":
			status.Detail = value
			status.Running = value == "running"
		case "pid":
			status.PID, _ = strconv.Atoi(value)
		case "last exit code":
			if !status.Running {
				status.Detail = "not running; last exit code " + value
			}
		}
	}
	if status.PID > 0 && status.Detail == "" {
		status.Running = true
		status.Detail = "running"
	}
	return status, nil
}

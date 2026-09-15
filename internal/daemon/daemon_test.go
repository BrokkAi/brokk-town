package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner scripts supervisor answers by the joined command line and
// records every invocation in order.
type fakeRunner struct {
	calls   []string
	answers map[string]func() (string, error)
}

func (f *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	line := strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if answer, ok := f.answers[line]; ok {
		return answer()
	}
	for prefix, answer := range f.answers {
		if strings.HasSuffix(prefix, "*") && strings.HasPrefix(line, strings.TrimSuffix(prefix, "*")) {
			return answer()
		}
	}
	return "", nil
}

func ok(out string) func() (string, error) { return func() (string, error) { return out, nil } }
func fail(text string) func() (string, error) {
	return func() (string, error) { return "", errors.New(text) }
}

func spec(home string) Spec {
	return Spec{
		Command:    []string{"/opt/tools & more/bt", "serve", "--state-dir", filepath.Join(home, "state <dir>"), "--listen", "127.0.0.1:8099"},
		WorkingDir: filepath.Join(home, "state <dir>"),
		Env:        []EnvVar{{Key: "PATH", Value: "/usr/bin:/opt/100%/bin"}, {Key: "BROKK_TOWN_MANAGED", Value: "1"}, {Key: "HOME", Value: home}},
		StdoutPath: filepath.Join(home, "state <dir>", "logs", "serve.log"),
		StderrPath: filepath.Join(home, "state <dir>", "logs", "serve.err.log"),
	}
}

func TestNewSelectsBackendByPlatform(t *testing.T) {
	if _, err := New(Options{GOOS: "windows", Home: "/home/me"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported platform accepted: %v", err)
	}
	if _, err := New(Options{GOOS: "darwin"}); err == nil {
		t.Fatal("missing home accepted")
	}
	mac, err := New(Options{GOOS: "darwin", Home: "/Users/me", UID: 501})
	if err != nil || mac.Name() != "launchd" || mac.UnitPath() != "/Users/me/Library/LaunchAgents/ai.brokk.town.plist" {
		t.Fatalf("%v %v", err, mac)
	}
	linux, err := New(Options{GOOS: "linux", Home: "/home/me"})
	if err != nil || linux.Name() != "systemd --user" || linux.UnitPath() != "/home/me/.config/systemd/user/brokk-town.service" {
		t.Fatalf("%v %v", err, linux)
	}
	custom, _ := New(Options{GOOS: "linux", Home: "/home/me", ConfigHome: "/cfg"})
	if custom.UnitPath() != "/cfg/systemd/user/brokk-town.service" {
		t.Fatal(custom.UnitPath())
	}
}

func TestLaunchdPlistEscapesAndSupervises(t *testing.T) {
	home := t.TempDir()
	m := &launchd{home: home, uid: 501, run: &fakeRunner{}}
	out, err := m.Render(spec(home))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"<string>/opt/tools &amp; more/bt</string>",
		"state &lt;dir&gt;</string>",
		"<key>KeepAlive</key>\n\t<true/>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>ThrottleInterval</key>\n\t<integer>10</integer>",
		"<key>ExitTimeOut</key>\n\t<integer>30</integer>",
		"<key>BROKK_TOWN_MANAGED</key>\n\t\t<string>1</string>",
		"<key>StandardOutPath</key>",
		"serve.err.log</string>",
		"<string>ai.brokk.town</string>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plist lacks %q:\n%s", want, text)
		}
	}
	// Environment keys render sorted regardless of input order.
	if strings.Index(text, "BROKK_TOWN_MANAGED") > strings.Index(text, "<key>HOME</key>") || strings.Index(text, "<key>HOME</key>") > strings.Index(text, "<key>PATH</key>") {
		t.Fatal("environment is not sorted")
	}
	if _, err := m.Render(Spec{Command: []string{"bt"}}); err == nil {
		t.Fatal("relative executable accepted")
	}
	if _, err := m.Render(Spec{Command: []string{"/bin/bt"}, WorkingDir: "/w", StdoutPath: "/o", StderrPath: "/e", Env: []EnvVar{{Key: "A=B", Value: "x"}}}); err == nil {
		t.Fatal("invalid env key accepted")
	}
}

func TestLaunchdEnsureBootstrapsOnceAndReloadsChangedDefinitions(t *testing.T) {
	home := t.TempDir()
	runner := &fakeRunner{answers: map[string]func() (string, error){
		"launchctl print gui/501/ai.brokk.town": fail("Could not find service \"ai.brokk.town\" in domain for uid: 501"),
	}}
	m := &launchd{home: home, uid: 501, run: runner}
	ctx := context.Background()
	if err := m.Ensure(ctx, spec(home)); err != nil {
		t.Fatal(err)
	}
	plist := m.UnitPath()
	info, err := os.Stat(plist)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("plist not written with owner-only write: %v %v", err, info)
	}
	want := []string{
		"launchctl print gui/501/ai.brokk.town",
		"launchctl bootstrap gui/501 " + plist,
		"launchctl kickstart gui/501/ai.brokk.town",
	}
	if strings.Join(runner.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("first ensure ran:\n%s", strings.Join(runner.calls, "\n"))
	}

	// Loaded and unchanged: only a kickstart, which is a no-op while running.
	runner.calls = nil
	runner.answers["launchctl print gui/501/ai.brokk.town"] = ok("gui/501/ai.brokk.town = {\n\tstate = running\n\tpid = 4242\n}")
	if err := m.Ensure(ctx, spec(home)); err != nil {
		t.Fatal(err)
	}
	if strings.Join(runner.calls, "\n") != "launchctl print gui/501/ai.brokk.town\nlaunchctl kickstart gui/501/ai.brokk.town" {
		t.Fatalf("unchanged ensure ran:\n%s", strings.Join(runner.calls, "\n"))
	}

	// Loaded and changed (new binary path): bootout, then bootstrap again.
	runner.calls = nil
	changed := spec(home)
	changed.Command[0] = "/opt/new/bt"
	if err := m.Ensure(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if strings.Join(runner.calls, "\n") != strings.Join([]string{
		"launchctl print gui/501/ai.brokk.town",
		"launchctl bootout gui/501/ai.brokk.town",
		"launchctl bootstrap gui/501 " + plist,
		"launchctl kickstart gui/501/ai.brokk.town",
	}, "\n") {
		t.Fatalf("changed ensure ran:\n%s", strings.Join(runner.calls, "\n"))
	}
	if content, _ := os.ReadFile(plist); !strings.Contains(string(content), "/opt/new/bt") {
		t.Fatal("plist not rewritten")
	}

	status, err := m.Status(ctx)
	if err != nil || !status.Installed || !status.Loaded || !status.Running || status.PID != 4242 {
		t.Fatalf("status %+v %v", status, err)
	}
	runner.answers["launchctl print gui/501/ai.brokk.town"] = ok("gui/501/ai.brokk.town = {\n\tstate = not running\n\tlast exit code = 1\n}")
	status, _ = m.Status(ctx)
	if !status.Loaded || status.Running || !strings.Contains(status.Detail, "last exit code 1") {
		t.Fatalf("crashed status %+v", status)
	}

	// Restart kicks the loaded job; bootstrap failures surface for fallback.
	runner.calls = nil
	if err := m.Restart(ctx); err != nil || runner.calls[len(runner.calls)-1] != "launchctl kickstart -k gui/501/ai.brokk.town" {
		t.Fatalf("%v %v", err, runner.calls)
	}
	runner.answers["launchctl print gui/501/ai.brokk.town"] = fail("not found")
	if err := m.Restart(ctx); !errors.Is(err, ErrNotLoaded) {
		t.Fatal(err)
	}
	runner.answers["launchctl bootstrap gui/501 "+plist] = fail("Bootstrap failed: 125: Domain does not support specified action")
	if err := m.Ensure(ctx, spec(home)); err == nil || !strings.Contains(err.Error(), "Domain does not support") {
		t.Fatalf("bootstrap failure hidden: %v", err)
	}

	// Uninstall tolerates an unloaded job and removes the plist.
	runner.answers["launchctl bootout gui/501/ai.brokk.town"] = fail("Boot-out failed: 3: No such process")
	if err := m.Uninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Fatal("plist remains")
	}
	status, _ = m.Status(ctx)
	if status.Installed || status.Loaded {
		t.Fatalf("status after uninstall %+v", status)
	}
}

func TestSystemdUnitQuotesAndSupervises(t *testing.T) {
	home := t.TempDir()
	warnings := []string{}
	runner := &fakeRunner{answers: map[string]func() (string, error){
		"loginctl enable-linger": fail("Could not enable linger: Access denied"),
	}}
	m := &systemd{configHome: filepath.Join(home, ".config"), run: runner, warn: func(s string) { warnings = append(warnings, s) }}
	out, err := m.Render(spec(home))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		`ExecStart="/opt/tools & more/bt" "serve" "--state-dir" "` + filepath.Join(home, "state <dir>") + `" "--listen" "127.0.0.1:8099"`,
		`Environment="PATH=/usr/bin:/opt/100%%/bin"`,
		`Environment="BROKK_TOWN_MANAGED=1"`,
		"Restart=always",
		"RestartSec=5",
		"StartLimitIntervalSec=0",
		"KillMode=process",
		"StandardOutput=append:" + filepath.Join(home, "state <dir>", "logs", "serve.log"),
		"WantedBy=default.target",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("unit lacks %q:\n%s", want, text)
		}
	}
	if q := systemdQuote(`a"b\c%d`); q != `"a\"b\\c%%d"` {
		t.Fatal(q)
	}

	ctx := context.Background()
	if err := m.Ensure(ctx, spec(home)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable brokk-town.service",
		"systemctl --user start brokk-town.service",
		"loginctl enable-linger",
	}
	if strings.Join(runner.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("first ensure ran:\n%s", strings.Join(runner.calls, "\n"))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "linger") {
		t.Fatalf("linger failure not surfaced: %v", warnings)
	}

	// Second ensure with a changed definition restarts instead of starting
	// and does not retry lingering.
	runner.calls = nil
	changed := spec(home)
	changed.Command[0] = "/opt/new/bt"
	if err := m.Ensure(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if strings.Join(runner.calls, "\n") != "systemctl --user daemon-reload\nsystemctl --user enable brokk-town.service\nsystemctl --user restart brokk-town.service" {
		t.Fatalf("changed ensure ran:\n%s", strings.Join(runner.calls, "\n"))
	}
	runner.calls = nil
	if err := m.Ensure(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if runner.calls[len(runner.calls)-1] != "systemctl --user start brokk-town.service" {
		t.Fatalf("unchanged ensure ran:\n%s", strings.Join(runner.calls, "\n"))
	}

	runner.answers["systemctl --user show *"] = ok("LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=4242\nExecMainStatus=0")
	status, err := m.Status(ctx)
	if err != nil || !status.Installed || !status.Loaded || !status.Running || status.PID != 4242 || status.Detail != "active (running)" {
		t.Fatalf("status %+v %v", status, err)
	}
	runner.answers["systemctl --user show *"] = ok("LoadState=loaded\nActiveState=activating\nSubState=auto-restart\nMainPID=0\nExecMainStatus=1")
	status, _ = m.Status(ctx)
	if status.Running || !strings.Contains(status.Detail, "last exit code 1") {
		t.Fatalf("crashed status %+v", status)
	}

	runner.answers["systemctl --user disable --now brokk-town.service"] = fail("Unit brokk-town.service not loaded.")
	if err := m.Uninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.UnitPath()); !os.IsNotExist(err) {
		t.Fatal("unit remains")
	}
	if err := m.Restart(ctx); !errors.Is(err, ErrNotLoaded) {
		t.Fatal(err)
	}
}

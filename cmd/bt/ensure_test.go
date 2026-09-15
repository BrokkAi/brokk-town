package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/daemon"
)

// fakeManager records supervisor calls; Ensure and Restart optionally start
// a fake service so waitReady can observe it.
type fakeManager struct {
	mu      sync.Mutex
	calls   []string
	ensure  func()
	restart func()
	status  daemon.Status
	err     error
}

func (f *fakeManager) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}
func (f *fakeManager) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
func (f *fakeManager) Name() string                                  { return "fake" }
func (f *fakeManager) UnitPath() string                              { return "/fake/unit" }
func (f *fakeManager) Render(daemon.Spec) ([]byte, error)            { return []byte("unit"), nil }
func (f *fakeManager) Stop(context.Context) error                    { f.record("stop"); return f.err }
func (f *fakeManager) Uninstall(context.Context) error               { f.record("uninstall"); return f.err }
func (f *fakeManager) Status(context.Context) (daemon.Status, error) { return f.status, nil }
func (f *fakeManager) Ensure(_ context.Context, spec daemon.Spec) error {
	f.record("ensure " + strings.Join(spec.Command, " "))
	if f.err != nil {
		return f.err
	}
	if f.ensure != nil {
		f.ensure()
	}
	return nil
}
func (f *fakeManager) Restart(context.Context) error {
	f.record("restart")
	if f.err != nil {
		return f.err
	}
	if f.restart != nil {
		f.restart()
	}
	return nil
}

// fakeService is an httptest server that answers the liveness probe and
// records restart requests, with a connection file written like the real one.
type fakeService struct {
	server    *httptest.Server
	restarts  int
	mu        sync.Mutex
	onRestart func()
}

func newFakeService(t *testing.T) *fakeService {
	t.Helper()
	f := &fakeService{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"error":"local access key required"}`, http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/state":
			_, _ = w.Write([]byte(`{}`))
		case "POST /api/restart":
			f.mu.Lock()
			f.restarts++
			hook := f.onRestart
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"restarting":true}`))
			if hook != nil {
				hook()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeService) write(t *testing.T, dir string, conn connection) {
	t.Helper()
	conn.URL = f.server.URL
	conn.Token = "test-key"
	if conn.PID == 0 {
		conn.PID = os.Getpid()
	}
	data, _ := json.Marshal(conn)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func stubLifecycle(t *testing.T, manager *fakeManager, managerErr error, spawn func(base string, demo bool, listen string) error, exe string) *[]string {
	t.Helper()
	warnings := &[]string{}
	oldManager, oldSpawn, oldExe, oldWarn := newManager, spawnService, executablePath, warn
	newManager = func(func(string)) (daemon.Manager, error) {
		if managerErr != nil {
			return nil, managerErr
		}
		return manager, nil
	}
	spawnService = spawn
	executablePath = func() (string, error) { return exe, nil }
	warn = func(m string) { *warnings = append(*warnings, m) }
	t.Cleanup(func() { newManager, spawnService, executablePath, warn = oldManager, oldSpawn, oldExe, oldWarn })
	return warnings
}

func TestEnsureUsesRunningServiceWithoutSideEffects(t *testing.T) {
	base := t.TempDir()
	exe := filepath.Join(base, "bt")
	if err := os.WriteFile(exe, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	service := newFakeService(t)
	started := time.Now().Add(time.Minute) // The binary predates the service.
	service.write(t, base, connection{Version: buildVersion(), Executable: exe, Started: started})
	manager := &fakeManager{}
	spawned := 0
	warnings := stubLifecycle(t, manager, nil, func(string, bool, string) error { spawned++; return nil }, exe)
	conn, err := ensureService(context.Background(), base, false, "127.0.0.1:8099")
	if err != nil || conn.URL != service.server.URL {
		t.Fatalf("%v %+v", err, conn)
	}
	if spawned != 0 || len(manager.Calls()) != 0 || service.restarts != 0 || len(*warnings) != 0 {
		t.Fatalf("running service disturbed: spawned=%d manager=%v restarts=%d warnings=%v", spawned, manager.Calls(), service.restarts, *warnings)
	}
}

func TestEnsureRegistersThenFallsBackToDetachedSpawn(t *testing.T) {
	base := t.TempDir()
	exe := filepath.Join(base, "bt")
	if err := os.WriteFile(exe, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	service := newFakeService(t)
	// A stale connection file from a dead process must not count as running.
	service.write(t, base, connection{PID: deadPID(t), Version: buildVersion()})
	manager := &fakeManager{ensure: func() {
		service.write(t, base, connection{Version: buildVersion(), Executable: exe, Started: time.Now(), Managed: true})
	}}
	spawned := 0
	stubLifecycle(t, manager, nil, func(string, bool, string) error { spawned++; return nil }, exe)
	conn, err := ensureService(context.Background(), base, false, "127.0.0.1:8099")
	if err != nil || !conn.Managed {
		t.Fatalf("%v %+v", err, conn)
	}
	calls := manager.Calls()
	if spawned != 0 || len(calls) != 1 || !strings.HasPrefix(calls[0], "ensure "+exe+" serve --state-dir "+base+" --listen 127.0.0.1:8099") {
		t.Fatalf("registration not used: spawned=%d calls=%v", spawned, calls)
	}
	for _, name := range []string{"serve.log", "serve.err.log"} {
		info, err := os.Stat(filepath.Join(base, "logs", name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("log %s not pre-created owner-only: %v", name, err)
		}
	}

	// Registration failing (SSH-only macOS, no systemd) falls back to a
	// detached spawn with a warning, and never blocks the command.
	os.Remove(filepath.Join(base, "connection.json"))
	failing := &fakeManager{err: errors.New("Bootstrap failed: Domain does not support specified action")}
	warnings := stubLifecycle(t, failing, nil, func(b string, demo bool, listen string) error {
		spawned++
		service.write(t, base, connection{Version: buildVersion(), Executable: exe, Started: time.Now()})
		return nil
	}, exe)
	conn, err = ensureService(context.Background(), base, false, "127.0.0.1:8099")
	if err != nil || conn.Managed || spawned != 1 {
		t.Fatalf("fallback failed: %v %+v spawned=%d", err, conn, spawned)
	}
	if len(*warnings) == 0 || !strings.Contains(strings.Join(*warnings, "\n"), "without login registration") {
		t.Fatalf("fallback not explained: %v", *warnings)
	}

	// Registration off and demo both skip the supervisor entirely.
	os.Remove(filepath.Join(base, "connection.json"))
	if err := setRegistration(base, false); err != nil {
		t.Fatal(err)
	}
	manager = &fakeManager{}
	stubLifecycle(t, manager, nil, func(b string, demo bool, listen string) error {
		spawned++
		service.write(t, base, connection{Version: buildVersion(), Executable: exe, Started: time.Now()})
		return nil
	}, exe)
	if _, err = ensureService(context.Background(), base, false, "127.0.0.1:8099"); err != nil || spawned != 2 || len(manager.Calls()) != 0 {
		t.Fatalf("registration=off ignored: %v spawned=%d calls=%v", err, spawned, manager.Calls())
	}
	if err := setRegistration(base, true); err != nil {
		t.Fatal(err)
	}
	stubLifecycle(t, manager, nil, func(b string, demo bool, listen string) error {
		spawned++
		if !demo {
			t.Error("demo flag lost")
		}
		service.write(t, filepath.Join(base, "demo"), connection{Version: buildVersion(), Started: time.Now()})
		return nil
	}, exe)
	if _, err = ensureService(context.Background(), base, true, "127.0.0.1:8099"); err != nil || spawned != 3 || len(manager.Calls()) != 0 {
		t.Fatalf("demo registered: %v spawned=%d calls=%v", err, spawned, manager.Calls())
	}
}

func TestEnsureRollsStaleServiceForwardButNeverBackward(t *testing.T) {
	base := t.TempDir()
	exe := filepath.Join(base, "bt")
	if err := os.WriteFile(exe, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	service := newFakeService(t)
	stubLifecycle(t, &fakeManager{}, nil, func(string, bool, string) error { t.Error("spawned"); return nil }, exe)

	// Same binary rebuilt after the service started: restart in place.
	service.onRestart = func() {
		service.write(t, base, connection{Version: buildVersion(), Executable: exe, Started: time.Now().Add(time.Second)})
	}
	service.write(t, base, connection{Version: buildVersion(), Executable: exe, Started: time.Now().Add(-time.Hour)})
	conn, err := ensureService(context.Background(), base, false, "127.0.0.1:8099")
	if err != nil || service.restarts != 1 {
		t.Fatalf("rebuilt binary not rolled: %v restarts=%d", err, service.restarts)
	}
	if reason, roll := driftReason(conn); reason != "" || roll {
		t.Fatalf("fresh service still reported stale: %q %v", reason, roll)
	}

	// A service newer than this client is left alone with a notice. Release
	// versions compare; a dev build never triggers a roll in either direction.
	version = "0.0.2"
	t.Cleanup(func() { version = "dev" })
	service.write(t, base, connection{Version: "99.0.0", Executable: exe, Started: time.Now().Add(time.Minute)})
	warnings := stubLifecycle(t, &fakeManager{}, nil, func(string, bool, string) error { t.Error("spawned"); return nil }, exe)
	if _, err = ensureService(context.Background(), base, false, "127.0.0.1:8099"); err != nil || service.restarts != 1 {
		t.Fatalf("newer service downgraded: %v restarts=%d", err, service.restarts)
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "99.0.0") {
		t.Fatalf("notice missing: %v", *warnings)
	}

	// An older release at a different path under a supervisor is re-registered
	// and restarted rather than signalled.
	old := connection{Version: "0.0.1", Executable: "/elsewhere/bt", Started: time.Now().Add(-time.Hour), Managed: true}
	service.write(t, base, old)
	manager := &fakeManager{}
	manager.restart = func() { service.write(t, base, connection{Version: "0.0.2", Executable: exe, Started: time.Now()}) }
	stubLifecycle(t, manager, nil, func(string, bool, string) error { t.Error("spawned"); return nil }, exe)
	conn, err = ensureService(context.Background(), base, false, "127.0.0.1:8099")
	if err != nil || conn.Version != "0.0.2" || service.restarts != 1 {
		t.Fatalf("release roll failed: %v %+v restarts=%d", err, conn, service.restarts)
	}
	calls := manager.Calls()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "ensure "+exe) || calls[1] != "restart" {
		t.Fatalf("supervisor not re-pointed at the new binary: %v", calls)
	}
}

func TestServiceCommandsReportAndControlTheService(t *testing.T) {
	base := t.TempDir()
	exe := filepath.Join(base, "bt")
	if err := os.WriteFile(exe, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	manager := &fakeManager{status: daemon.Status{Installed: true, Loaded: true, Running: true, PID: os.Getpid(), Detail: "running"}}
	stubLifecycle(t, manager, nil, func(string, bool, string) error { return nil }, exe)

	// Nothing running: status explains and exits non-zero.
	if err := run(context.Background(), []string{"service", "status", "--state-dir", base}); err == nil {
		t.Fatal("status succeeded without a service")
	}
	service := newFakeService(t)
	service.write(t, base, connection{Version: "1.2.3", Executable: exe, Started: time.Now(), Managed: true})
	if err := run(context.Background(), []string{"service", "status", "--state-dir", base}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"service", "bogus", "--state-dir", base}); err == nil || !strings.Contains(err.Error(), "unknown service command") {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"service", "on", "--demo", "--state-dir", base}); err == nil {
		t.Fatal("demo registration accepted")
	}
	if err := run(context.Background(), []string{"service", "status", "--state-dir", base, "extra"}); err == nil {
		t.Fatal("extra argument accepted")
	}

	// off: preference persists, the unit is removed, and a managed service is
	// respawned detached so the town stays available.
	respawned := 0
	stubLifecycle(t, manager, nil, func(string, bool, string) error {
		respawned++
		service.write(t, base, connection{Version: "1.2.3", Executable: exe, Started: time.Now().Add(time.Second)})
		return nil
	}, exe)
	if err := run(context.Background(), []string{"service", "off", "--state-dir", base}); err != nil {
		t.Fatal(err)
	}
	if registrationEnabled(base) || respawned != 1 || manager.Calls()[len(manager.Calls())-1] != "uninstall" {
		t.Fatalf("off: enabled=%v respawned=%d calls=%v", registrationEnabled(base), respawned, manager.Calls())
	}

	// on: the unmanaged service is stopped (a dead PID here) and the
	// supervisor takes over.
	service.write(t, base, connection{PID: deadPID(t), Version: "1.2.3", Executable: exe, Started: time.Now()})
	manager.ensure = func() {
		service.write(t, base, connection{Version: "1.2.3", Executable: exe, Started: time.Now().Add(2 * time.Second), Managed: true})
	}
	if err := run(context.Background(), []string{"service", "on", "--state-dir", base}); err != nil {
		t.Fatal(err)
	}
	if !registrationEnabled(base) || !strings.HasPrefix(manager.Calls()[len(manager.Calls())-1], "ensure ") {
		t.Fatalf("on: enabled=%v calls=%v", registrationEnabled(base), manager.Calls())
	}

	// restart on a running service restarts in place through the API.
	service.onRestart = func() {
		service.write(t, base, connection{Version: "1.2.3", Executable: exe, Started: time.Now().Add(3 * time.Second), Managed: true})
	}
	if err := run(context.Background(), []string{"service", "restart", "--state-dir", base}); err != nil || service.restarts != 1 {
		t.Fatalf("restart: %v restarts=%d", err, service.restarts)
	}
}

func TestStopUnloadsASupervisedJobWithoutAConnection(t *testing.T) {
	base := t.TempDir()
	exe := filepath.Join(base, "bt")
	if err := os.WriteFile(exe, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	// A crash-looping registered job has no connection file, yet stop must
	// still unload it; otherwise the supervisor keeps relaunching it.
	manager := &fakeManager{status: daemon.Status{Installed: true, Loaded: true, Detail: "not running; last exit code 1"}}
	stubLifecycle(t, manager, nil, func(string, bool, string) error { t.Error("spawned"); return nil }, exe)
	if err := run(context.Background(), []string{"service", "stop", "--state-dir", base}); err != nil {
		t.Fatal(err)
	}
	if calls := manager.Calls(); len(calls) != 1 || calls[0] != "stop" {
		t.Fatalf("loaded job not unloaded: %v", calls)
	}
	manager.status = daemon.Status{}
	manager.calls = nil
	if err := run(context.Background(), []string{"service", "stop", "--state-dir", base}); err != nil || len(manager.Calls()) != 0 {
		t.Fatalf("nothing to stop: %v %v", err, manager.Calls())
	}
}

func TestListenAddressIsRememberedPerTown(t *testing.T) {
	base := t.TempDir()
	address, err := resolveListen(base, false, defaultListen, false)
	if err != nil || address != defaultListen {
		t.Fatalf("%q %v", address, err)
	}
	// An explicit address is remembered, and a later default never overrides
	// it: a registration must keep the port it was created with.
	if address, err = resolveListen(base, false, "127.0.0.1:0", true); err != nil || address != "127.0.0.1:0" {
		t.Fatalf("%q %v", address, err)
	}
	if address, err = resolveListen(base, false, defaultListen, false); err != nil || address != "127.0.0.1:0" {
		t.Fatalf("remembered address lost: %q %v", address, err)
	}
	if address, err = resolveListen(base, true, defaultListen, false); err != nil || address != defaultListen {
		t.Fatalf("demo shares the real address: %q %v", address, err)
	}
	if address, err = resolveListen(base, true, "127.0.0.1:1", true); err != nil || address != "127.0.0.1:1" {
		t.Fatalf("%q %v", address, err)
	}
	if address, _ = resolveListen(base, false, defaultListen, false); address != "127.0.0.1:0" {
		t.Fatalf("demo address overwrote the real one: %q", address)
	}
	if _, err = resolveListen(base, false, "0.0.0.0:8099", true); err == nil {
		t.Fatal("public address accepted")
	}
	if err := setRegistration(base, false); err != nil || registrationEnabled(base) {
		t.Fatal("registration off not persisted")
	}
	if address, _ = resolveListen(base, false, defaultListen, false); address != "127.0.0.1:0" {
		t.Fatal("registration change dropped the listen address")
	}
}

func TestPreferenceAndTemporaryBinaryRules(t *testing.T) {
	base := t.TempDir()
	if !registrationEnabled(base) {
		t.Fatal("absent preference must mean on")
	}
	if err := os.WriteFile(preferencePath(base), []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}
	if !registrationEnabled(base) {
		t.Fatal("corrupt preference must mean on")
	}
	if !temporaryBinary(filepath.Join(os.TempDir(), "go-build123", "bt")) || temporaryBinary("/usr/local/bin/bt") {
		t.Fatal("temporary binary detection is wrong")
	}
	oldExe := executablePath
	executablePath = func() (string, error) { return filepath.Join(os.TempDir(), "go-build123", "bt"), nil }
	t.Cleanup(func() { executablePath = oldExe })
	if _, err := buildSpec(base, "127.0.0.1:8099"); err == nil {
		t.Fatal("temporary binary registered")
	}
	if err := spawnDetached(base, false, "127.0.0.1:8099"); err == nil {
		t.Fatal("temporary binary spawned")
	}
	if err := stopProcess(context.Background(), base, connection{PID: deadPID(t)}); err != nil {
		t.Fatal(err)
	}
}

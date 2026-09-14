package town

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BrokkAi/acp-go/runner"
)

func writeFakeWorker(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}

const pythonFakeWorker = `#!/usr/bin/env python3
import json, os, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import socket
import socketserver

class UnixHTTPServer(ThreadingHTTPServer):
    address_family = socket.AF_UNIX
    allow_reuse_address = False
    def server_bind(self):
        self.server_path = self.server_address[0]
        self.socket.bind(self.server_path)
        os.chmod(self.server_path, 0o600)
        self.server_name = 'worker'
        self.server_port = 0

class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def log_message(self, *args):
        pass
    def send_json(self, status, value, stream=False):
        data = (json.dumps(value, separators=(',', ':')) + '\n').encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/x-ndjson' if stream else 'application/json')
        self.send_header('X-Brokk-Worker-Protocol', '1')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def do_GET(self):
        if self.path != '/v1/initialize':
            self.send_error(404)
            return
        self.send_json(200, {
            'protocol': 1,
            'minimum_protocol': 1,
            'bot': 'issue-bot',
            'version': '9.8.7',
            'capabilities': ['run', 'progress', 'issue-result', 'exact-issue'],
        })
    def do_POST(self):
        if self.path == '/v1/shutdown':
            self.send_json(202, {'stopping': True})
            self.server.shutdown()
            return
        if self.path != '/v1/runs':
            self.send_error(404)
            return
        length = int(self.headers.get('Content-Length', '0'))
        request = json.loads(self.rfile.read(length))
        capture = os.environ.get('TOWN_WORKER_TEST_CAPTURE')
        if capture:
            with open(capture, 'w') as f: json.dump(request, f)
        events = [
            {'type': 'progress', 'seq': 1, 'progress': {'phase': 'investigating', 'task': 'external protocol fixture'}},
            {'type': 'result', 'seq': 2, 'result': {'issue': {'owned': [{'pr': 7, 'branch': 'town/7', 'issue': 7}]}}},
            {'type': 'complete', 'seq': 3},
        ]
        data = ''.join(json.dumps(event, separators=(',', ':')) + '\n' for event in events).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/x-ndjson')
        self.send_header('X-Brokk-Worker-Protocol', '1')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

if len(sys.argv) == 2 and sys.argv[1] == 'version':
    print('9.8.7')
    sys.exit(0)
if len(sys.argv) != 4 or sys.argv[1] != 'worker' or sys.argv[2] != '--socket':
    print('invalid fake worker arguments', file=sys.stderr)
    sys.exit(64)
socket_path = sys.argv[3]
try: os.unlink(socket_path)
except FileNotFoundError: pass
server = UnixHTTPServer((socket_path, ), Handler)
try: server.serve_forever(poll_interval=.05)
finally:
    server.server_close()
    try: os.unlink(socket_path)
    except FileNotFoundError: pass
`

func TestIssueWorkerUsesVersionedUnixSocketProtocol(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	capture := filepath.Join(dir, "request.json")
	writeFakeWorker(t, fake, pythonFakeWorker)
	t.Setenv("TOWN_WORKER_TEST_CAPTURE", capture)
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Branch = "main"
	x.Config.Verify = []string{"go", "test", "./..."}
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent", "--mode=issue"}, Environment: map[string]string{"PRIVATE": "value"}}
	x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Issue, Stage: "queued"}
	workers := &BotWorkers{Root: dir, Store: store, BotCommands: map[Role]string{Issue: fake}}
	progress := []Progress{}
	result, err := workers.Run(context.Background(), x, Issue, func(p Progress) { progress = append(progress, p) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	owned := result.Owned[7]
	if owned.Branch != "town/7" || owned.Issue != 7 || len(result.Owned) != 1 {
		t.Fatalf("wrong protocol result: %+v", result.Owned)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Protocol int                `json:"protocol"`
		Remote   string             `json:"remote"`
		Branch   string             `json:"branch"`
		Repo     string             `json:"repo"`
		Verify   []string           `json:"verify"`
		Agent    runner.AgentConfig `json:"agent"`
		Issue    int                `json:"issue"`
	}
	if err = json.Unmarshal(data, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Protocol != 1 || sent.Remote != "https://github.com/acme/orchard.git" || sent.Branch != "main" || sent.Repo != "acme/orchard" || sent.Issue != 7 {
		t.Fatalf("wrong worker request identity: %+v", sent)
	}
	if !reflect.DeepEqual(sent.Agent.Command, []string{"fake-agent", "--mode=issue"}) || sent.Agent.Environment["PRIVATE"] != "value" {
		t.Fatalf("wrong worker agent profile: %+v", sent.Agent)
	}
	found := false
	for _, p := range progress {
		if p.Phase == "investigating" && p.Task == "external protocol fixture" {
			found = true
		}
	}
	if !found {
		t.Fatalf("worker progress was not observed: %#v", progress)
	}
}

func TestDefaultWorkersUseExactPinnedNpxPackages(t *testing.T) {
	dir := t.TempDir()
	npx := filepath.Join(dir, "npx")
	capture := filepath.Join(dir, "args")
	writeFakeWorker(t, npx, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TOWN_NPX_CAPTURE\"\nprintf '1.2.3\\n'\n")
	resolvedNpx, err := filepath.EvalSymlinks(npx)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TOWN_NPX_CAPTURE", capture)
	workers := &BotWorkers{}
	for role, packageName := range workerPackageNames {
		packageSpec := packageName + "@" + workerDefaultVersions[role]
		bot, err := workers.externalBot(context.Background(), Config{}, role)
		if err != nil {
			t.Fatalf("resolve %s: %v", role, err)
		}
		if bot.command != resolvedNpx || !reflect.DeepEqual(bot.args, []string{"--yes", packageSpec}) {
			t.Fatalf("%s did not use exact pinned npx package: %+v", role, bot)
		}
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for role, packageName := range workerPackageNames {
		packageSpec := packageName + "@" + workerDefaultVersions[role]
		if !strings.Contains(string(data), "--yes "+packageSpec+" version\n") {
			t.Fatalf("missing pinned invocation for %s: %s", packageSpec, data)
		}
	}
	custom := Config{BotVersions: map[Role]string{Issue: "9.8.7"}}
	bot, err := workers.externalBot(context.Background(), custom, Issue)
	if err != nil || !reflect.DeepEqual(bot.args, []string{"--yes", "@brokkai/issue-bot@9.8.7"}) {
		t.Fatalf("saved bot pin was not used: %+v %v", bot, err)
	}
}

func TestWorkerVersionAndCapabilitiesAreChecked(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	body := strings.Replace(pythonFakeWorker, `['run', 'progress', 'issue-result', 'exact-issue']`, `['run']`, 1)
	writeFakeWorker(t, fake, body)
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent"}}
	x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Issue, Stage: "queued"}
	workers := &BotWorkers{Root: dir, Store: store, BotCommands: map[Role]string{Issue: fake}}
	_, err := workers.Run(context.Background(), x, Issue, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), `does not advertise "progress"`) {
		t.Fatalf("missing capability was accepted: %v", err)
	}
}

func TestWorkerEventSequenceMustBeContiguous(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	body := strings.Replace(pythonFakeWorker, `{'type': 'progress', 'seq': 1,`, `{'type': 'progress', 'seq': 2,`, 1)
	writeFakeWorker(t, fake, body)
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Branch = "main"
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent"}}
	x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Issue, Stage: "queued"}
	workers := &BotWorkers{Root: dir, Store: store, BotCommands: map[Role]string{Issue: fake}}
	_, err := workers.Run(context.Background(), x, Issue, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "event sequence gap") {
		t.Fatalf("event sequence gap was accepted: %v", err)
	}
}

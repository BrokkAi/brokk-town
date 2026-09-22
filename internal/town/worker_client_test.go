package town

import (
	"context"
	"encoding/json"
	"errors"
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
import json, os, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import socket
import socketserver

# Modes (TOWN_WORKER_TEST_MODE):
#   default  finish the run immediately (protocol v1 fixture).
#   hang     protocol v1 without detach: block the run until the release file
#            exists, then finish; honor shutdown only after the run ends.
MODE = os.environ.get('TOWN_WORKER_TEST_MODE', '')
RELEASE = os.environ.get('TOWN_WORKER_TEST_RELEASE', '')
INIT_BLOCK = RELEASE + '.initialize-block' if RELEASE else ''
EVENTS = []
COND = threading.Condition()
RUN_STARTED = threading.Event()
RUN_DONE = threading.Event()

def wait_release():
    while RELEASE and not os.path.exists(RELEASE):
        time.sleep(0.02)

def publish(event):
    with COND:
        EVENTS.append(event)
        COND.notify_all()

def final_events():
    if os.environ.get('TOWN_WORKER_TEST_UNTYPED'):
        return [{'type': 'complete', 'seq': 2}]
    return [
        {'type': 'result', 'seq': 2, 'result': {'issue': {'owned': [{'pr': 7, 'branch': 'town/7', 'issue': 7}]}}},
        {'type': 'complete', 'seq': 3},
    ]

class UnixHTTPServer(ThreadingHTTPServer):
    address_family = socket.AF_UNIX
    allow_reuse_address = False
    daemon_threads = True
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
    def start_stream(self):
        self.send_response(200)
        self.send_header('Content-Type', 'application/x-ndjson')
        self.send_header('X-Brokk-Worker-Protocol', '1')
        self.send_header('Connection', 'close')
        self.end_headers()
    def emit(self, event):
        try:
            self.wfile.write((json.dumps(event, separators=(',', ':')) + '\n').encode())
            self.wfile.flush()
            return True
        except (BrokenPipeError, ConnectionResetError, OSError):
            return False
    def do_GET(self):
        if self.path != '/v1/initialize':
            self.send_error(404)
            return
        if INIT_BLOCK and os.path.exists(INIT_BLOCK):
            open(INIT_BLOCK + '.started', 'a').close()
            while not os.path.exists(INIT_BLOCK + '.release'):
                time.sleep(0.02)
        capabilities = ['run', 'progress', 'issue-result', 'exact-issue']
        if os.environ.get('TOWN_WORKER_TEST_POLICY'):
            capabilities.append('policy')
        self.send_json(200, {
            'protocol': 1,
            'minimum_protocol': 1,
            'bot': 'issue-bot',
            'version': '9.8.7',
            'capabilities': capabilities,
        })
    def do_POST(self):
        if self.path == '/v1/retry':
            length = int(self.headers.get('Content-Length', '0'))
            request = json.loads(self.rfile.read(length))
            journal = os.environ.get('TOWN_WORKER_TEST_JOURNAL')
            if journal:
                with open(journal, 'a') as f: f.write('retry ' + request['state_directory'] + '\n')
            self.send_json(200, {'retry': 'scheduled'})
            return
        if self.path == '/v1/shutdown':
            self.send_json(202, {'stopping': True})
            def stop():
                if RUN_STARTED.is_set():
                    RUN_DONE.wait()
                self.server.shutdown()
            threading.Thread(target=stop, daemon=True).start()
            return
        if self.path != '/v1/runs':
            self.send_error(404)
            return
        length = int(self.headers.get('Content-Length', '0'))
        request = json.loads(self.rfile.read(length))
        capture = os.environ.get('TOWN_WORKER_TEST_CAPTURE')
        if capture and request.get('mode') not in ('jobs','retry-issue'):
            with open(capture, 'w') as f: json.dump(request, f)
        journal = os.environ.get('TOWN_WORKER_TEST_JOURNAL')
        if journal:
            with open(journal, 'a') as f: f.write('run ' + request['state_directory'] + '\n')
        RUN_STARTED.set()
        progress = {'type': 'progress', 'seq': 1, 'progress': {'phase': 'investigating', 'task': 'external protocol fixture'}}
        if MODE == 'hang':
            # A run that has been accepted always completes its bookkeeping,
            # however early the Town client disconnects: a detach-capable bot
            # keeps working and buffering, and every bot must still exit after
            # shutdown. A socket failure while writing headers must not leave
            # the run half-recorded.
            try:
                try:
                    self.start_stream()
                except OSError:
                    pass
                publish(progress)
                self.emit(progress)
                wait_release()
                for event in final_events():
                    publish(event)
                    self.emit(event)
            finally:
                RUN_DONE.set()
            return
        events = [progress] + final_events()
        data = ''.join(json.dumps(event, separators=(',', ':')) + '\n' for event in events).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/x-ndjson')
        self.send_header('X-Brokk-Worker-Protocol', '1')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)
        RUN_DONE.set()

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
	workers := &BotWorkers{Root: dir, Store: store, botCommands: map[Role]string{Issue: fake}}
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

func TestWorkerVersionAndCapabilitiesAreChecked(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	body := strings.Replace(pythonFakeWorker, `['run', 'progress', 'issue-result', 'exact-issue']`, `['run']`, 1)
	writeFakeWorker(t, fake, body)
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent"}}
	x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Issue, Stage: "queued"}
	workers := &BotWorkers{Root: dir, Store: store, botCommands: map[Role]string{Issue: fake}}
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
	workers := &BotWorkers{Root: dir, Store: store, botCommands: map[Role]string{Issue: fake}}
	_, err := workers.Run(context.Background(), x, Issue, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "event sequence gap") {
		t.Fatalf("event sequence gap was accepted: %v", err)
	}
}

func TestReleaseRetryUsesWorkerAPIBeforeRun(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-worker")
	journal := filepath.Join(dir, "journal.txt")
	body := strings.Replace(pythonFakeWorker, `'bot': 'issue-bot'`, `'bot': 'release-bot'`, 1)
	body = strings.Replace(body, `['run', 'progress', 'issue-result', 'exact-issue']`, `['run', 'progress', 'release', 'retry']`, 1)
	writeFakeWorker(t, fake, body)
	t.Setenv("TOWN_WORKER_TEST_JOURNAL", journal)
	t.Setenv("TOWN_WORKER_TEST_UNTYPED", "1")
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent"}}
	x.Workers[Release].RetryRequested = true
	workers := &BotWorkers{Root: dir, Store: store, botCommands: map[Role]string{Release: fake}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := workers.Run(context.Background(), x, Release, func(Progress) {}, logger)
	if err != nil {
		t.Fatal(err)
	}
	_, state := Workspace(dir, x.ID, Release)
	if data, _ := os.ReadFile(journal); !result.Retried || string(data) != "retry "+state+"\nrun "+state+"\n" {
		t.Fatalf("retry did not precede the run in the bot's own workspace: retried=%t journal=%q", result.Retried, data)
	}
	// Without an operator request the worker API is not called.
	if err = os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	x.Workers[Release].RetryRequested = false
	if result, err = workers.Run(context.Background(), x, Release, func(Progress) {}, logger); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(journal); result.Retried || string(data) != "run "+state+"\n" {
		t.Fatalf("unrequested retry: retried=%t journal=%q", result.Retried, data)
	}
	// A pinned release that predates the API reports that plainly instead of
	// running as if the budget had been lifted.
	writeFakeWorker(t, fake, strings.Replace(body, `'release', 'retry'`, `'release'`, 1))
	x.Workers[Release].RetryRequested = true
	if _, err = workers.Run(context.Background(), x, Release, func(Progress) {}, logger); err == nil || !strings.Contains(err.Error(), `does not support retry`) {
		t.Fatalf("missing retry capability was accepted: %v", err)
	}
}

func TestManualMergePolicyDoesNotStartFakeReleaseWorker(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.json")
	t.Setenv("TOWN_WORKER_TEST_CAPTURE", capture)
	store := testStore(t, false)
	town := addTown(t, store)
	town.Config.MergePolicy = "manual"
	workers := &BotWorkers{
		Root: dir, Store: store,
		botCommands: map[Role]string{Release: filepath.Join(dir, "worker-that-must-not-start")},
	}
	_, err := workers.Run(context.Background(), town, Release, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "release-preparation") {
		t.Fatalf("manual release dispatch was accepted: %v", err)
	}
	if _, statErr := os.Stat(capture); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("fake release worker received a request: %v", statErr)
	}
}

// TestWorkerPolicyReachesTheBotOrIsRefused covers both halves of the contract:
// a policy-aware worker receives the operator's selection verbatim, and one
// that never learned to read it is refused rather than run unfiltered.
func TestWorkerPolicyReachesTheBotOrIsRefused(t *testing.T) {
	newTown := func(t *testing.T, dir, fake string) (*Town, *BotWorkers) {
		t.Helper()
		store := testStore(t, false)
		x := addTown(t, store)
		x.Config.Branch = "main"
		x.Config.Verify = []string{"go", "test", "./..."}
		x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent"}}
		x.Config.BotPolicies = map[Role]BotPolicy{Issue: {
			Labels:        []string{"agent-ready"},
			ExcludeLabels: []string{"blocked"},
			Attempts:      2,
			Verify:        []string{"make", "issue-check"},
		}}
		x.Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Issue, Stage: "queued", Labels: []string{"agent-ready"}}
		return x, &BotWorkers{Root: dir, Store: store, botCommands: map[Role]string{Issue: fake}}
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("a policy-aware worker receives it", func(t *testing.T) {
		dir := t.TempDir()
		fake := filepath.Join(dir, "fake-worker")
		capture := filepath.Join(dir, "request.json")
		writeFakeWorker(t, fake, pythonFakeWorker)
		t.Setenv("TOWN_WORKER_TEST_CAPTURE", capture)
		t.Setenv("TOWN_WORKER_TEST_POLICY", "1")
		x, workers := newTown(t, dir, fake)
		if _, err := workers.Run(context.Background(), x, Issue, func(Progress) {}, quiet); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		var sent struct {
			Verify []string   `json:"verify"`
			Policy *BotPolicy `json:"policy"`
		}
		if err := json.Unmarshal(data, &sent); err != nil {
			t.Fatal(err)
		}
		if sent.Policy == nil {
			t.Fatal("the worker request carried no policy")
		}
		if !reflect.DeepEqual(sent.Policy.Labels, []string{"agent-ready"}) || !reflect.DeepEqual(sent.Policy.ExcludeLabels, []string{"blocked"}) || sent.Policy.Attempts != 2 {
			t.Fatalf("policy did not survive the protocol: %+v", sent.Policy)
		}
		// The house's own verification command replaces the town's.
		if !reflect.DeepEqual(sent.Verify, []string{"make", "issue-check"}) {
			t.Fatalf("verify = %v, want the house's own command", sent.Verify)
		}
	})

	t.Run("a worker without the capability is refused", func(t *testing.T) {
		dir := t.TempDir()
		fake := filepath.Join(dir, "fake-worker")
		writeFakeWorker(t, fake, pythonFakeWorker)
		x, workers := newTown(t, dir, fake)
		_, err := workers.Run(context.Background(), x, Issue, func(Progress) {}, quiet)
		if err == nil {
			t.Fatal("an unfiltered run was allowed against a configured policy")
		}
		if !strings.Contains(err.Error(), "does not support work policies") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("a town with no policy still runs on that worker", func(t *testing.T) {
		dir := t.TempDir()
		fake := filepath.Join(dir, "fake-worker")
		writeFakeWorker(t, fake, pythonFakeWorker)
		x, workers := newTown(t, dir, fake)
		x.Config.BotPolicies = nil
		if _, err := workers.Run(context.Background(), x, Issue, func(Progress) {}, quiet); err != nil {
			t.Fatalf("an unconfigured town was refused: %v", err)
		}
	})
}

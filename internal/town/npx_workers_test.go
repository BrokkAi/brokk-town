package town

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This runner deliberately changes latest after the probe. Startup must use the
// exact resolved version, not query latest again. No package registry is used.
func fakeNPX(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	npx := filepath.Join(dir, "npx")
	writeFakeWorker(t, npx, "#!"+python+"\n"+body)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestNPXResolvesLatestThenStartsExactVersion(t *testing.T) {
	dir := t.TempDir()
	// macOS exposes its temporary directory through /var -> /private/var.
	// Exercise the same aliasing on every platform.
	root := filepath.Join(t.TempDir(), "town-root")
	if err := os.Symlink(dir, root); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(dir, "bib")
	writeFakeWorker(t, native, pythonFakeWorker)
	trace := filepath.Join(dir, "trace")
	t.Setenv("FAKE_NPX_NATIVE", native)
	t.Setenv("FAKE_NPX_TRACE", trace)
	fakeNPX(t, `import json, os, sys
with open(os.environ['FAKE_NPX_TRACE'], 'a') as output:
    output.write(json.dumps({'args': sys.argv[1:], 'cwd': os.getcwd()}) + '\n')
assert sys.argv[1].startswith('--prefix='), sys.argv
assert os.path.samefile(sys.argv[1][len('--prefix='):], os.getcwd()), sys.argv
del sys.argv[1]
assert sys.argv[1:4] == ['--yes', '--registry=https://registry.npmjs.org', '--'], sys.argv
if sys.argv[4:] == ['@brokkai/issue-bot@latest', 'version']:
    print('9.8.7')
    sys.exit(0)
assert sys.argv[4:6] == ['@brokkai/issue-bot@9.8.7', 'worker'], sys.argv
os.execv(os.environ['FAKE_NPX_NATIVE'], [os.environ['FAKE_NPX_NATIVE'], *sys.argv[5:]])
`)
	workers := &BotWorkers{Root: root}
	bot, err := workers.externalBot(t.Context(), Issue)
	if err != nil {
		t.Fatal(err)
	}
	p, err := startWorkerProcess(t.Context(), bot)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if p.info.Version != "9.8.7" {
		t.Fatal(p.info)
	}
	workers.pool = map[string]*workerProcess{"acme/repo:issue": p}
	workers.poolContext = t.Context()
	for range 2 {
		resolved, err := workers.workerBot(t.Context(), "acme/repo", Issue)
		if err != nil || resolved.version != bot.version {
			t.Fatalf("running worker identity changed: %+v, %v", resolved, err)
		}
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected package invocations: %s", data)
	}
	wantDir, err := filepath.EvalSymlinks(filepath.Join(root, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		var call struct {
			Args []string `json:"args"`
			CWD  string   `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		if call.CWD != wantDir {
			t.Fatalf("package runner used an unowned directory: %s", line)
		}
	}
}

func TestNPXLatestProbeRejectsInvalidVersionAndHonorsCancellation(t *testing.T) {
	for _, output := range []string{"latest", "9.8.7\nextra", "--evil"} {
		t.Run(output, func(t *testing.T) {
			t.Setenv("FAKE_NPX_VERSION", output)
			fakeNPX(t, "import os\nprint(os.environ['FAKE_NPX_VERSION'])\n")
			workers := &BotWorkers{Root: t.TempDir()}
			if _, err := workers.externalBot(t.Context(), Issue); err == nil || !strings.Contains(err.Error(), "invalid version") {
				t.Fatalf("invalid package version accepted: %v", err)
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		fakeNPX(t, "import time\ntime.sleep(60)\n")
		workers := &BotWorkers{Root: t.TempDir()}
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		if _, err := workers.externalBot(ctx, Issue); err == nil || ctx.Err() == nil {
			t.Fatalf("package resolution ignored cancellation: %v", err)
		}
	})
}

func TestNewerBotRetainsTownV1Contract(t *testing.T) {
	bot := externalBot{role: Issue, version: "9.8.7"}
	for _, test := range []struct {
		name     string
		protocol int
		minimum  int
		valid    bool
	}{
		{"current", 1, 1, true},
		{"adds_v2_keeps_v1", 2, 1, true},
		{"removed_v1", 2, 2, false},
		{"invalid_range", 1, 2, false},
		{"invalid_minimum", 1, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := workerInitialize{Bot: "issue-bot", Version: bot.version, Protocol: test.protocol,
				MinimumProtocol: test.minimum, Capabilities: append([]string{"future-capability"}, workerCapabilities[Issue]...)}
			if err := validateWorkerInitialize(bot, info); (err == nil) != test.valid {
				t.Fatalf("compatibility = %v, valid=%v", err, test.valid)
			}
		})
	}
	// Exercise an actual v1 stream while the same worker advertises a newer API.
	path := filepath.Join(t.TempDir(), "worker")
	body := strings.Replace(pythonFakeWorker, "'protocol': 1,", "'protocol': 2,", 1)
	writeFakeWorker(t, path, body)
	workers := &BotWorkers{botCommands: map[Role]string{Issue: path}}
	resolved, err := workers.externalBot(t.Context(), Issue)
	if err != nil {
		t.Fatal(err)
	}
	p, err := startWorkerProcess(t.Context(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	result, err := p.run(t.Context(), workerRequest{Protocol: 1}, false, time.Now().Add(time.Minute), func(Progress) {}, nil)
	if err != nil || result.Issue == nil || len(result.Issue.Owned) != 1 || result.Issue.Owned[0].Branch != "town/7" {
		t.Fatalf("new bot failed existing v1 request: %+v, %v", result, err)
	}
}

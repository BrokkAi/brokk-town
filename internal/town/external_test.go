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

func writeExecutable(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestIssueWorkerRunsExternalBotAndReadsDurableState(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(dir, "capture.json")
	t.Setenv("TOWN_EXTERNAL_TEST_CAPTURE", capture)
	writeExecutable(t, filepath.Join(bin, "bib"), `#!/bin/sh
set -eu
if [ "$1" = version ]; then echo 9.8.7; exit 0; fi
python3 - "$2" <<'PY'
import json, os, pathlib, sys
cfg = json.load(open(sys.argv[1]))
capture = os.environ["TOWN_EXTERNAL_TEST_CAPTURE"]
with open(capture, "w") as f: json.dump(cfg, f)
state = {
  "format": 1,
  "remote": cfg["remote"],
  "branch": cfg["branch"],
  "directory": cfg["directory"],
  "repo": cfg["github"]["repo"],
  "host": cfg["github"]["host"],
  "jobs": {"7": {"issue": {"number": 7}, "branch": "town/7", "status": "submitted", "url": "https://github.com/"+cfg["github"]["repo"]+"/pull/7"}},
}
pathlib.Path(cfg["state_directory"]).mkdir(parents=True, exist_ok=True)
(pathlib.Path(cfg["state_directory"]) / "state.json").write_text(json.dumps(state))
print('{"level":"INFO","msg":"Investigating one issue"}', file=sys.stderr)
PY
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Branch = "main"
	x.Config.Verify = []string{"go", "test", "./..."}
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent", "--mode=issue"}, Environment: map[string]string{"PRIVATE": "value"}}
	workers := &BotWorkers{Root: dir, Store: store}
	var progress []Progress
	result, err := workers.Run(context.Background(), x, Issue, func(p Progress) { progress = append(progress, p) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	own := result.Owned[7]
	if own.Branch != "town/7" || own.Issue != 7 || len(result.Owned) != 1 {
		t.Fatalf("wrong durable ownership: %+v", result.Owned)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	agent := cfg["agent"].(map[string]any)
	if !reflect.DeepEqual(agent["command"], []any{"fake-agent", "--mode=issue"}) || agent["environment"].(map[string]any)["PRIVATE"] != "value" {
		t.Fatalf("private agent launch was not passed exactly: %#v", agent)
	}
	if cfg["draft"] != false || !reflect.DeepEqual(cfg["verify"], []any{"go", "test", "./..."}) || cfg["poll"] != "5m" {
		t.Fatalf("wrong issue-bot config: %#v", cfg)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "towns", x.ID, "issue", "state", ".town-bot-config-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatal("external bot config was not removed", matches)
	}
	found := false
	for _, p := range progress {
		if p.Phase == "investigating" && strings.Contains(p.Task, "one issue") {
			found = true
		}
	}
	if !found {
		t.Fatal("structured external-bot progress was not observed", progress)
	}
}

func TestExternalBotRevisionCannotChangeDuringDispatch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "bbb")
	writeExecutable(t, path, `#!/bin/sh
if [ "$1" = version ]; then echo 0.2.1; exit 0; fi
echo "# changed after revision check" >> "$0"
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store := testStore(t, false)
	x := addTown(t, store)
	x.Config.Agent = runner.AgentConfig{Command: []string{"fake-agent"}}
	workers := &BotWorkers{Root: dir, Store: store}
	_, err := workers.Run(context.Background(), x, Bug, func(Progress) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "executable changed during dispatch") {
		t.Fatalf("mid-dispatch replacement was not rejected: %v", err)
	}
}

func TestReviewExternalStateRejectsBadRevisionAndPublicationIntent(t *testing.T) {
	dir := t.TempDir()
	base := strings.Repeat("1", 40)
	head := strings.Repeat("2", 40)
	job := reviewExternalJob{
		Key:    reviewRevisionKey("owner/repo", reviewExternalPull{Number: 1, Base: reviewExternalGitRef{Ref: "main", SHA: base}, Head: reviewExternalGitRef{Ref: "branch", SHA: head}}),
		Status: "submitted", Actor: "actor",
		PR:      reviewExternalPull{Number: 1, Base: reviewExternalGitRef{Ref: "main", SHA: base}, Head: reviewExternalGitRef{Ref: "branch", SHA: head}},
		Payload: &reviewExternalPayload{Commit: head, Event: "COMMENT", Body: "<!-- review-bot:v1:" + reviewRevisionKey("owner/repo", reviewExternalPull{Number: 1, Base: reviewExternalGitRef{Ref: "main", SHA: base}, Head: reviewExternalGitRef{Ref: "branch", SHA: head}}) + " -->"},
	}
	state := reviewExternalState{Format: 1, Remote: "remote", Branch: "main", Directory: "work", Repo: "owner/repo", Host: "github.com", Jobs: []reviewExternalJob{job}}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := readReviewExternalState(dir, "remote", "main", "work", "owner/repo")
	if err != nil || len(parsed.Jobs) != 1 {
		t.Fatal(parsed, err)
	}
	job.Key = strings.Repeat("0", 64)
	state.Jobs[0] = job
	data, _ = json.Marshal(state)
	if err = os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readReviewExternalState(dir, "remote", "main", "work", "owner/repo"); err == nil || !strings.Contains(err.Error(), "invalid saved review job") {
		t.Fatalf("bad revision key was accepted: %v", err)
	}
}

package town

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/artifact"
)

// ArtifactRecord links a transcript to a completed worker receipt. Older or
// interrupted transcripts without this provenance are retained, never guessed.
type ArtifactRecord struct {
	Path     string    `json:"path"`
	Role     Role      `json:"role"`
	Task     string    `json:"task,omitempty"`
	Complete bool      `json:"complete"`
	Digest   string    `json:"digest"`
	Bytes    int64     `json:"bytes"`
	Finished time.Time `json:"finished"`
}

func (a ArtifactRecord) valid() bool {
	return ValidAgentRole(a.Role) && artifact.SafeRel(a.Path) && len(a.Digest) == 64 && strings.Trim(a.Digest, "0123456789abcdef") == "" && a.Bytes >= 0 && !a.Finished.IsZero()
}

// transcriptRoots are the session directories Town itself writes for a role:
// its repair and audit sessions. A bot's own transcripts live with the bot,
// which reports them in its result.
func transcriptRoots(root, id string, role Role) []string {
	var paths []string
	extension := ""
	if role == Review {
		extension = "audit"
	} else if role == Issue {
		extension = "repair"
	}
	if extension != "" {
		paths = append(paths, filepath.Join(root, "towns", Key(id), "extensions", extension, "sessions"))
	}
	return paths
}

// A nil snapshot means discovery was incomplete. Never infer new transcripts
// by comparing a later scan with an incomplete list of pre-existing evidence.
func transcriptPaths(root, id string, role Role) map[string]bool {
	result := map[string]bool{}
	for _, dir := range transcriptRoots(root, id, role) {
		if _, err := os.Lstat(dir); os.IsNotExist(err) {
			continue
		}
		if !artifact.PathSafe(root, dir) {
			return nil
		}
		f, err := os.Open(dir)
		if err != nil {
			return nil
		}
		entries, err := f.ReadDir(10001)
		_ = f.Close()
		if (err != nil && err != io.EOF) || len(entries) > 10000 {
			return nil
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
				result[filepath.Join(dir, entry.Name())] = true
			}
		}
	}
	return result
}

// recordTranscripts keeps retention evidence for the transcripts one run
// wrote: the ones the bot reported, and the ones Town's own sessions added. A
// nil before means Town's own scan was incomplete, so only the bot's report is
// trusted.
func (b *BotWorkers) recordTranscripts(id string, role Role, before map[string]bool, result RunResult, complete bool, log *slog.Logger) {
	if b.Store == nil {
		return
	}
	task := result.JudgedTask
	if task == "" && result.PR > 0 {
		task = fmt.Sprintf("pr:%d", result.PR)
	}
	if task == "" && result.Issue > 0 {
		task = fmt.Sprintf("issue:%d", result.Issue)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	records := map[string]ArtifactRecord{}
	base := filepath.Join(b.Root, "towns", Key(id))
	var after map[string]bool
	if before != nil {
		after = transcriptPaths(b.Root, id, role)
	}
	for _, t := range result.transcripts {
		// A bot names its transcripts relative to its workspace, which Town
		// lays out as <role>/ in its own view of the repository's storage.
		rel := filepath.Join(string(role), filepath.FromSlash(t.Path))
		if !artifact.SafeRel(filepath.FromSlash(t.Path)) || filepath.Dir(rel) != filepath.Join(string(role), "state", "sessions") || !strings.HasSuffix(rel, ".jsonl") || len(t.Digest) != 64 || strings.Trim(t.Digest, "0123456789abcdef") != "" || t.Bytes < 0 {
			continue
		}
		records[Key(rel)] = ArtifactRecord{Path: rel, Role: role, Task: task, Complete: complete, Digest: t.Digest, Bytes: t.Bytes, Finished: time.Now().UTC()}
	}
	for path := range after {
		if before[path] {
			continue
		}
		rel, err := filepath.Rel(base, path)
		if err != nil || !artifact.SafeRel(rel) {
			continue
		}
		digest, size, err := artifact.Digest(ctx, path)
		if err != nil {
			continue
		}
		records[Key(rel)] = ArtifactRecord{Path: rel, Role: role, Task: task, Complete: complete, Digest: digest, Bytes: size, Finished: time.Now().UTC()}
	}
	if len(records) == 0 {
		return
	}
	if err := b.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil {
			return errors.New("repository missing")
		}
		if t.Artifacts == nil {
			t.Artifacts = map[string]ArtifactRecord{}
		}
		for key, a := range records {
			t.Artifacts[key] = a
		}
		return nil
	}); err != nil {
		log.Warn("Could not record transcript retention evidence; those artifacts will be retained")
	}
}

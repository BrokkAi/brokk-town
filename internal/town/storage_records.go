package town

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
	return ValidAgentRole(a.Role) && safeArtifactPath(a.Path) && len(a.Digest) == 64 && strings.Trim(a.Digest, "0123456789abcdef") == "" && a.Bytes >= 0 && !a.Finished.IsZero()
}
func safeArtifactPath(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator)) && !strings.ContainsAny(path, "\x00\r\n")
}
func transcriptRoots(root, id string, role Role) []string {
	_, state := Workspace(root, id, role)
	paths := []string{filepath.Join(state, "sessions")}
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
		if !storagePathSafe(root, dir) {
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

const maxTranscriptBytes = 128 << 20

func artifactDigest(ctx context.Context, path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, errors.New("artifact is not a regular file")
	}
	if info.Size() > maxTranscriptBytes {
		return "", 0, errors.New("artifact exceeds the verification limit")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	hash := sha256.New()
	var total int64
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		n, err := f.Read(buffer)
		total += int64(n)
		if total > maxTranscriptBytes {
			return "", 0, errors.New("artifact exceeds the verification limit")
		}
		_, _ = hash.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(info, after) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", 0, errors.New("artifact changed during inspection")
	}
	return hex.EncodeToString(hash.Sum(nil)), total, nil
}
func (b *BotWorkers) recordTranscripts(id string, role Role, before map[string]bool, result RunResult, complete bool, log *slog.Logger) {
	if b.Store == nil || before == nil {
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
	after := transcriptPaths(b.Root, id, role)
	if after == nil {
		return
	}
	for path := range after {
		if before[path] {
			continue
		}
		rel, err := filepath.Rel(base, path)
		if err != nil || !safeArtifactPath(rel) {
			continue
		}
		digest, size, err := artifactDigest(ctx, path)
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
			return errors.New("town missing")
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

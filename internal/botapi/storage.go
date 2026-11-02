package botapi

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/artifact"
	"github.com/BrokkAi/brokk-town/internal/filelock"
)

// maxStorageEntries bounds how many entries one directory listing may report.
const maxStorageEntries = 10000

func (w Worker) Storage(ctx context.Context, request StorageRequest) (StorageResult, error) {
	if request.Role != w.Role {
		return StorageResult{}, errors.New(w.Name + " does not keep that unit's storage")
	}
	dir, state, err := Dirs(w.Root, request.Town, request.Role)
	if err != nil {
		return StorageResult{}, err
	}
	base := filepath.Dir(dir)
	if len(request.Remove) > 0 {
		return StorageResult{Removed: w.remove(ctx, base, state, request.Remove)}, nil
	}
	return w.inventory(ctx, base, request.Verify), nil
}

// inventory lists a workspace the way the service's storage view shows it:
// the checkout and other top-level entries as workspace data, each entry of
// the state directory as worker state, and each session transcript on its own.
func (w Worker) inventory(ctx context.Context, base string, verify []string) StorageResult {
	out := StorageResult{Entries: []StorageEntry{}}
	wanted := map[string]bool{}
	for _, path := range verify {
		wanted[path] = true
	}
	add := func(path, kind string) {
		if ctx.Err() != nil || len(out.Entries) >= maxStorageEntries {
			out.Incomplete = true
			return
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return
		}
		entry := StorageEntry{Path: filepath.ToSlash(rel), Kind: kind}
		if !artifact.PathSafe(w.Root, path) {
			entry.Unsafe = true
		} else if entry.Bytes, entry.Files, entry.Modified, err = artifact.Measure(ctx, path); err != nil {
			entry.Unreadable = true
			out.Incomplete = true
		} else if kind == "transcript" && wanted[entry.Path] {
			if digest, size, e := artifact.Digest(ctx, path); e == nil && size == entry.Bytes {
				entry.Digest = digest
			}
		}
		out.Entries = append(out.Entries, entry)
	}
	read := func(path string) []os.DirEntry {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return nil
		}
		if !artifact.PathSafe(w.Root, path) {
			out.Incomplete = true
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			out.Incomplete = true
			return nil
		}
		entries, err := f.ReadDir(maxStorageEntries + 1)
		_ = f.Close()
		if err == io.EOF {
			err = nil
		}
		if err != nil {
			out.Incomplete = true
			return nil
		}
		if len(entries) > maxStorageEntries {
			entries = entries[:maxStorageEntries]
			out.Incomplete = true
		}
		return entries
	}
	for _, entry := range read(base) {
		path := filepath.Join(base, entry.Name())
		if entry.Name() != "state" || !entry.IsDir() {
			add(path, "workspace")
			continue
		}
		for _, child := range read(path) {
			p := filepath.Join(path, child.Name())
			if child.Name() != "sessions" || !child.IsDir() {
				add(p, "worker state")
				continue
			}
			for _, transcript := range read(p) {
				add(filepath.Join(p, transcript.Name()), "transcript")
			}
		}
	}
	if ctx.Err() != nil {
		out.Incomplete = true
	}
	return out
}

// remove deletes transcripts that still match what the service saw. It holds
// the workspace's state lock throughout, which every bot also holds while it
// runs, so a transcript is never removed from under a session writing it.
func (w Worker) remove(ctx context.Context, base, state string, transcripts []Transcript) []StorageRemoval {
	results := make([]StorageRemoval, 0, len(transcripts))
	refuse := func(detail string) []StorageRemoval {
		for _, t := range transcripts {
			results = append(results, StorageRemoval{Path: t.Path, Status: "retained", Detail: detail})
		}
		return results
	}
	if !artifact.PathSafe(w.Root, state) {
		return refuse("The unit's state directory is missing or unsafe; cleanup refused.")
	}
	lock, err := os.OpenFile(filepath.Join(state, "daemon.lock"), os.O_CREATE|os.O_RDWR|artifact.NoFollow, 0600)
	if err != nil {
		return refuse("Cannot lock the unit's state; cleanup refused.")
	}
	defer lock.Close()
	if err = filelock.TryLock(lock); err != nil {
		return refuse("The unit is working in this workspace; cleanup refused.")
	}
	defer filelock.Unlock(lock)
	for _, t := range transcripts {
		result := StorageRemoval{Path: t.Path, Status: "retained", Detail: "Artifact changed or cleanup was refused."}
		rel := filepath.FromSlash(t.Path)
		path := filepath.Join(base, rel)
		switch {
		case ctx.Err() != nil:
			result.Detail = "Cleanup canceled; artifact retained."
		case !artifact.SafeRel(rel) || filepath.Dir(rel) != filepath.Join("state", "sessions") || !strings.HasSuffix(rel, ".jsonl"):
			result.Detail = "Only session transcripts can be removed."
		case !artifact.PathSafe(w.Root, path):
		default:
			digest, size, e := artifact.Digest(ctx, path)
			if e != nil || digest != t.Digest || size != t.Bytes {
				break
			}
			if e = os.Remove(path); e != nil {
				result.Status, result.Detail = "uncertain", "Cleanup could not finish; refresh the inventory to inspect the outcome."
				break
			}
			result.Status, result.Detail = "removed", ""
		}
		results = append(results, result)
	}
	return results
}

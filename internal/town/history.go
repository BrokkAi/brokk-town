package town

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const historyDays = 30
const maxHistoryBatch = 500
const maxHistoryObjectBytes = 8 << 20
const maxHistoryIndexBytes = 64 << 20

// The small index is loaded once, outside normal state snapshots. Objects are
// immutable; record -> index -> hot-state removal makes every crash prefix safe.
type historyEntry struct {
	ID      string    `json:"id"`
	Stage   string    `json:"stage"`
	Updated time.Time `json:"updated"`
	Digest  string    `json:"digest"`
	Bytes   int64     `json:"bytes"`
}
type historyIndex struct {
	Format  int                     `json:"format"`
	Town    string                  `json:"town"`
	Entries map[string]historyEntry `json:"entries"`
}
type historyObject struct {
	Town string `json:"town"`
	Task *Task  `json:"task"`
}

func historyTerminal(task *Task) bool {
	return task != nil && (task.Stage == "closed" || task.Stage == "merged" || task.Stage == "shipped")
}
func historyIdentity(id string) bool {
	kind, value, ok := strings.Cut(id, ":")
	if !ok {
		return false
	}
	if kind == "commit" {
		return SHA(value)
	}
	n, err := strconv.Atoi(value)
	return (kind == "issue" || kind == "pr") && err == nil && n > 0 && value == strconv.Itoa(n)
}
func (e historyEntry) valid() bool {
	return historyIdentity(e.ID) && historyTerminal(&Task{Stage: e.Stage}) && !e.Updated.IsZero() && len(e.Digest) == 64 && strings.Trim(e.Digest, "0123456789abcdef") == "" && e.Bytes > 0 && e.Bytes <= maxHistoryObjectBytes
}
func (e historyEntry) filename() string  { return Key(e.ID) + "-" + e.Digest + ".json" }
func historyRoot(root, id string) string { return filepath.Join(root, "towns", Key(id), "history") }
func historyHash(data []byte) string     { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func readHistoryFile(root, path string, limit int64) ([]byte, error) {
	if !storagePathSafe(root, path) {
		return nil, errors.New("unsafe or missing history path")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid history file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("history file exceeds its bound")
	}
	return data, nil
}
func historyDirectory(root, id string) (string, error) {
	path := root
	for _, part := range []string{"towns", Key(id), "history", "objects"} {
		path = filepath.Join(path, part)
		err := os.Mkdir(path, 0700)
		if err != nil && !os.IsExist(err) {
			return "", err
		}
		if !storagePathSafe(root, path) {
			return "", errors.New("unsafe history directory")
		}
		if err == nil {
			// Persist each newly created directory entry before hot state can
			// point at objects beneath it, including on the first archival.
			parent, openErr := os.Open(filepath.Dir(path))
			if openErr != nil {
				return "", openErr
			}
			syncErr := parent.Sync()
			closeErr := parent.Close()
			if err := errors.Join(syncErr, closeErr); err != nil {
				return "", err
			}
		}
	}
	return filepath.Dir(path), nil
}
func writeHistoryFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) loadHistory() error {
	s.history = map[string]map[string]historyEntry{}
	if s.state.Demo {
		return nil
	}
	root := filepath.Dir(s.path)
	for id, t := range s.state.Towns {
		path := filepath.Join(historyRoot(root, id), "index.json")
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			if t.HistoryCreated {
				return errors.New("saved task history is missing; restore the complete private directory")
			}
			continue
		}
		data, err := readHistoryFile(root, path, maxHistoryIndexBytes)
		if err != nil {
			return errors.New("cannot read saved task history index")
		}
		var index historyIndex
		if json.Unmarshal(data, &index) != nil || index.Format != 1 || index.Town != id || index.Entries == nil {
			return errors.New("invalid task history index")
		}
		count := 0
		for key, e := range index.Entries {
			if key != e.ID || !e.valid() {
				return errors.New("invalid task history identity")
			}
			if t.Tasks[key] == nil {
				count++
			}
		}
		if count != t.ArchivedTasks {
			return errors.New("task history does not match the saved snapshot; restore a consistent backup")
		}
		s.history[id] = index.Entries
	}
	return nil
}
func historyHold(t *Town) bool {
	for role, w := range t.Workers {
		// Repository inventory itself owns this maintenance step. Other
		// workers can still reference tasks before recording a dispatch handle.
		if role != Repo && (w.Run != nil || w.Agent != nil || w.Status == "working" || w.Status == "pausing") {
			return true
		}
	}
	for _, i := range t.Intents {
		if i != nil && i.Status != "confirmed" {
			return true
		}
	}
	for _, i := range t.FunnelIntents {
		if i != nil && i.Status != IntentConfirmed && i.Status != IntentRejected {
			return true
		}
	}
	for _, r := range t.Requests {
		if r.Status == "queued" || r.Status == "uncertain" {
			return true
		}
	}
	return false
}
func historyCandidate(t *Town, task *Task, now time.Time, index map[string]historyEntry) bool {
	if !historyTerminal(task) || !historyIdentity(task.ID) || task.Source != nil || task.Updated.IsZero() || now.Sub(task.Updated) < historyDays*24*time.Hour || task.Blocked || task.BranchKept || len(task.FollowUps) > 0 || task.Requeue > 0 {
		return false
	}
	if task.IssueJob != nil && task.IssueJob.ClaimPending {
		return false
	}
	for _, w := range t.Workers {
		if w.Recovery != nil && (w.Recovery.TaskID == task.ID || w.Recovery.TaskID == "") {
			return false
		}
		if run := w.Run; run != nil && ((task.Kind == "issue" && run.Issue == task.Number) || (task.Kind == "pr" && run.PR == task.Number)) {
			return false
		}
	}
	if task.Kind == "issue" {
		for n, owned := range t.Owned {
			key := fmt.Sprintf("pr:%d", n)
			if owned.Issue == task.Number && !historyTerminal(t.Tasks[key]) && (t.Tasks[key] != nil || index[key].ID == "") {
				return false
			}
		}
	}
	return true
}

// ArchiveCompleted is called on the repository worker path, never by snapshot
// rendering. Every selected task is rechecked against its exact saved bytes.
func (s *Store) ArchiveCompleted(ctx context.Context, id string, now time.Time) error {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	state := s.Snapshot()
	t := state.Towns[id]
	if state.Demo || t == nil || t.Deleted || s.storageHeld(id) || historyHold(t) {
		return nil
	}
	s.mu.RLock()
	index := map[string]historyEntry{}
	for key, e := range s.history[id] {
		index[key] = e
	}
	s.mu.RUnlock()
	tasks := []*Task{}
	for _, task := range t.Tasks {
		if historyCandidate(t, task, now, index) {
			tasks = append(tasks, task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Updated.Equal(tasks[j].Updated) {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].Updated.Before(tasks[j].Updated)
	})
	if len(tasks) > maxHistoryBatch {
		tasks = tasks[:maxHistoryBatch]
	}
	if len(tasks) == 0 {
		return nil
	}
	root := filepath.Dir(s.path)
	dir, err := historyDirectory(root, id)
	if err != nil {
		return err
	}
	selected := map[string]historyEntry{}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := json.Marshal(historyObject{Town: id, Task: task})
		if err != nil {
			return err
		}
		if len(data) > maxHistoryObjectBytes {
			continue
		}
		e := historyEntry{ID: task.ID, Stage: task.Stage, Updated: task.Updated, Digest: historyHash(data), Bytes: int64(len(data))}
		if err = writeHistoryFile(filepath.Join(dir, "objects", e.filename()), data); err != nil {
			return err
		}
		index[task.ID] = e
		selected[task.ID] = e
	}
	if len(selected) == 0 {
		return nil
	}
	data, err := json.Marshal(historyIndex{Format: 1, Town: id, Entries: index})
	if err != nil {
		return err
	}
	if len(data) > maxHistoryIndexBytes {
		return errors.New("task history index reached its size limit; hot tasks retained")
	}
	if err = writeHistoryFile(filepath.Join(dir, "index.json"), data); err != nil {
		return err
	}
	s.mu.Lock()
	if s.history == nil {
		s.history = map[string]map[string]historyEntry{}
	}
	s.history[id] = index
	s.mu.Unlock()
	return s.Update(func(st *State) error {
		current := st.Towns[id]
		if current == nil || current.Deleted || historyHold(current) {
			return nil
		}
		for key, e := range selected {
			task := current.Tasks[key]
			if !historyCandidate(current, task, now, index) {
				continue
			}
			data, err := json.Marshal(historyObject{Town: id, Task: task})
			if err != nil {
				return err
			}
			if historyHash(data) == e.Digest {
				delete(current.Tasks, key)
			}
		}
		count := 0
		for key := range index {
			if current.Tasks[key] == nil {
				count++
			}
		}
		current.ArchivedTasks = count
		current.HistoryCreated = true
		// Older Town versions must refuse this state rather than forget cold
		// identities and interpret a reopened archived task as brand-new work.
		st.Format = 2
		return nil
	})
}
func (s *Store) readHistoryObject(ctx context.Context, id string, e historyEntry) (*Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := readHistoryFile(filepath.Dir(s.path), filepath.Join(historyRoot(filepath.Dir(s.path), id), "objects", e.filename()), maxHistoryObjectBytes)
	if err != nil || int64(len(data)) != e.Bytes || historyHash(data) != e.Digest {
		return nil, errors.New("task history evidence is missing or changed; restore it before continuing")
	}
	var object historyObject
	if json.Unmarshal(data, &object) != nil || object.Town != id || object.Task == nil || object.Task.ID != e.ID || object.Task.Stage != e.Stage || !object.Task.Updated.Equal(e.Updated) {
		return nil, errors.New("task history identity mismatch")
	}
	return object.Task, ctx.Err()
}

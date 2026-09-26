package town

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// StorageArtifact is an inspection receipt. Paths are relative to this town's
// private directory; cleanup accepts its opaque identity, never an input path.
type StorageArtifact struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	Kind     string    `json:"kind"`
	Role     Role      `json:"role"`
	Task     string    `json:"task,omitempty"`
	Bytes    int64     `json:"bytes"`
	Files    int       `json:"files"`
	Modified time.Time `json:"modified"`
	Eligible bool      `json:"eligible"`
	Reason   string    `json:"reason"`
	head     string
	digest   string
}
type StorageUsage struct {
	Bytes     int64 `json:"bytes"`
	Files     int   `json:"files"`
	Artifacts int   `json:"artifacts"`
}
type StorageInventory struct {
	Town            string                `json:"town"`
	At              time.Time             `json:"at"`
	MinimumAgeHours int                   `json:"minimum_age_hours"`
	Roles           map[Role]StorageUsage `json:"roles"`
	Artifacts       []StorageArtifact     `json:"artifacts"`
	Incomplete      bool                  `json:"incomplete"`
	CleanupHold     string                `json:"cleanup_hold,omitempty"`
}

func storagePathSafe(root, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || !safeArtifactPath(rel) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(pathAbs)
	if err != nil {
		return false
	}
	base, err := filepath.EvalSymlinks(rootAbs)
	return err == nil && filepath.Clean(resolved) == filepath.Join(base, rel)
}
func (s *Store) storageHeld(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.storageBusy[id]
}
func storageHold(t *Town) string {
	for _, r := range t.Requests {
		if r.Status == "queued" || r.Status == "uncertain" {
			return "Pending or uncertain issue submissions retain this town’s artifacts."
		}
	}
	for _, w := range t.Workers {
		if w.Recovery != nil {
			return "Resolve interrupted worker outcomes before cleanup."
		}
		if w.Enabled || w.Run != nil || w.Agent != nil || w.Status == "working" || w.Status == "pausing" {
			return "Pause all town workers and wait for active work to finish before cleanup."
		}
	}
	for _, i := range t.Intents {
		if i != nil && i.Status != "confirmed" {
			return "Pending or uncertain write intents retain this town's artifacts."
		}
	}
	for _, i := range t.FunnelIntents {
		if i != nil && i.Status != "confirmed" && i.Status != "rejected" {
			return "Unresolved source write intents retain this town's artifacts."
		}
	}
	return ""
}
func terminalStorageTask(t *Task) bool {
	if t == nil {
		return false
	}
	switch t.Stage {
	case "closed", "merged", "shipped", "complete", "implemented", "declined":
		return true
	}
	return false
}
func storageMeasure(ctx context.Context, path string) (size int64, files int, modified time.Time, err error) {
	visited := 0
	err = filepath.WalkDir(path, func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 200000 {
			return errors.New("inventory entry limit")
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.ModTime().After(modified) {
			modified = info.ModTime()
		}
		if !entry.IsDir() {
			size += info.Size()
			files++
		}
		return nil
	})
	return
}
func (s *Supervisor) Storage(ctx context.Context, id string, ageHours int) (StorageInventory, error) {
	id = strings.ToLower(id)
	if ageHours < 0 || ageHours > 24*3650 {
		return StorageInventory{}, errors.New("minimum_age_hours must be between 0 and 87600")
	}
	t := s.Store.Snapshot().Towns[id]
	if t == nil {
		return StorageInventory{}, errors.New("unknown town")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return storageInventory(ctx, filepath.Dir(s.Store.path), t, ageHours), nil
}
func storageInventory(ctx context.Context, root string, t *Town, ageHours int) StorageInventory {
	out := StorageInventory{Town: t.ID, At: time.Now().UTC(), MinimumAgeHours: ageHours, Roles: map[Role]StorageUsage{}, Artifacts: []StorageArtifact{}, CleanupHold: storageHold(t)}
	base := filepath.Join(root, "towns", Key(t.ID))
	add := func(path, kind string, role Role) {
		if ctx.Err() != nil || len(out.Artifacts) >= 10000 {
			out.Incomplete = true
			return
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return
		}
		a := StorageArtifact{Path: rel, Kind: kind, Role: role, Reason: "Worker-owned data or unmapped evidence; retained."}
		if !storagePathSafe(root, path) {
			a.Reason = "Symlink or unsafe path; retained."
		} else {
			a.Bytes, a.Files, a.Modified, err = storageMeasure(ctx, path)
			if err != nil {
				out.Incomplete = true
				a.Reason = "Inventory incomplete or timed out; retained."
			} else if kind == "transcript" {
				record, known := t.Artifacts[Key(rel)]
				if known {
					a.Task = record.Task
				}
				switch {
				case !known || !record.Complete:
					a.Reason = "No completed run receipt; retained."
				case record.Task == "" || !terminalStorageTask(t.Tasks[record.Task]):
					a.Reason = "Task is unfinished or has no retained completion identity."
				case out.At.Sub(record.Finished) < time.Duration(ageHours)*time.Hour:
					a.Reason = "Younger than the selected retention period."
				default:
					digest, size, e := artifactDigest(ctx, path)
					if e != nil || digest != record.Digest || size != record.Bytes {
						a.Reason = "Transcript changed or cannot be verified; retained."
					} else {
						a.digest = digest
						a.Eligible = true
						a.Reason = "Completed task and unchanged transcript receipt."
					}
				}
			} else if kind == "repair worktree" {
				a.Task, a.head, a.Reason = repairRetention(ctx, t, path, filepath.Join(filepath.Dir(path), "repository.git"))
				a.Eligible = a.Reason == ""
				if a.Eligible {
					a.Reason = "Reconciled repair at its exact saved commit; working tree is clean."
				}
				if a.Eligible && out.At.Sub(a.Modified) < time.Duration(ageHours)*time.Hour {
					a.Eligible = false
					a.Reason = "Younger than the selected retention period."
				}
			}
		}
		if out.CleanupHold != "" && a.Eligible {
			a.Eligible = false
			a.Reason = out.CleanupHold
		}
		a.ID = Key(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", rel, a.Modified.UTC().Format(time.RFC3339Nano), a.Bytes, a.head, a.digest))
		usage := out.Roles[role]
		usage.Bytes += a.Bytes
		usage.Files += a.Files
		usage.Artifacts++
		out.Roles[role] = usage
		out.Artifacts = append(out.Artifacts, a)
	}
	read := func(path string) []os.DirEntry {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return nil
		}
		if !storagePathSafe(root, path) {
			out.Incomplete = true
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			out.Incomplete = true
			return nil
		}
		entries, err := f.ReadDir(10001)
		_ = f.Close()
		if err == io.EOF {
			err = nil
		}
		if len(entries) > 10000 {
			entries = entries[:10000]
			out.Incomplete = true
		}
		if err != nil {
			out.Incomplete = true
			return nil
		}
		return entries
	}
	sessions := func(dir string, role Role) {
		for _, entry := range read(dir) {
			add(filepath.Join(dir, entry.Name()), "transcript", role)
		}
	}
	for _, role := range AgentRoles {
		dir := filepath.Join(base, string(role))
		for _, entry := range read(dir) {
			path := filepath.Join(dir, entry.Name())
			if entry.Name() == "state" && entry.IsDir() {
				for _, child := range read(path) {
					p := filepath.Join(path, child.Name())
					if child.Name() == "sessions" && child.IsDir() {
						sessions(p, role)
					} else {
						add(p, "worker state", role)
					}
				}
			} else {
				add(path, "workspace", role)
			}
		}
	}
	for _, extension := range []struct {
		name string
		role Role
	}{{"repair", Issue}, {"audit", Review}} {
		dir := filepath.Join(base, "extensions", extension.name)
		for _, entry := range read(dir) {
			path := filepath.Join(dir, entry.Name())
			kind := "extension data"
			if entry.Name() == "sessions" && entry.IsDir() {
				sessions(path, extension.role)
				continue
			}
			if extension.name == "repair" && strings.HasPrefix(entry.Name(), "work-") {
				kind = "repair worktree"
			}
			add(path, kind, extension.role)
		}
	}
	if ctx.Err() != nil || out.Incomplete {
		out.Incomplete = true
		for i := range out.Artifacts {
			out.Artifacts[i].Eligible = false
			out.Artifacts[i].Reason = "Inventory incomplete; no artifacts can be removed."
		}
	}
	sort.Slice(out.Artifacts, func(i, j int) bool { return out.Artifacts[i].Path < out.Artifacts[j].Path })
	return out
}

// An orphan is not evidence of completion. Check ownership, the exact saved
// commit and all tracked/untracked/ignored files without contacting a remote.
func repairRetention(ctx context.Context, t *Town, path, repository string) (task, head, reason string) {
	var intent *Intent
	for _, i := range t.Intents {
		if i != nil && i.Kind == "repair" && resolvedPath(i.Directory) == resolvedPath(path) {
			intent = i
			break
		}
	}
	if intent == nil {
		return "", "", "No durable repair receipt; retained."
	}
	task = fmt.Sprintf("pr:%d", intent.PR)
	if intent.Status != "confirmed" {
		return task, "", "Pending or uncertain repair; saved worktree retained."
	}
	if !SHA(intent.NewHead) {
		return task, "", "Repair receipt has no exact saved commit."
	}
	if filepath.Dir(path) != filepath.Dir(repository) || !strings.HasPrefix(filepath.Base(path), "work-") {
		return task, "", "Worktree is outside the owned repair directory."
	}
	common, err := git(ctx, path, "rev-parse", "--git-common-dir")
	if err != nil {
		return task, "", "Cannot verify worktree ownership."
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(path, common)
	}
	if resolvedPath(common) != resolvedPath(repository) {
		return task, "", "Worktree belongs to another repository."
	}
	branch, err := git(ctx, path, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != repairBranch(path) {
		return task, "", "Worktree is not on its saved private repair branch."
	}
	head, err = git(ctx, path, "rev-parse", "HEAD")
	if err != nil || head != intent.NewHead {
		return task, head, "Worktree revision changed since the repair receipt."
	}
	status, err := git(ctx, path, "status", "--porcelain", "--untracked-files=all", "--ignored")
	if err != nil || status != "" {
		return task, head, "Worktree has unfinished files or cannot be inspected."
	}
	return task, head, ""
}

// Cooperative bot state locks cover standalone workers as well as Town jobs.
// Scheduler reservations and the store's runtime flag prevent a new Town job
// or a concurrent cleanup from entering after the quiescence check.
func (s *Supervisor) beginStorage(id string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.running {
		if strings.HasPrefix(key, id+":") {
			return nil, errors.New("town still has active work")
		}
	}
	for key, busy := range s.retrying {
		if busy && strings.HasPrefix(key, id+":") {
			return nil, errors.New("town still has an active retry")
		}
	}
	s.Store.mu.Lock()
	defer s.Store.mu.Unlock()
	t := s.Store.state.Towns[id]
	if t == nil {
		return nil, errors.New("unknown town")
	}
	if s.Store.storageBusy[id] {
		return nil, errors.New("storage cleanup is already running")
	}
	if reason := storageHold(t); reason != "" {
		return nil, errors.New(reason)
	}
	if s.Store.storageBusy == nil {
		s.Store.storageBusy = map[string]bool{}
	}
	s.Store.storageBusy[id] = true
	return func() { s.Store.mu.Lock(); delete(s.Store.storageBusy, id); s.Store.mu.Unlock(); s.notifyScheduler() }, nil
}
func lockStorage(root, id string) (func(), error) {
	var locked []*os.File
	release := func() {
		for _, f := range locked {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}
	}
	for _, role := range AgentRoles {
		_, dir := Workspace(root, id, role)
		if _, err := os.Lstat(dir); os.IsNotExist(err) {
			continue
		}
		if !storagePathSafe(root, dir) {
			release()
			return nil, errors.New("unsafe worker state directory; cleanup refused")
		}
		f, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			release()
			return nil, errors.New("cannot lock worker state; cleanup refused")
		}
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			release()
			return nil, errors.New("an independent worker owns its state; cleanup refused")
		}
		locked = append(locked, f)
	}
	return release, nil
}

type StorageRemoval struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (s *Supervisor) CleanupStorage(ctx context.Context, id string, ageHours int, ids []string) ([]StorageRemoval, error) {
	id = strings.ToLower(id)
	if len(ids) == 0 || len(ids) > 128 {
		return nil, errors.New("select between 1 and 128 artifact IDs from a fresh storage inventory")
	}
	if ageHours < 0 || ageHours > 24*3650 {
		return nil, errors.New("invalid retention period")
	}
	if s.Store.Snapshot().Demo {
		return nil, errors.New("demo storage cleanup is disabled")
	}
	release, err := s.beginStorage(id)
	if err != nil {
		return nil, err
	}
	defer release()
	root := filepath.Dir(s.Store.path)
	unlock, err := lockStorage(root, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t := s.Store.Snapshot().Towns[id]
	inventory := storageInventory(ctx, root, t, ageHours)
	selected := map[string]StorageArtifact{}
	for _, a := range inventory.Artifacts {
		selected[a.ID] = a
	}
	seen := map[string]bool{}
	for _, id := range ids {
		a, exists := selected[id]
		if !exists || !a.Eligible || seen[id] {
			return nil, errors.New("selection changed or contains retained artifacts; refresh the inventory")
		}
		seen[id] = true
	}
	results := []StorageRemoval{}
	for _, id := range ids {
		a := selected[id]
		path := filepath.Join(root, "towns", Key(t.ID), a.Path)
		result := StorageRemoval{ID: id, Status: "retained", Detail: "Artifact changed or cleanup was refused."}
		if ctx.Err() != nil {
			result.Detail = "Cleanup canceled; artifact retained."
			results = append(results, result)
			continue
		}
		if !storagePathSafe(root, path) {
			results = append(results, result)
			continue
		}
		switch a.Kind {
		case "transcript":
			digest, size, e := artifactDigest(ctx, path)
			if e != nil || digest != a.digest || size != a.Bytes {
				results = append(results, result)
				continue
			}
			err = os.Remove(path)
		case "repair worktree":
			repository := filepath.Join(filepath.Dir(path), "repository.git")
			_, head, reason := repairRetention(ctx, t, path, repository)
			if reason != "" || head != a.head {
				results = append(results, result)
				continue
			}
			_, err = git(ctx, "", "--git-dir", repository, "worktree", "remove", path)
			if err == nil {
				_, err = git(ctx, "", "--git-dir", repository, "update-ref", "-d", "refs/heads/"+repairBranch(path), head)
			}
		default:
			results = append(results, result)
			continue
		}
		if err != nil {
			result.Detail = "Cleanup could not finish; refresh the inventory to inspect the outcome."
		} else {
			result.Status, result.Detail = "removed", "Artifact removed; task and write identities retained."
			if e := s.Store.Update(func(st *State) error { delete(st.Towns[t.ID].Artifacts, Key(a.Path)); return nil }); e != nil {
				result.Detail = "Artifact removed; retention receipt update could not be saved."
				results = append(results, result)
				break
			}
		}
		results = append(results, result)
	}
	return results, nil
}

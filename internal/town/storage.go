package town

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/artifact"
	"github.com/BrokkAi/brokk-town/internal/botapi"
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
	AgeHours int64     `json:"age_hours"`
	Eligible bool      `json:"eligible"`
	Reason   string    `json:"reason"`
	head     string
	digest   string
	// botPath names a bot-owned artifact within its unit's workspace; the
	// bot, not Town, measures and removes it.
	botPath string
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

func (s *Store) storageHeld(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.storageBusy[id]
}
func storageHold(t *Town) string {
	for _, r := range t.Requests {
		if r.Status == "queued" || r.Status == "uncertain" {
			return "Pending or uncertain issue submissions retain this precinct’s artifacts."
		}
	}
	for _, w := range t.Workers {
		if w.Recovery != nil {
			return "Resolve interrupted worker outcomes before cleanup."
		}
		if w.Enabled || w.Run != nil || w.Agent != nil || w.Status == "working" || w.Status == "pausing" {
			return "Stand down every unit in this precinct and wait for active work to finish before cleanup."
		}
	}
	for _, i := range t.Intents {
		if i != nil && i.Status != "confirmed" {
			return "Pending or uncertain write intents retain this precinct's artifacts."
		}
	}
	for _, i := range t.FunnelIntents {
		if i != nil && i.Status != "confirmed" && i.Status != "rejected" {
			return "Unresolved source write intents retain this precinct's artifacts."
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
func (s *Supervisor) Storage(ctx context.Context, id string, ageHours int) (StorageInventory, error) {
	id = strings.ToLower(id)
	if ageHours < 0 || ageHours > 24*3650 {
		return StorageInventory{}, errors.New("minimum_age_hours must be between 0 and 87600")
	}
	t := s.Store.Snapshot().Towns[id]
	if t == nil {
		return StorageInventory{}, errors.New("unknown precinct")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	s.Store.historyForStorage(t)
	return storageInventory(ctx, filepath.Dir(s.Store.path), t, ageHours, s.botStorage()), nil
}

// botStorer reaches each unit's bot for the data it keeps.
type botStorer interface {
	BotStorage(context.Context, Role, botapi.StorageRequest) (botapi.StorageResult, error)
}

// botStorage returns the bots' storage, or nil when the configured workers
// have no bots of their own.
func (s *Supervisor) botStorage() botStorer {
	if b, ok := s.Workers.(botStorer); ok {
		return b
	}
	return nil
}

// BotStorage asks one unit's bot about the data it keeps for a precinct.
func (b *BotWorkers) BotStorage(ctx context.Context, role Role, request botapi.StorageRequest) (botapi.StorageResult, error) {
	bot := b.Bots[role]
	if bot == nil {
		return botapi.StorageResult{}, fmt.Errorf("no %s is configured", workerBotNames[role])
	}
	request.Role = string(role)
	return bot.Storage(ctx, request)
}
func storageInventory(ctx context.Context, root string, t *Town, ageHours int, bots botStorer) StorageInventory {
	out := StorageInventory{Town: t.ID, At: time.Now().UTC(), MinimumAgeHours: ageHours, Roles: map[Role]StorageUsage{}, Artifacts: []StorageArtifact{}, CleanupHold: storageHold(t)}
	base := filepath.Join(root, "towns", Key(t.ID))
	// retained explains why a transcript's receipt keeps it, or returns "" when
	// only verifying its content remains.
	retained := func(record ArtifactRecord) string {
		switch {
		case !record.Complete:
			return "No completed run receipt; retained."
		case record.Task == "" || !terminalStorageTask(t.Tasks[record.Task]):
			return "Task is unfinished or has no retained completion identity."
		case out.At.Sub(record.Finished) < time.Duration(ageHours)*time.Hour:
			return "Younger than the selected retention period."
		}
		return ""
	}
	// transcript decides one transcript from its receipt; verify is consulted
	// only for a transcript that could otherwise be removed.
	transcript := func(a *StorageArtifact, verify func() (string, int64, error)) {
		record, known := t.Artifacts[Key(a.Path)]
		if known {
			a.Task = record.Task
		}
		if !known {
			a.Reason = "No completed run receipt; retained."
		} else if a.Reason = retained(record); a.Reason == "" {
			digest, size, e := verify()
			if e != nil || digest != record.Digest || size != record.Bytes {
				a.Reason = "Transcript changed or cannot be verified; retained."
			} else {
				a.digest = digest
				a.Eligible = true
				a.Reason = "Completed task and unchanged transcript receipt."
			}
		}
	}
	finish := func(a StorageArtifact) {
		if !a.Modified.IsZero() {
			a.AgeHours = max(0, int64(out.At.Sub(a.Modified)/time.Hour))
		}
		if out.CleanupHold != "" && a.Eligible {
			a.Eligible = false
			a.Reason = out.CleanupHold
		}
		a.ID = Key(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", a.Path, a.Modified.UTC().Format(time.RFC3339Nano), a.Bytes, a.head, a.digest))
		usage := out.Roles[a.Role]
		usage.Bytes += a.Bytes
		usage.Files += a.Files
		usage.Artifacts++
		out.Roles[a.Role] = usage
		out.Artifacts = append(out.Artifacts, a)
	}
	// add measures one artifact Town itself owns.
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
		if !artifact.PathSafe(root, path) {
			a.Reason = "Symlink or unsafe path; retained."
		} else {
			a.Bytes, a.Files, a.Modified, err = artifact.Measure(ctx, path)
			if err != nil {
				out.Incomplete = true
				a.Reason = "Inventory incomplete or timed out; retained."
			} else if kind == "transcript" {
				transcript(&a, func() (string, int64, error) { return artifact.Digest(ctx, path) })
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
		finish(a)
	}
	// Each unit's bot reports what it keeps. Town asks it to verify only the
	// transcripts whose receipts would otherwise let them go.
	for _, role := range AgentRoles {
		if bots == nil {
			break
		}
		prefix := string(role) + string(filepath.Separator)
		var verify []string
		for _, record := range t.Artifacts {
			if record.Role == role && strings.HasPrefix(record.Path, prefix) && retained(record) == "" {
				verify = append(verify, filepath.ToSlash(strings.TrimPrefix(record.Path, prefix)))
			}
		}
		sort.Strings(verify)
		kept, err := bots.BotStorage(ctx, role, botapi.StorageRequest{Town: Key(t.ID), Verify: verify})
		if err != nil || kept.Incomplete {
			out.Incomplete = true
		}
		for _, entry := range kept.Entries {
			local := filepath.FromSlash(entry.Path)
			if len(out.Artifacts) >= 10000 || !artifact.SafeRel(local) || (entry.Kind != "workspace" && entry.Kind != "worker state" && entry.Kind != "transcript") || entry.Bytes < 0 || entry.Files < 0 {
				out.Incomplete = true
				continue
			}
			a := StorageArtifact{Path: filepath.Join(string(role), local), Kind: entry.Kind, Role: role, Reason: "Worker-owned data or unmapped evidence; retained.", botPath: entry.Path}
			switch {
			case entry.Unsafe:
				a.Reason = "Symlink or unsafe path; retained."
			case entry.Unreadable:
				out.Incomplete = true
				a.Reason = "Inventory incomplete or timed out; retained."
			default:
				a.Bytes, a.Files, a.Modified = entry.Bytes, entry.Files, entry.Modified
				if entry.Kind == "transcript" {
					transcript(&a, func() (string, int64, error) {
						if entry.Digest == "" {
							return "", 0, errors.New("the unit could not verify this transcript")
						}
						return entry.Digest, entry.Bytes, nil
					})
				}
			}
			finish(a)
		}
	}
	read := func(path string) []os.DirEntry {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return nil
		}
		if !artifact.PathSafe(root, path) {
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
	if path := filepath.Join(base, "history"); artifact.PathSafe(root, path) {
		add(path, "task history (retained)", Hall)
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

// Scheduler reservations and the store's runtime flag prevent a new Town job
// or a concurrent cleanup from entering after the quiescence check. Each bot
// holds its own state lock while it removes its transcripts, so a bot running
// outside Town's scheduling still refuses.
func (s *Supervisor) beginStorage(id string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.running {
		if strings.HasPrefix(key, id+":") {
			return nil, errors.New("precinct still has active work")
		}
	}
	for key, busy := range s.retrying {
		if busy && strings.HasPrefix(key, id+":") {
			return nil, errors.New("precinct still has an active retry")
		}
	}
	s.Store.mu.Lock()
	defer s.Store.mu.Unlock()
	t := s.Store.state.Towns[id]
	if t == nil {
		return nil, errors.New("unknown precinct")
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

// removeBotTranscripts asks each unit's bot to remove the selected
// transcripts it keeps, and returns its answers by artifact ID.
func (s *Supervisor) removeBotTranscripts(ctx context.Context, t *Town, ids []string, selected map[string]StorageArtifact, bots botStorer) map[string]botapi.StorageRemoval {
	answers := map[string]botapi.StorageRemoval{}
	byRole := map[Role][]string{}
	for _, id := range ids {
		if a := selected[id]; a.botPath != "" {
			byRole[a.Role] = append(byRole[a.Role], id)
		}
	}
	if bots == nil {
		return answers
	}
	for _, role := range AgentRoles {
		chosen := byRole[role]
		if len(chosen) == 0 {
			continue
		}
		request := botapi.StorageRequest{Town: Key(t.ID)}
		byPath := map[string]string{}
		for _, id := range chosen {
			a := selected[id]
			request.Remove = append(request.Remove, botapi.Transcript{Path: a.botPath, Digest: a.digest, Bytes: a.Bytes})
			byPath[a.botPath] = id
		}
		reply, err := bots.BotStorage(ctx, role, request)
		if err != nil {
			continue
		}
		for _, removal := range reply.Removed {
			if id, ok := byPath[removal.Path]; ok {
				answers[id] = removal
			}
		}
	}
	return answers
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t := s.Store.Snapshot().Towns[id]
	s.Store.historyForStorage(t)
	bots := s.botStorage()
	inventory := storageInventory(ctx, root, t, ageHours, bots)
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
	removedByBot := s.removeBotTranscripts(ctx, t, ids, selected, bots)
	results := []StorageRemoval{}
removal:
	for _, id := range ids {
		a := selected[id]
		path := filepath.Join(root, "towns", Key(t.ID), a.Path)
		result := StorageRemoval{ID: id, Status: "retained", Detail: "Artifact changed or cleanup was refused."}
		if a.botPath != "" {
			outcome, answered := removedByBot[id]
			switch {
			case !answered:
				result.Status, result.Detail = "uncertain", "The unit did not confirm the outcome; refresh the inventory to inspect it."
			case outcome.Status == "removed":
				result.Status, result.Detail = "removed", "Artifact removed; task and write identities retained."
				if e := s.Store.Update(func(st *State) error { delete(st.Towns[t.ID].Artifacts, Key(a.Path)); return nil }); e != nil {
					result.Detail = "Artifact removed; retention receipt update could not be saved."
					results = append(results, result)
					break removal
				}
			case outcome.Status == "uncertain":
				result.Status, result.Detail = "uncertain", "Cleanup could not finish; refresh the inventory to inspect the outcome."
			case outcome.Detail != "":
				result.Detail = truncate(outcome.Detail, 512)
			}
			results = append(results, result)
			continue
		}
		if ctx.Err() != nil {
			result.Detail = "Cleanup canceled; artifact retained."
			results = append(results, result)
			continue
		}
		if !artifact.PathSafe(root, path) {
			results = append(results, result)
			continue
		}
		removedWorktree := false
		switch a.Kind {
		case "transcript":
			digest, size, e := artifact.Digest(ctx, path)
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
				removedWorktree = true
				_, err = git(ctx, "", "--git-dir", repository, "update-ref", "-d", "refs/heads/"+repairBranch(path), head)
			}
		default:
			results = append(results, result)
			continue
		}
		if err != nil {
			result.Status = "uncertain"
			result.Detail = "Cleanup could not finish; refresh the inventory to inspect the outcome."
			if removedWorktree {
				result.Status = "partial"
				result.Detail = "Worktree removed; private branch cleanup was not confirmed."
			}
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

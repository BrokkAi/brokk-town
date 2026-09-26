package town

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PrepareHistory loads only reopened/changed terminal identities on a worker
// path. Repeated historical entries remain cold and cannot replay arrivals.
func (s *Store) PrepareHistory(ctx context.Context, id string, remote *RepoSnapshot) (map[string]*Task, error) {
	s.mu.RLock()
	t := s.state.Towns[id]
	index := s.history[id]
	cold := map[string]historyEntry{}
	dependencies := map[int]string{}
	consider := func(key string) {
		if t != nil && t.Tasks[key] == nil {
			if e, ok := index[key]; ok {
				cold[key] = e
			}
		}
	}
	for _, i := range remote.Issues {
		consider(fmt.Sprintf("issue:%d", i.Number))
	}
	for _, p := range remote.Pulls {
		consider(fmt.Sprintf("pr:%d", p.Number))
		consider("commit:" + p.MergeCommit)
		if t != nil && p.State != "closed" && p.MergedAt == nil && t.Owned[p.Number].Issue > 0 {
			key := fmt.Sprintf("issue:%d", t.Owned[p.Number].Issue)
			consider(key)
			dependencies[p.Number] = key
		}
	}
	for _, c := range remote.Commits {
		consider("commit:" + c.SHA)
	}
	s.mu.RUnlock()
	restored := map[string]*Task{}
	remote.Archived = map[string]bool{}
	restore := func(e historyEntry) error {
		task, err := s.readHistoryObject(ctx, id, e)
		if err == nil {
			restored[e.ID] = task
		}
		return err
	}
	issues := remote.Issues[:0]
	for _, i := range remote.Issues {
		key := fmt.Sprintf("issue:%d", i.Number)
		if e, ok := cold[key]; ok {
			if i.State == "closed" {
				remote.Archived[key] = true
				continue
			}
			if err := restore(e); err != nil {
				return nil, err
			}
		}
		issues = append(issues, i)
	}
	remote.Issues = issues
	pulls := remote.Pulls[:0]
	for _, p := range remote.Pulls {
		key := fmt.Sprintf("pr:%d", p.Number)
		if e, ok := cold[dependencies[p.Number]]; ok {
			if err := restore(e); err != nil {
				return nil, err
			}
		}
		if e, ok := cold["commit:"+p.MergeCommit]; ok && e.Stage == "shipped" {
			remote.Archived[e.ID] = true
		}
		if e, ok := cold[key]; ok {
			if (p.MergedAt != nil && e.Stage == "merged") || (p.State == "closed" && p.MergedAt == nil && e.Stage == "closed") {
				remote.Archived[key] = true
				continue
			}
			if err := restore(e); err != nil {
				return nil, err
			}
		}
		pulls = append(pulls, p)
	}
	remote.Pulls = pulls
	for _, c := range remote.Commits {
		if e, ok := cold["commit:"+c.SHA]; ok && e.Stage == "shipped" {
			remote.Archived[e.ID] = true
		}
	}
	return restored, nil
}

// A newly enabled GitHub funnel can encounter a previously archived native
// issue. Restore that identity before source reconciliation can create it.
func (s *Store) prepareFunnelHistory(ctx context.Context, t *Town, config FunnelConfig, page DiscoveryPage) (map[string]*Task, error) {
	remote := RepoSnapshot{}
	if config.Provider == "github" && strings.EqualFold(config.Location["repository"], t.Config.Repo) {
		for _, item := range page.Items {
			if n, err := strconv.Atoi(string(item.Identity.Item)); err == nil && n > 0 {
				remote.Issues = append(remote.Issues, RemoteIssue{Number: n, State: "open"})
			}
		}
	}
	return s.PrepareHistory(ctx, t.ID, &remote)
}

func restoreHistory(t *Town, restored map[string]*Task) error {
	count := 0
	for id, task := range restored {
		if t.Tasks[id] == nil {
			t.Tasks[id] = clone(task)
			count++
		}
	}
	if count > t.ArchivedTasks {
		return errors.New("saved history count changed; refresh before reconciling")
	}
	t.ArchivedTasks -= count
	return nil
}

// historyForStorage adds only the terminal identities needed to validate
// transcript retention, in memory. It is never persisted or sent as hot state.
func (s *Store) historyForStorage(t *Town) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range t.Artifacts {
		if t.Tasks[record.Task] == nil {
			if e, ok := s.history[t.ID][record.Task]; ok {
				t.Tasks[e.ID] = &Task{ID: e.ID, Stage: e.Stage}
			}
		}
	}
}

type HistoryItem struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	URL     string    `json:"url,omitempty"`
	Stage   string    `json:"stage"`
	House   Role      `json:"house"`
	Updated time.Time `json:"updated"`
}
type HistoryPage struct {
	Town          string        `json:"town"`
	RetentionDays int           `json:"retention_days"`
	Total         int           `json:"total"`
	Items         []HistoryItem `json:"items"`
	Next          string        `json:"next,omitempty"`
}

func (s *Store) History(ctx context.Context, id, after string, limit int) (HistoryPage, error) {
	id = strings.ToLower(id)
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return HistoryPage{}, errors.New("history limit must be between 1 and 100")
	}
	var before time.Time
	var beforeID string
	if after != "" {
		stamp, key, ok := strings.Cut(after, "|")
		var err error
		before, err = time.Parse(time.RFC3339Nano, stamp)
		if !ok || err != nil || !historyIdentity(key) {
			return HistoryPage{}, errors.New("invalid history cursor")
		}
		beforeID = key
	}
	s.mu.RLock()
	t := s.state.Towns[id]
	if t == nil || t.Deleted {
		s.mu.RUnlock()
		return HistoryPage{}, errors.New("unknown town")
	}
	entries := []historyEntry{}
	total := 0
	for key, e := range s.history[id] {
		if t.Tasks[key] != nil {
			continue
		}
		total++
		if !before.IsZero() && (e.Updated.After(before) || (e.Updated.Equal(before) && key <= beforeID)) {
			continue
		}
		entries = append(entries, e)
	}
	s.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Updated.Equal(entries[j].Updated) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Updated.After(entries[j].Updated)
	})
	page := HistoryPage{Town: id, RetentionDays: historyDays, Total: total, Items: []HistoryItem{}}
	more := len(entries) > limit
	if more {
		entries = entries[:limit]
	}
	for _, e := range entries {
		task, err := s.readHistoryObject(ctx, id, e)
		if err != nil {
			return HistoryPage{}, err
		}
		page.Items = append(page.Items, HistoryItem{ID: task.ID, Title: task.Title, URL: task.URL, Stage: task.Stage, House: task.House, Updated: task.Updated})
	}
	if more {
		last := entries[len(entries)-1]
		page.Next = last.Updated.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return page, nil
}
func (s *Store) HistoryTask(ctx context.Context, id, key string) (*Task, error) {
	id = strings.ToLower(id)
	s.mu.RLock()
	t := s.state.Towns[id]
	e, exists := s.history[id][key]
	valid := t != nil && !t.Deleted && t.Tasks[key] == nil && exists
	s.mu.RUnlock()
	if !valid {
		return nil, errors.New("unknown archived task")
	}
	return s.readHistoryObject(ctx, id, e)
}

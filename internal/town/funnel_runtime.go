package town

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FunnelRegistry keeps operational adapters private. Public state contains only
// normalized items, capability reports, cursors, and typed outcomes.
type FunnelRegistry map[ProviderID]FunnelAdapter

func (s *Supervisor) funnelAdapter(config FunnelConfig) (FunnelAdapter, error) {
	base := s.Funnels[config.Provider]
	if base == nil {
		return nil, fmt.Errorf("provider %s is not available", config.Provider)
	}
	switch adapter := base.(type) {
	case *GitHubFunnel:
		return NewConfiguredGitHubFunnel(config, adapter.Client), nil
	case *SlackFunnel:
		return NewSlackFunnelWithOptions(SlackFunnelOptions{Config: config, HTTP: adapter.http, Resolve: adapter.resolve, BaseURL: adapter.baseURL, Capabilities: adapter.capabilities, ReadOnly: config.ReadOnly, PageSize: adapter.pageSize, PageLimit: adapter.pageLimit, RequestTimeout: adapter.requestTimeout}), nil
	default:
		return base, nil
	}
}

// TownIntentStore binds the generic lifecycle protocol to one durable town.
type TownIntentStore struct {
	Store  *Store
	TownID string
}

func (s TownIntentStore) SaveWriteIntent(_ context.Context, intent WriteIntent) error {
	return s.Store.Update(func(state *State) error {
		t := state.Towns[s.TownID]
		if t == nil {
			return fmt.Errorf("unknown town")
		}
		if t.FunnelIntents == nil {
			t.FunnelIntents = map[string]*WriteIntent{}
		}
		if t.FunnelIntents[intent.ID] != nil {
			return fmt.Errorf("funnel intent already exists")
		}
		copy := clone(intent)
		t.FunnelIntents[intent.ID] = &copy
		return nil
	})
}
func (s TownIntentStore) GetWriteIntent(_ context.Context, id string) (WriteIntent, error) {
	snapshot := s.Store.Snapshot()
	t := snapshot.Towns[s.TownID]
	if t == nil || t.FunnelIntents[id] == nil {
		return WriteIntent{}, ErrWriteIntentNotFound
	}
	return clone(*t.FunnelIntents[id]), nil
}
func (s TownIntentStore) UpdateWriteIntent(_ context.Context, intent WriteIntent) error {
	return s.Store.Update(func(state *State) error {
		t := state.Towns[s.TownID]
		if t == nil || t.FunnelIntents[intent.ID] == nil {
			return ErrWriteIntentNotFound
		}
		copy := clone(intent)
		t.FunnelIntents[intent.ID] = &copy
		return nil
	})
}

func ReconcileFunnelPage(s *State, t *Town, page DiscoveryPage, now time.Time) error {
	if err := page.Validate(); err != nil {
		return err
	}
	if t.FunnelSyncs == nil {
		t.FunnelSyncs = map[FunnelID]*FunnelSync{}
	}
	t.FunnelSyncs[page.Funnel] = &FunnelSync{Funnel: page.Funnel, Provider: page.Provider, Cursor: page.NextCursor, LastSync: now, Outcome: page.Outcome}
	for i := range page.Items {
		item := clone(page.Items[i])
		item.LastOutcome = page.Outcome
		id := "source:" + item.Identity.Key()
		kind, number := "source", 0
		if item.Identity.Provider == ProviderID("github") {
			for _, config := range t.Config.Funnels {
				if config.ID == item.Identity.Funnel && strings.EqualFold(config.Location["repository"], t.Config.Repo) {
					if parsed, err := strconv.Atoi(string(item.Identity.Item)); err == nil {
						id, kind, number = fmt.Sprintf("issue:%d", parsed), "issue", parsed
					}
				}
			}
		}
		task := t.Tasks[id]
		if task == nil {
			task = &Task{ID: id, Kind: kind, Number: number, Title: item.Title, URL: item.Provenance.URL, Stage: string(item.Status), House: Issue, External: true, Updated: now, Source: &item}
			t.Tasks[id] = task
			if t.Initialized && !item.Status.Terminal() {
				s.Event(t.ID, "delivery", "outside", "issue", id, "Work arrived through "+string(item.Identity.Funnel)+": "+item.Title, now)
			}
		} else {
			task.Title, task.URL, task.Stage, task.Updated, task.Source = item.Title, item.Provenance.URL, string(item.Status), now, &item
		}
		task.Blocked = !page.Complete || item.LastOutcome.Kind == OutcomeAuthentication || item.LastOutcome.Kind == OutcomeRateLimited || item.LastOutcome.Kind == OutcomeIncomplete || item.LastOutcome.Kind == OutcomePartial || item.LastOutcome.Kind == OutcomeUncertain
	}
	return nil
}

func (s *Supervisor) reconcileFunnels(ctx context.Context, townID string) error {
	t := s.Store.Snapshot().Towns[townID]
	if t == nil {
		return fmt.Errorf("unknown town")
	}
	configs := append(FunnelConfigs(nil), t.Config.Funnels...)
	sort.Slice(configs, func(i, j int) bool { return configs[i].ID < configs[j].ID })
	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		adapter, adapterErr := s.funnelAdapter(config)
		if adapterErr != nil {
			return fmt.Errorf("funnel %s: %w", config.ID, adapterErr)
		}
		cursor := Cursor("")
		if sync := t.FunnelSyncs[config.ID]; sync != nil {
			cursor = sync.Cursor
		}
		page, err := adapter.Discover(ctx, DiscoveryRequest{Funnel: config, Cursor: cursor})
		if err != nil && page.Outcome.Kind == "" {
			page = DiscoveryPage{Funnel: config.ID, Provider: config.Provider, Cursor: cursor, Complete: false, Outcome: OutcomeFromError(err), ObservedAt: s.now()}
		}
		restored, historyErr := s.Store.prepareFunnelHistory(ctx, t, config, page)
		if historyErr != nil {
			return historyErr
		}
		if updateErr := s.Store.Update(func(st *State) error {
			current := st.Towns[townID]
			if err := restoreHistory(current, restored); err != nil {
				return err
			}
			return ReconcileFunnelPage(st, current, page, s.now())
		}); updateErr != nil {
			return updateErr
		}
		if err != nil {
			return fmt.Errorf("funnel %s: %w", config.ID, err)
		}
		t = s.Store.Snapshot().Towns[townID]
	}
	return nil
}

// ApplySourceLifecycle exposes only the normalized capability vocabulary. It
// persists the intent before calling the provider and never supplies model or
// UI code with credentials or provider-specific mutation methods.
func (s *Supervisor) ApplySourceLifecycle(ctx context.Context, townID, taskID string, action CapabilityAction) (LifecycleResult, error) {
	snapshot := s.Store.Snapshot()
	t := snapshot.Towns[townID]
	if t == nil {
		return LifecycleResult{}, fmt.Errorf("unknown town")
	}
	task := t.Tasks[taskID]
	if task == nil || task.Source == nil {
		return LifecycleResult{}, fmt.Errorf("task is not funnel-backed")
	}
	var config *FunnelConfig
	for i := range t.Config.Funnels {
		if t.Config.Funnels[i].ID == task.Source.Identity.Funnel {
			copy := clone(t.Config.Funnels[i])
			config = &copy
			break
		}
	}
	if config == nil {
		return LifecycleResult{}, fmt.Errorf("source funnel is no longer configured")
	}
	if err := task.Source.Capabilities.Allows(action); err != nil {
		return LifecycleResult{Outcome: OutcomeFromError(err)}, err
	}
	adapter, err := s.funnelAdapter(*config)
	if err != nil {
		return LifecycleResult{}, err
	}
	request := LifecycleRequest{Identity: task.Source.Identity, Action: action, ExpectedRevision: task.Source.Provenance.Revision, Cursor: task.Source.Provenance.Cursor, IntentID: Key(task.Source.Identity.Canonical() + ":" + string(action) + ":" + string(task.Source.Provenance.Revision))}
	return ExecuteLifecycle(ctx, TownIntentStore{Store: s.Store, TownID: townID}, adapter, request, s.now())
}

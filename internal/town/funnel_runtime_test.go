package town

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFunnelConfigAndNormalizedTaskPersistWithoutCredentialReference(t *testing.T) {
	store := testStore(t, false)
	config := DefaultConfig("acme/orchard")
	config.Funnels = FunnelConfigs{{ID: "slack-ready", Provider: "slack", Location: SourceLocation{"channel": "C123"}, Credentials: SecretRef{Name: "secret-reference-never-public"}, Enabled: true, PriorityPolicy: "operator-explicit", ReadOnly: true}}
	update(t, store, func(state *State) {
		if _, err := state.Add(config); err != nil {
			t.Fatal(err)
		}
	})
	now := time.Unix(100, 0).UTC()
	id := WorkIdentity{Funnel: "slack-ready", Provider: "slack", Item: "10.000"}
	item := WorkItem{Identity: id, Title: "Source request", Status: WorkQueued, Eligible: true, Priority: Priority{Value: 7, Policy: "operator-explicit", Reason: "operator rank"}, Provenance: Provenance{Identity: id, URL: "https://app.slack.com/client/T/C/thread", Revision: "r1", Cursor: "c1", ObservedAt: now, ExternalState: "message"}, Capabilities: CapabilitySet{{Action: ActionClaim, State: CapabilityReadOnly, Reason: "read only"}}}
	update(t, store, func(state *State) {
		town := state.Towns["acme/orchard"]
		town.Initialized = true
		if err := ReconcileFunnelPage(state, town, DiscoveryPage{Funnel: "slack-ready", Provider: "slack", Items: []WorkItem{item}, Complete: true, Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, ObservedAt: now}, now); err != nil {
			t.Fatal(err)
		}
	})
	snapshot := store.Snapshot()
	task := snapshot.Towns["acme/orchard"].Tasks["source:"+id.Key()]
	if task == nil || task.Source == nil || task.Source.Provenance.Revision != "r1" || task.Source.Priority.Value != 7 {
		t.Fatalf("normalized source task not retained: %#v", task)
	}
	public, _ := json.Marshal(snapshot.Public())
	if strings.Contains(string(public), "secret-reference-never-public") || strings.Contains(string(public), `"credentials"`) {
		t.Fatalf("public snapshot leaked credential reference: %s", public)
	}
	store.Close()
	reopened, err := Open(filepath.Dir(store.path), false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Snapshot().Towns["acme/orchard"].Tasks[task.ID].Source.Provenance.Cursor != "c1" {
		t.Fatal("restart lost source cursor")
	}
}

func TestIncompleteFunnelPageNeverBecomesCleanQueue(t *testing.T) {
	state := NewState(false)
	town, _ := state.Add(DefaultConfig("acme/orchard"))
	town.Initialized = true
	page := DiscoveryPage{Funnel: "linear", Provider: "linear", Complete: false, Outcome: Outcome{Kind: OutcomeIncomplete, Detail: "cursor expired", Covered: false}, ObservedAt: time.Now()}
	if err := ReconcileFunnelPage(&state, town, page, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sync := town.FunnelSyncs["linear"]; sync == nil || sync.Outcome.Kind != OutcomeIncomplete || sync.Outcome.Complete() {
		t.Fatalf("incomplete empty read was flattened: %#v", sync)
	}
}

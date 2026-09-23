package town

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

// Adding a deleted town again applies the settings it is added with, keeps its
// recovery records and branch, and never resumes automation.
func TestReaddAppliesNewSettingsAndKeepsRecovery(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		town.Initialized = true
		town.Owned[9] = Ownership{Branch: "issue/1", Issue: 1}
		town.Workers[Issue].Enabled = true
		town.Workers[Issue].Next = time.Now().Add(time.Hour)
	})
	sup := NewSupervisor(s, nil, nil)
	if err := sup.Delete(x.ID); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig(x.ID)
	cfg.MergePolicy = "manual"
	cfg.Harness = "custom"
	cfg.Agent.Command = []string{"agent", "--acp"}
	if _, err := sup.Add(cfg); err != nil {
		t.Fatal(err)
	}
	st := s.Snapshot()
	got := st.Towns[x.ID]
	if got.Deleted || got.Config.MergePolicy != "manual" || got.Config.harness() != "custom" || !reflect.DeepEqual(got.Config.Agent.Command, cfg.Agent.Command) {
		t.Fatal("restore ignored the new settings", got.Config)
	}
	if got.Config.Branch != "main" || got.Owned[9].Issue != 1 {
		t.Fatal("restore lost the branch or recovery records", got.Config.Branch, got.Owned)
	}
	for r, w := range got.Workers {
		if w.Enabled != (r == Repo) || !w.Next.IsZero() {
			t.Fatal("restore resumed stale worker state", r, w.Enabled, w.Next)
		}
	}
	if last := st.Events[len(st.Events)-1]; !strings.Contains(last.Title, "restored") {
		t.Fatal("restore was not reported", last.Title)
	}

	// A restored town cannot move onto another branch, and an invalid config
	// leaves the tombstone untouched.
	if err := sup.Delete(x.ID); err != nil {
		t.Fatal(err)
	}
	moved := DefaultConfig(x.ID)
	moved.Branch = "develop"
	if _, err := sup.Add(moved); err == nil || !strings.Contains(err.Error(), "branch") {
		t.Fatal("restored onto another branch", err)
	}
	invalid := DefaultConfig(x.ID)
	invalid.MergePolicy = "never"
	if _, err := sup.Add(invalid); err == nil {
		t.Fatal("restored with invalid settings")
	}
	if after := s.Snapshot().Towns[x.ID]; !after.Deleted || after.Config.MergePolicy != "manual" {
		t.Fatal("failed restore changed the tombstone", after.Deleted, after.Config.MergePolicy)
	}
}

// The operator's add request supplies only a merge policy and agent settings;
// restoring through it keeps everything else the town was configured with.
func TestAddRepoRestoreOverlaysOnlySuppliedSettings(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	funnel := FunnelConfig{ID: "github-ready", Provider: "github", Location: SourceLocation{"repository": "acme/orchard"}, Enabled: true, PriorityPolicy: "operator-explicit"}
	review := BotAgentConfig{Harness: "custom", Agent: runner.AgentConfig{Command: []string{"reviewer"}}}
	update(t, s, func(st *State) {
		c := &st.Towns[x.ID].Config
		c.Harness = "custom"
		c.Agent.Command = []string{"agent"}
		c.MergePolicy = "manual"
		c.Budget = &Budget{Period: "day", MaxAttempts: 3}
		c.BotPolicies = map[Role]BotPolicy{Issue: {Only: 42}}
		c.BotAgents = map[Role]BotAgentConfig{Review: review}
		c.Verify = []string{"make", "check"}
		c.ReviewCloseSeverity = "P1"
		c.Funnels = FunnelConfigs{funnel}
	})
	sup := NewSupervisor(s, nil, nil)
	if err := sup.Delete(x.ID); err != nil {
		t.Fatal(err)
	}
	// No merge policy: the kept one stays. The agent settings apply.
	if _, err := sup.AddRepo(x.ID, "", AgentSettings{Command: ptr([]string{"agent", "--acp"}), Model: ptr("m")}); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot().Towns[x.ID].Config
	if got.MergePolicy != "manual" || got.Agent.Model != "m" || !reflect.DeepEqual(got.Agent.Command, []string{"agent", "--acp"}) {
		t.Fatal("supplied settings were not applied over the kept ones", got.MergePolicy, got.Agent)
	}
	if got.Budget == nil || got.Budget.MaxAttempts != 3 || got.BotPolicies[Issue].Only != 42 || !reflect.DeepEqual(got.BotAgents[Review], review) || !reflect.DeepEqual(got.Verify, []string{"make", "check"}) || got.ReviewCloseSeverity != "P1" || len(got.Funnels) != 1 || got.Funnels[0].ID != funnel.ID || got.Branch != "main" {
		t.Fatal("restore dropped settings the request did not supply", got)
	}
	// A supplied merge policy replaces the kept one.
	if err := sup.Delete(x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sup.AddRepo(x.ID, "all", AgentSettings{}); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().Towns[x.ID].Config; got.MergePolicy != "all" || got.Budget == nil || len(got.Funnels) != 1 {
		t.Fatal("merge policy restore", got.MergePolicy, got.Budget, got.Funnels)
	}
	if _, err := sup.AddRepo(x.ID, "", AgentSettings{}); err == nil || err.Error() != "town already exists" {
		t.Fatal("added a live town again", err)
	}
}

func TestDeleteOfDeletedTownIsUnknown(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	sup := NewSupervisor(s, nil, nil)
	if err := sup.Delete(x.ID); err != nil {
		t.Fatal(err)
	}
	events := len(s.Snapshot().Events)
	if err := sup.Delete(x.ID); err == nil || err.Error() != "unknown town" {
		t.Fatal("deleted a deleted town", err)
	}
	if err := sup.Control(x.ID, "all", "delete", ""); err == nil {
		t.Fatal("control deleted a deleted town")
	}
	if got := len(s.Snapshot().Events); got != events {
		t.Fatal("repeated delete appended events", events, got)
	}
}

package town

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/brokk-town/internal/harness"
)

func TestBotProfilesInheritAndRemainIndependent(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	sup := NewSupervisor(store, nil, nil)
	if err := sup.Settings(x.ID, AgentSettings{Harness: ptr("custom"), Command: ptr([]string{"town-agent", "private-default"}), Model: ptr("default"), Effort: ptr("low")}); err != nil {
		t.Fatal(err)
	}
	for _, role := range AgentRoles {
		if !store.Snapshot().Towns[x.ID].Config.Public().BotAgents[role].Inherited {
			t.Fatal("new bot does not inherit", role)
		}
	}
	for _, role := range []Role{Review, Issue, Release} {
		if err := sup.SettingsForRole(x.ID, role, AgentSettings{Model: ptr(string(role)), Effort: ptr("xhigh")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sup.Settings(x.ID, AgentSettings{Model: ptr("new-default"), Effort: ptr("high")}); err != nil {
		t.Fatal(err)
	}
	cfg := store.Snapshot().Towns[x.ID].Config
	for _, role := range AgentRoles {
		model, effort := string(role), "xhigh"
		if role == Bug || role == Feature {
			model, effort = "new-default", "high"
		}
		effective := cfg.ForRole(role)
		if effective.Agent.Model != model || effective.Agent.Effort != effort || effective.Agent.Command[1] != "private-default" {
			t.Fatal("wrong effective profile", role, effective.Agent)
		}
	}
	// Returned profiles and settings applications cannot mutate the source maps.
	effective := cfg.ForRole(Review)
	effective.Agent.Command[1] = "changed"
	effective.BotAgents[Issue] = BotAgentConfig{}
	if cfg.ForRole(Review).Agent.Command[1] != "private-default" || cfg.ForRole(Issue).Agent.Model != "issue" {
		t.Fatal("ForRole aliases its source")
	}
	if err := sup.SettingsForRole(x.ID, Review, AgentSettings{Inherit: true}); err != nil {
		t.Fatal(err)
	}
	cfg = store.Snapshot().Towns[x.ID].Config
	if _, exists := cfg.BotAgents[Review]; exists || cfg.ForRole(Review).Agent.Model != "new-default" || !cfg.Public().BotAgents[Review].Inherited {
		t.Fatal("reset did not restore inheritance", cfg.Public())
	}
}

func TestBotSettingsResolveAtDispatchAndFreezeActiveRun(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	update(t, store, func(st *State) { st.Towns[x.ID].Workers[Review].Enabled = true })
	stale := store.Snapshot().Towns[x.ID]
	seen, release := make(chan string, 2), make(chan struct{})
	workers := workerFunc(func(_ context.Context, town *Town, role Role, _ func(Progress), _ *slog.Logger) (RunResult, error) {
		if role != Review {
			t.Fatal(role)
		}
		seen <- town.Config.Agent.Model
		<-release
		seen <- town.Config.ForRole(Review).Agent.Model
		return RunResult{}, nil
	})
	sup := NewSupervisor(store, nil, workers)
	if err := sup.SettingsForRole(x.ID, Review, AgentSettings{Harness: ptr("claude"), Model: ptr("review-old"), Effort: ptr("xhigh")}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { sup.execute(context.Background(), stale, Review); close(done) }()
	if <-seen != "review-old" {
		t.Fatal("dispatch used the town default or stale snapshot")
	}
	if err := sup.SettingsForRole(x.ID, Review, AgentSettings{Model: ptr("review-new")}); err != nil {
		t.Fatal(err)
	}
	if err := sup.SettingsForRole(x.ID, Issue, AgentSettings{Model: ptr("issue-new")}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if <-seen != "review-old" {
		t.Fatal("active profile changed")
	}
	<-done
	sup.execute(context.Background(), stale, Review)
	if <-seen != "review-new" || <-seen != "review-new" {
		t.Fatal("next dispatch missed the saved profile")
	}
}

func TestBotProfilePersistencePinningAndPrivacy(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	sup := NewSupervisor(store, nil, nil)
	cfg := DefaultConfig("acme/profiles")
	cfg.BotAgents = map[Role]BotAgentConfig{
		Review:  {Harness: "claude", Agent: runner.AgentConfig{Model: "review-model", Effort: "xhigh", Environment: map[string]string{"TOKEN": "private-profile"}, AuthMethod: "private-login", Mode: "private-mode"}},
		Issue:   {Harness: "codex", Agent: runner.AgentConfig{Model: "issue-model"}},
		Release: {Harness: "custom", Agent: runner.AgentConfig{Command: []string{"private-release-agent", "private-argument"}, Environment: map[string]string{"TOKEN": "private-release"}, Model: "deepseek-flash"}},
	}
	id, err := sup.Add(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg = store.Snapshot().Towns[id].Config
	if cfg.HarnessDefinition == nil || cfg.BotAgents[Review].HarnessDefinition == nil || cfg.BotAgents[Issue].HarnessDefinition == nil {
		t.Fatal("add did not pin all registry profiles")
	}
	latest := clone(cfg.BotAgents[Review].HarnessDefinition)
	update(t, store, func(st *State) {
		a := st.Towns[id].Config.BotAgents[Review]
		a.HarnessDefinition.Version = "0.0.1"
		a.HarnessDefinition.Distribution = harness.Distribution{Npx: &harness.Package{Package: "private-agent@0.0.1", Env: map[string]string{"TOKEN": "private-registry"}}}
	})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	sup = NewSupervisor(store, nil, nil)
	if err := sup.SettingsForRole(id, Review, AgentSettings{Model: ptr("review-next")}); err != nil {
		t.Fatal(err)
	}
	if err := sup.SettingsForRole(id, Release, AgentSettings{Effort: ptr("low")}); err != nil {
		t.Fatal(err)
	}
	cfg = store.Snapshot().Towns[id].Config
	a := cfg.ForRole(Review)
	if a.Agent.Environment["TOKEN"] != "private-profile" || a.Agent.AuthMethod != "private-login" || a.Agent.Mode != "private-mode" || a.HarnessDefinition.Version != "0.0.1" || a.HarnessDefinition.Distribution.Npx.Package != "private-agent@0.0.1" {
		t.Fatal("profile edit lost private configuration or the saved version")
	}
	if cfg.ForRole(Release).Agent.Command[1] != "private-argument" || cfg.ForRole(Release).Agent.Environment["TOKEN"] != "private-release" || cfg.ForRole(Issue).Agent.Model != "issue-model" {
		t.Fatal("profile edit changed another selection")
	}
	public, err := json.Marshal(store.Snapshot().Public())
	if err != nil || strings.Contains(string(public), "private-") || strings.Contains(string(public), "harness_definition") || strings.Contains(string(public), `"command"`) || strings.Contains(string(public), `"environment"`) {
		t.Fatal("private bot settings exposed", string(public), err)
	}
	if cfg.Public().BotAgents[Review].HarnessVersion != "0.0.1" || len(cfg.Public().BotAgents) != len(AgentRoles) {
		t.Fatal("missing public bot selection")
	}
	if err := sup.SettingsForRole(id, Review, AgentSettings{Version: ptr(latest.Version)}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.Snapshot().Towns[id].Config.BotAgents[Review].HarnessDefinition, latest) {
		t.Fatal("explicit profile version update was not pinned")
	}
	if err := sup.SettingsForRole(id, Review, AgentSettings{Harness: ptr("codex")}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.Snapshot().Towns[id].Config.BotAgents[Review].Agent, runner.AgentConfig{}) {
		t.Fatal("authentication or model carried across harnesses")
	}
}

func TestBotProfileValidationIsAtomic(t *testing.T) {
	store := testStore(t, true)
	x := addTown(t, store)
	sup := NewSupervisor(store, nil, nil)
	before := store.Snapshot()
	for _, role := range []Role{Repo, "all", "unknown"} {
		if err := sup.SettingsForRole(x.ID, role, AgentSettings{}); err == nil {
			t.Fatal("accepted invalid role", role)
		}
		if _, err := sup.ChoicesForRole(context.Background(), x.ID, role, AgentSettings{}); err == nil {
			t.Fatal("probed invalid role", role)
		}
		cfg := DefaultConfig("acme/invalid")
		cfg.BotAgents = map[Role]BotAgentConfig{role: {}}
		if cfg.Validate() == nil {
			t.Fatal("persisted invalid profile role", role)
		}
	}
	for _, settings := range []AgentSettings{
		{Inherit: true, Model: ptr("")}, {Inherit: true, Harness: ptr("codex")},
		{Inherit: true, Effort: ptr("")}, {Inherit: true, Version: ptr("")},
		{Inherit: true, Command: ptr([]string{})}, {Harness: ptr("unknown")},
		{Harness: ptr("custom")}, {Model: ptr("invalid\nmodel")},
	} {
		if err := sup.SettingsForRole(x.ID, Issue, settings); err == nil {
			t.Fatal("accepted invalid profile settings", settings)
		}
		if _, err := sup.ChoicesForRole(context.Background(), x.ID, Issue, settings); err == nil {
			t.Fatal("probed invalid profile settings", settings)
		}
	}
	if err := sup.Settings(x.ID, AgentSettings{Inherit: true}); err == nil {
		t.Fatal("town defaults inherited themselves")
	}
	if !reflect.DeepEqual(before, store.Snapshot()) {
		t.Fatal("invalid profile update changed persisted state")
	}
}

func TestRoleChoicesUsePrivateProfileAndCanPreviewInheritance(t *testing.T) {
	store := testStore(t, false)
	x := addTown(t, store)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	update(t, store, func(st *State) {
		st.Towns[x.ID].Config.Agent = runner.AgentConfig{Command: []string{exe, "-test.run=TestChoiceAgentHelper"}, Environment: map[string]string{"BROKK_CHOICE_HELPER": "1"}, Model: "quick"}
	})
	sup := NewSupervisor(store, nil, nil)
	if err := sup.SettingsForRole(x.ID, Review, AgentSettings{Model: ptr("careful")}); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	for _, test := range []struct {
		role     Role
		settings AgentSettings
		effort   string
	}{{Review, AgentSettings{}, "high"}, {Issue, AgentSettings{}, "low"}, {Review, AgentSettings{Inherit: true}, "low"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		choices, err := sup.ChoicesForRole(ctx, x.ID, test.role, test.settings)
		cancel()
		if err != nil || len(choices.Efforts) != 1 || choices.Efforts[0].Value != test.effort {
			t.Fatal(test.role, choices, err)
		}
	}
	if !reflect.DeepEqual(before, store.Snapshot()) {
		t.Fatal("discovery persisted preview settings")
	}
}

func TestNestedCertificationAndRepairUseTheirBotProfiles(t *testing.T) {
	b, town, task, _ := fixtureWorkers(t)
	bin := t.TempDir()
	npx := filepath.Join(bin, "npx")
	if err := os.WriteFile(npx, []byte("unused fake executable"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	town.Config.Agent = runner.AgentConfig{Command: []string{"unused-default-agent"}, Model: "default"}
	town.Config.BotAgents = map[Role]BotAgentConfig{
		Review: {Harness: "custom", Agent: runner.AgentConfig{Command: []string{"unused-review-agent"}, Model: "review-model", Effort: "xhigh"}},
		Issue:  {Harness: "codex-acp", HarnessDefinition: &harness.Entry{ID: "codex-acp", Name: "Fake Codex", Version: "0.0.1", Distribution: harness.Distribution{Npx: &harness.Package{Package: "unused-issue-agent@0.0.1", Env: map[string]string{"REGISTRY": "private-launch"}}}}, Agent: runner.AgentConfig{Model: "issue-model", Effort: "high", Environment: map[string]string{"PROFILE": "private-profile"}}},
	}
	stop := errors.New("fake agent stopped before writes")
	seen := map[string]bool{}
	b.executeAgent = func(_ context.Context, current *Town, _ sessionTree, role string, _ *slog.Logger, _ string) (string, error) {
		want := clone(town.Config.BotAgents[Role(role)].Agent)
		if role == "issue" {
			want.Command = []string{npx, "--yes", "--", "unused-issue-agent@0.0.1"}
			want.Environment["REGISTRY"] = "private-launch"
		}
		if !reflect.DeepEqual(current.Config.Agent, want) {
			t.Fatalf("%s session got the wrong profile: %+v", role, current.Config.Agent)
		}
		seen[role] = true
		return "", stop
	}
	if _, err := b.certify(context.Background(), town, task, nil, slog.Default()); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if _, err := b.Run(context.Background(), town, Issue, func(Progress) {}, slog.Default()); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if !seen["review"] || !seen["issue"] || town.Config.Agent.Model != "default" {
		t.Fatal("missing nested agent or mutated town defaults", seen)
	}
}

func TestDemoBotProfilesNeverLaunchAgents(t *testing.T) {
	store := testStore(t, true)
	x := addTown(t, store)
	sup := NewSupervisor(store, nil, workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		t.Fatal("demo started a worker")
		return RunResult{}, nil
	}))
	t.Setenv("PATH", t.TempDir())
	for _, role := range AgentRoles {
		if err := sup.SettingsForRole(x.ID, role, AgentSettings{Harness: ptr("custom"), Command: ptr([]string{"unavailable-agent"}), Model: ptr(string(role))}); err != nil {
			t.Fatal(err)
		}
		if err := sup.Control(x.ID, role, "start", ""); err != nil {
			t.Fatal(err)
		}
		choices, err := sup.ChoicesForRole(context.Background(), x.ID, role, AgentSettings{})
		if err != nil || len(choices.Models) != 1 || choices.Models[0].Value != "demo-model" {
			t.Fatal(role, choices, err)
		}
	}
	sup.schedule(context.Background())
}

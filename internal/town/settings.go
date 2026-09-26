package town

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	acp "github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/runner"
	"github.com/BrokkAi/acp-go/schema"
	"github.com/BrokkAi/brokk-town/internal/harness"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// Pointer fields distinguish preserving a setting from resetting it to default.
type AgentSettings struct {
	Harness *string   `json:"harness,omitempty"`
	Model   *string   `json:"model,omitempty"`
	Effort  *string   `json:"effort,omitempty"`
	Command *[]string `json:"command,omitempty"`
	Version *string   `json:"version,omitempty"`
	Inherit bool      `json:"inherit,omitempty"`
}

func (a AgentSettings) validateRole(role Role) error {
	if role != "" && !ValidAgentRole(role) {
		return errors.New("agent settings require bug, feature, issue, review, release, simplifier, or repo")
	}
	if a.Inherit {
		if role == "" {
			return errors.New("town defaults cannot inherit agent settings")
		}
		if a.Harness != nil || a.Model != nil || a.Effort != nil || a.Command != nil || a.Version != nil {
			return errors.New("inherit cannot be combined with agent overrides")
		}
	}
	return nil
}

func (c Config) harness() string {
	if c.Harness != "" {
		return harness.Canonical(c.Harness)
	}
	if len(c.Agent.Command) > 0 {
		return "custom"
	}
	return "codex-acp"
}

func validateAgent(c Config) error {
	if c.harness() == "custom" {
		if len(c.Agent.Command) == 0 || strings.TrimSpace(c.Agent.Command[0]) == "" {
			return errors.New("custom harness requires an ACP command")
		}
	} else if !harness.ValidID(c.harness()) {
		return errors.New("invalid ACP registry harness ID")
	}
	if d := c.HarnessDefinition; d != nil {
		if c.harness() != d.ID {
			return errors.New("saved harness definition does not match selection")
		}
		if err := d.Validate(); err != nil {
			return err
		}
	}
	for _, v := range append([]string{c.Agent.Model, c.Agent.Effort}, c.Agent.Command...) {
		if len(v) > 4096 || strings.ContainsAny(v, "\x00\r\n") {
			return errors.New("invalid agent setting")
		}
	}
	return nil
}

func (a AgentSettings) Apply(c Config) (Config, error) {
	if a.Inherit {
		return c, errors.New("inherit requires a specific bot role")
	}
	c = clone(c)
	if a.Harness != nil {
		if harness.Canonical(*a.Harness) != c.harness() {
			// Authentication and mode belong to the selected harness.
			c.Agent = runner.AgentConfig{}
			c.HarnessDefinition = nil
		}
		c.Harness = harness.Canonical(*a.Harness)
	}
	if a.Command != nil {
		if c.harness() != "custom" {
			return c, errors.New("command is only supported for a custom harness")
		}
		c.Agent.Command = append([]string{}, (*a.Command)...)
	}
	if a.Model != nil {
		c.Agent.Model = strings.TrimSpace(*a.Model)
	}
	if a.Effort != nil {
		c.Agent.Effort = strings.TrimSpace(*a.Effort)
	}
	return c, c.Validate()
}

func agentConfig(ctx context.Context, c Config, root string) (runner.AgentConfig, error) {
	a := c.Agent
	if err := requireLocalExecution(c); err != nil {
		return a, err
	}
	if len(a.Command) != 0 {
		return a, nil
	}
	var entry harness.Entry
	if c.HarnessDefinition != nil {
		entry = *c.HarnessDefinition
	} else {
		var err error
		entry, err = harness.New(filepath.Join(root, "harnesses"), false).Lookup(c.harness(), "")
		if err != nil {
			return a, err
		}
	}
	command, env, err := harness.Launch(ctx, root, entry)
	if err != nil {
		return a, err
	}
	a.Command = command
	merged := map[string]string{}
	for k, v := range env {
		merged[k] = v
	}
	for k, v := range a.Environment {
		merged[k] = v
	}
	// After the merge: the derivation already reads through whatever config
	// directory the operator pointed at, so the redirect has to outrank it.
	// BROKK_TOWN_MUSE_PERMISSIONS=keep is the opt-out.
	if harness.Canonical(c.harness()) == "brokkai/muse-acp" {
		if home := museConfigHome(root, merged); home != "" {
			merged["XDG_CONFIG_HOME"] = home
		}
	}
	a.Environment = merged
	return a, nil
}

// Prepare records the selected registry definition without downloading or
// launching anything. Network refreshes cannot change a saved selection.
func (s *Supervisor) Prepare(c Config, settings AgentSettings) (Config, error) {
	cfg, err := s.prepareAgent(c, settings)
	if err != nil {
		return cfg, err
	}
	for role, a := range cfg.BotAgents {
		bot, err := s.prepareAgent(cfg.withAgent(a), AgentSettings{})
		if err != nil {
			return cfg, fmt.Errorf("%s agent: %w", role, err)
		}
		cfg.BotAgents[role] = bot.botAgent()
	}
	return cfg, cfg.Validate()
}

func (s *Supervisor) prepareAgent(c Config, settings AgentSettings) (Config, error) {
	cfg, err := settings.Apply(c)
	if err != nil {
		return cfg, err
	}
	if cfg.harness() == "custom" {
		return cfg, nil
	}
	if cfg.HarnessDefinition != nil && (settings.Version == nil || *settings.Version == cfg.HarnessDefinition.Version) {
		return cfg, nil
	}
	version := ""
	if settings.Version != nil {
		version = *settings.Version
	}
	e, err := s.Harnesses.Lookup(cfg.harness(), version)
	if err != nil {
		return cfg, err
	}
	cfg.HarnessDefinition = &e
	return cfg, cfg.Validate()
}

// Add establishes a town from a complete configuration. A deleted town is
// restored under that configuration.
func (s *Supervisor) Add(c Config) (string, error) {
	return s.add(c.Repo, func(*Town) (Config, error) { return s.Prepare(c, AgentSettings{}) })
}

// AddRepo is the operator's add request: only a non-empty merge policy and the
// agent settings are supplied. A new town starts from the defaults; a deleted
// town is restored with those settings applied over the ones it kept, so its
// budget, work policies, bot profiles and funnels survive.
func (s *Supervisor) AddRepo(repo, mergePolicy string, settings AgentSettings) (string, error) {
	return s.add(repo, func(existing *Town) (Config, error) {
		cfg := DefaultConfig(repo)
		if existing != nil {
			cfg = clone(existing.Config)
		}
		if mergePolicy != "" {
			cfg.MergePolicy = mergePolicy
		}
		return s.Prepare(cfg, settings)
	})
}

func (s *Supervisor) add(repo string, prepare func(existing *Town) (Config, error)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strings.ToLower(repo)
	for key := range s.running {
		if strings.HasPrefix(key, id+":") {
			return "", errors.New("town workers are still stopping; try again shortly")
		}
	}
	err := s.Store.Update(func(st *State) error {
		if st.Demo {
			return errors.New("use a live service to add real repositories")
		}
		existing := st.Towns[id]
		if existing != nil && !existing.Deleted {
			return errors.New("town already exists")
		}
		cfg, err := prepare(existing)
		if err != nil {
			return err
		}
		t, err := st.Add(cfg)
		if err != nil {
			return err
		}
		title := "Town established; reporter is checking the repository"
		if existing != nil {
			title = "Town restored with the new settings; recovery records retained"
		}
		st.Event(t.ID, "town", "operator", "repo", "", title, s.now())
		return nil
	})
	return id, err
}

func (s *Supervisor) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		t.Deleted = true
		for _, w := range t.Workers {
			w.Enabled, w.Status = false, "paused"
		}
		for _, r := range t.Requests {
			if r.Status == "queued" {
				r.Status = "canceled"
				r.Detail = "Submission canceled when the town was deleted."
			}
		}
		st.Event(id, "town", "operator", "hall", "", "Town deleted; recovery records retained", s.now())
		return nil
	}); err != nil {
		return err
	}
	for key, cancel := range s.running {
		if strings.HasPrefix(key, id+":") {
			cancel()
		}
	}
	return nil
}

func (s *Supervisor) Settings(id string, settings AgentSettings) error {
	return s.SettingsForRole(id, "", settings)
}

// SettingsForRole changes one independent bot profile, or the town defaults when
// role is empty. Inherit removes an override so future defaults apply again.
func (s *Supervisor) SettingsForRole(id string, role Role, settings AgentSettings) error {
	return s.SettingsForRoleAndPolicy(id, role, settings, nil, nil, nil)
}

// BudgetEdit distinguishes leaving the budget alone from clearing it: a nil
// *BudgetEdit preserves the saved budget, and one holding a nil Budget
// removes it.
type BudgetEdit struct {
	Budget *Budget `json:"budget"`
}

// QuietHoursEdit distinguishes leaving a town's quiet hours alone from
// changing them: a nil *QuietHoursEdit preserves them. Inside it, nil Windows
// makes the town follow the service default again, and an empty list opts the
// town out of quiet hours altogether.
type QuietHoursEdit struct {
	Windows *[]QuietWindow `json:"windows"`
}

// PolicyEdit distinguishes leaving a house's work policy alone from clearing
// it: a nil *PolicyEdit preserves the saved policy, and one holding a nil or
// empty Policy removes it.
type PolicyEdit struct {
	Policy *BotPolicy `json:"policy"`
}

// TownSettings are the town-wide edits one submission may carry alongside an
// agent profile. A nil field leaves that setting as it was saved.
type TownSettings struct {
	MergePolicy    *string         `json:"merge_policy,omitempty"`
	SimplifierMode *string         `json:"simplifier_mode,omitempty"`
	CloseSeverity  *string         `json:"review_close_severity,omitempty"`
	Budget         *BudgetEdit     `json:"budget,omitempty"`
	QuietHours     *QuietHoursEdit `json:"quiet_hours,omitempty"`
	// WorkPolicy edits the policy of the role this submission names. It needs
	// a role: a work policy always belongs to one house.
	WorkPolicy *PolicyEdit `json:"work_policy,omitempty"`
}

// SettingsForRoleAndPolicy commits agent and merge-policy edits together so a
// form submission cannot leave only half of the requested settings applied.
func (s *Supervisor) SettingsForRoleAndPolicy(id string, role Role, settings AgentSettings, mergePolicy *string, simplifierMode *string, closeSeverity *string) error {
	return s.ApplySettings(id, role, settings, TownSettings{MergePolicy: mergePolicy, SimplifierMode: simplifierMode, CloseSeverity: closeSeverity})
}

// ApplySettings commits one agent profile edit and any town-wide edits in a
// single transaction.
func (s *Supervisor) ApplySettings(id string, role Role, settings AgentSettings, edits TownSettings) error {
	mergePolicy, simplifierMode, closeSeverity := edits.MergePolicy, edits.SimplifierMode, edits.CloseSeverity
	if err := settings.validateRole(role); err != nil {
		return err
	}
	if mergePolicy != nil && *mergePolicy != "bot" && *mergePolicy != "manual" && *mergePolicy != "all" {
		return errors.New("merge policy must be bot, manual, or all")
	}
	if simplifierMode != nil && *simplifierMode != "suggest" && *simplifierMode != "auto" {
		return errors.New("simplifier mode must be suggest or auto")
	}
	if closeSeverity != nil && !ValidSeverity(*closeSeverity) {
		return errors.New("review close severity must be P1, P2 or P3")
	}
	if edits.Budget != nil {
		if err := edits.Budget.Budget.Validate(); err != nil {
			return err
		}
	}
	if edits.QuietHours != nil && edits.QuietHours.Windows != nil {
		if err := ValidateQuietHours(*edits.QuietHours.Windows); err != nil {
			return err
		}
	}
	if edits.WorkPolicy != nil {
		if role == "" {
			return errors.New("a work policy belongs to one bot; name it with a role")
		}
		if err := edits.WorkPolicy.Policy.Validate(role); err != nil {
			return err
		}
	}
	cancelRelease := false
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		if mergePolicy != nil {
			t.Config.MergePolicy = *mergePolicy
			if *mergePolicy == "manual" {
				cancelRelease = enforceManualReleasePolicy(t)
			} else if t.Workers[Release].Task == manualReleaseTask {
				t.Workers[Release].Task = "Ready when you are"
			}
		}
		if simplifierMode != nil {
			t.Config.SimplifierMode = *simplifierMode
		}
		if closeSeverity != nil {
			t.Config.ReviewCloseSeverity = *closeSeverity
		}
		if edits.Budget != nil {
			t.Config.Budget = edits.Budget.Budget
			// A changed period starts a fresh window rather than carrying an
			// old one's spend into a differently sized one.
			t.rollBudget(s.now())
		}
		if edits.QuietHours != nil {
			t.Config.QuietHours = nil
			if w := edits.QuietHours.Windows; w != nil {
				windows := append([]QuietWindow{}, (*w)...)
				t.Config.QuietHours = &windows
			}
		}
		if edits.WorkPolicy != nil {
			if edits.WorkPolicy.Policy.Empty() {
				delete(t.Config.BotPolicies, role)
			} else {
				if t.Config.BotPolicies == nil {
					t.Config.BotPolicies = map[Role]BotPolicy{}
				}
				t.Config.BotPolicies[role] = *edits.WorkPolicy.Policy
			}
			// Filters change which queued work is eligible, so the house looks
			// again rather than waiting out its current delay.
			if w := t.Workers[role]; w != nil {
				w.Next = time.Time{}
			}
		}
		switch {
		case role == "":
			cfg, err := s.Prepare(t.Config, settings)
			if err != nil {
				return err
			}
			t.Config = cfg
		case settings.Inherit:
			delete(t.Config.BotAgents, role)
		default:
			cfg, err := s.prepareAgent(t.Config.ForRole(role), settings)
			if err != nil {
				return err
			}
			if t.Config.BotAgents == nil {
				t.Config.BotAgents = map[Role]BotAgentConfig{}
			}
			t.Config.BotAgents[role] = cfg.botAgent()
		}
		target, title := "hall", "Town agent defaults saved for the next worker run"
		if role != "" {
			target, title = string(role), string(role)+" agent settings saved for the next worker run"
			if settings.Inherit {
				title = string(role) + " now inherits the town agent defaults"
			}
		}
		st.Event(id, "settings", "operator", target, "", title, s.now())
		return nil
	})
	if err != nil {
		return err
	}
	if cancelRelease {
		s.mu.Lock()
		if cancel := s.running[id+":"+string(Release)]; cancel != nil {
			cancel()
		}
		s.mu.Unlock()
	}
	return nil
}

// ChoiceValue is the API shape of one selectable model or effort. It stays
// independent of acp-go's generated schema so the browser contract is fixed.
type ChoiceValue struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

type AgentChoices struct {
	Models  []ChoiceValue `json:"models"`
	Efforts []ChoiceValue `json:"efforts"`
}

// choices lists the options a run selects. acp-go does not export its
// selector lookup, so sessionSelector repeats the one SetModel and SetEffort
// use: model category else uncategorized "model"; thought_level category else
// uncategorized "reasoning_effort".
func choices(session acp.Session) (AgentChoices, error) {
	models, err := selectValues(sessionSelector(session, schema.SessionConfigOptionCategoryModel, "model"))
	if err != nil {
		return AgentChoices{}, err
	}
	efforts, err := selectValues(sessionSelector(session, schema.SessionConfigOptionCategoryThoughtLevel, "reasoning_effort"))
	if err != nil {
		return AgentChoices{}, err
	}
	return AgentChoices{Models: models, Efforts: efforts}, nil
}

// sessionSelector matches acp-go's: the first select option in the category,
// else the first uncategorized select option with the conventional ID.
func sessionSelector(session acp.Session, category schema.SessionConfigOptionCategory, conventionalID string) *schema.SessionConfigOption {
	for i := range session.ConfigOptions {
		if o := &session.ConfigOptions[i]; o.Select != nil && o.Category != nil && *o.Category == category {
			return o
		}
	}
	for i := range session.ConfigOptions {
		if o := &session.ConfigOptions[i]; o.Select != nil && o.Category == nil && o.ID == schema.SessionConfigId(conventionalID) {
			return o
		}
	}
	return nil
}

// selectValues flattens a select option's values exactly as acp-go does when
// selecting one: the first entry decides between flat and grouped values.
func selectValues(option *schema.SessionConfigOption) ([]ChoiceValue, error) {
	out := []ChoiceValue{}
	if option == nil || option.Select.Options == nil {
		return out, nil
	}
	encoded, err := json.Marshal(option.Select.Options)
	if err != nil {
		return nil, err
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &items); err != nil || len(items) == 0 {
		return out, err
	}
	var flat []schema.SessionConfigSelectOption
	if _, grouped := items[0]["group"]; grouped || json.Unmarshal(encoded, &flat) != nil {
		var groups []schema.SessionConfigSelectGroup
		if err := json.Unmarshal(encoded, &groups); err != nil {
			return nil, fmt.Errorf("unsupported %s options: %w", option.Name, err)
		}
		flat = nil
		for _, group := range groups {
			flat = append(flat, group.Options...)
		}
	}
	for _, value := range flat {
		out = append(out, ChoiceValue{Value: string(value.Value), Name: value.Name})
	}
	return out, nil
}

// A discovery session never sends a prompt, exposes client tools or uses a
// repository worktree. It only asks the harness for its session selectors.
func ProbeAgent(ctx context.Context, cfg Config, roots ...string) (AgentChoices, error) {
	prepareCtx, cancelPrepare := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelPrepare()
	dir, err := os.MkdirTemp("", "brokk-town-choices-*")
	if err != nil {
		return AgentChoices{}, err
	}
	defer os.RemoveAll(dir)
	root := dir
	if len(roots) > 0 {
		root = roots[0]
	}
	a, err := agentConfig(prepareCtx, cfg, root)
	cancelPrepare()
	if err != nil {
		return AgentChoices{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := osrun.StartCommand(ctx, dir, a.Command, a.Environment)
	cmd.Stderr = &osrun.Tail{Capacity: 16 << 10}
	in, err := cmd.StdoutPipe()
	if err != nil {
		return AgentChoices{}, err
	}
	out, err := cmd.StdinPipe()
	if err != nil {
		in.Close()
		return AgentChoices{}, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return AgentChoices{}, errors.New("could not start harness; check its installation")
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	c := acp.Connect(in, out, nil, nil)
	defer c.Close()
	// Advertise the same session config support as runner.Execute, so an agent
	// that gates its selectors on it reports what a real run would see. No
	// workspace capabilities: discovery never serves files or terminals.
	init, err := c.InitializeWithInfo(ctx, acp.Capabilities{Session: acp.ConfigOptionsClientCapabilities(true)}, acp.ClientInfo{Name: "brokk-town", Version: "dev"})
	if err != nil {
		return AgentChoices{}, errors.New("harness initialization failed; check its installation and login")
	}
	if a.AuthMethod != "" {
		if err = c.Authenticate(ctx, init, a.AuthMethod); err != nil {
			return AgentChoices{}, errors.New("harness authentication failed; complete login in your terminal")
		}
	}
	session, err := c.NewSession(ctx, dir)
	if err != nil {
		return AgentChoices{}, errors.New("could not open a harness session; complete login in your terminal")
	}
	if a.Mode != "" {
		if err = c.SetMode(ctx, &session, a.Mode); err != nil {
			return AgentChoices{}, errors.New("harness rejected its configured mode")
		}
	}
	if a.Model != "" {
		if err = c.SetModel(ctx, &session, a.Model); err != nil {
			return AgentChoices{}, fmt.Errorf("model selection: %w", err)
		}
	}
	return choices(session)
}

func (s *Supervisor) Choices(ctx context.Context, id string, settings AgentSettings) (AgentChoices, error) {
	return s.ChoicesForRole(ctx, id, "", settings)
}

func (s *Supervisor) ChoicesForRole(ctx context.Context, id string, role Role, settings AgentSettings) (AgentChoices, error) {
	if err := settings.validateRole(role); err != nil {
		return AgentChoices{}, err
	}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return AgentChoices{}, ctx.Err()
	}
	t := s.Store.Snapshot().Towns[id]
	if t == nil || t.Deleted {
		s.mu.Unlock()
		return AgentChoices{}, errors.New("unknown town")
	}
	execution := t.Config.ExecutionForRole(role)
	if settings.Inherit {
		role, settings = "", AgentSettings{}
	}
	cfg, err := s.prepareAgent(t.Config.ForRole(role), settings)
	cfg.Execution = &execution
	if err != nil {
		s.mu.Unlock()
		return AgentChoices{}, err
	}
	if s.Store.Snapshot().Demo {
		s.mu.Unlock()
		return AgentChoices{Models: []ChoiceValue{{Value: "demo-model", Name: "Demo model"}}, Efforts: []ChoiceValue{{Value: "low", Name: "Low"}, {Value: "high", Name: "High"}}}, nil
	}
	key := id + ":choices"
	if s.running[key] != nil {
		s.mu.Unlock()
		return AgentChoices{}, errors.New("already loading harness choices")
	}
	ctx, cancel := context.WithCancel(ctx)
	s.running[key] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	defer func() { cancel(); s.mu.Lock(); delete(s.running, key); s.mu.Unlock() }()
	return ProbeAgent(ctx, cfg, filepath.Dir(s.Store.path))
}

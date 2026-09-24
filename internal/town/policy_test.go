package town

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func policyTown(t *testing.T, s *Store, repo string, policies map[Role]BotPolicy) *Town {
	t.Helper()
	var out *Town
	if err := s.Update(func(st *State) error {
		cfg := DefaultConfig(repo)
		cfg.BotPolicies = policies
		x, err := st.Add(cfg)
		if err != nil {
			return err
		}
		x.Initialized = true
		out = x
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPolicyValidationMatchesWhatEachHouseSupports(t *testing.T) {
	for _, tc := range []struct {
		name   string
		role   Role
		policy BotPolicy
		want   string
	}{
		{"issue takes label filters", Issue, BotPolicy{Labels: []string{"agent-ready"}, ExcludeLabels: []string{"blocked"}}, ""},
		{"issue takes one issue", Issue, BotPolicy{Only: 42}, ""},
		{"review takes one pull request", Review, BotPolicy{Only: 7, Limit: 5}, ""},
		{"bug takes a focus", Bug, BotPolicy{Focus: "the storage layer", Limit: 3}, ""},
		{"release takes cadence", Release, BotPolicy{Release: &ReleasePolicy{DailySeconds: 3600, MinimumGapSeconds: 600}}, ""},
		{"repo takes a repair limit", Repo, BotPolicy{Limit: 2}, ""},
		{"hall takes a bulletin limit", Hall, BotPolicy{Limit: 8}, ""},

		{"simplifier has no excluded labels", Simplifier, BotPolicy{ExcludeLabels: []string{"x"}}, "does not support excluded labels"},
		{"release filters no labels", Release, BotPolicy{Labels: []string{"x"}}, "does not filter its work by label"},
		{"bug cannot be pinned", Bug, BotPolicy{Only: 3}, "cannot be pinned to a single item"},
		{"issue takes no focus", Issue, BotPolicy{Focus: "anything"}, "does not take a focus"},
		{"issue takes no limit", Issue, BotPolicy{Limit: 5}, "does not take a limit"},
		{"repo takes no attempt limit", Repo, BotPolicy{Attempts: 3}, "does not take an attempt limit"},
		{"release settings belong to release", Review, BotPolicy{Release: &ReleasePolicy{Burst: 2, BurstWindowSeconds: 60}}, "belong to the release house"},

		{"a label cannot be required and excluded", Issue, BotPolicy{Labels: []string{"Ready"}, ExcludeLabels: []string{"ready"}}, "both required and excluded"},
		{"limits stay in range", Review, BotPolicy{Limit: maximumPolicyLimit + 1}, "limit must be between"},
		{"attempts stay in range", Issue, BotPolicy{Attempts: maximumPolicyAttempts + 1}, "attempts must be between"},
		{"a pinned number is positive", Issue, BotPolicy{Only: -1}, "must be positive"},
		{"a verify command needs an executable", Issue, BotPolicy{Verify: []string{"  "}}, "needs an executable"},
		{"a burst needs a window", Release, BotPolicy{Release: &ReleasePolicy{Burst: 5}}, "need burst_window_seconds"},
		{"cadence stays in range", Release, BotPolicy{Release: &ReleasePolicy{QuietSeconds: -1}}, "must be between 0"},
	} {
		err := tc.policy.Validate(tc.role)
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: accepted an unsupported setting", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %q does not mention %q", tc.name, err, tc.want)
		}
	}
	if err := (*BotPolicy)(nil).Validate(Issue); err != nil {
		t.Fatalf("an absent policy must stay valid: %v", err)
	}
}

func TestPolicyLabelFiltersDecideWhichWorkIsEligible(t *testing.T) {
	cfg := DefaultConfig("acme/filter")
	cfg.BotPolicies = map[Role]BotPolicy{
		Issue:  {Labels: []string{"agent-ready"}, ExcludeLabels: []string{"blocked"}},
		Review: {Only: 12},
	}
	for _, tc := range []struct {
		name string
		role Role
		task *Task
		want bool
	}{
		{"required label present", Issue, &Task{Kind: "issue", Number: 1, Labels: []string{"agent-ready"}}, true},
		{"label case does not matter", Issue, &Task{Kind: "issue", Number: 2, Labels: []string{"Agent-Ready"}}, true},
		{"required label missing", Issue, &Task{Kind: "issue", Number: 3, Labels: []string{"docs"}}, false},
		{"no labels at all", Issue, &Task{Kind: "issue", Number: 4}, false},
		{"excluded label wins", Issue, &Task{Kind: "issue", Number: 5, Labels: []string{"agent-ready", "blocked"}}, false},
		{"the pinned pull request", Review, &Task{Kind: "pr", Number: 12}, true},
		{"another pull request", Review, &Task{Kind: "pr", Number: 13}, false},
		{"a house with no policy takes everything", Bug, &Task{Kind: "issue", Number: 99}, true},
	} {
		if got := cfg.eligibleUnderPolicy(tc.role, tc.task); got != tc.want {
			t.Fatalf("%s: eligible = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPolicyRestrictsTownSelectionToEligibleWork(t *testing.T) {
	s := testStore(t, false)
	x := policyTown(t, s, "acme/select", map[Role]BotPolicy{
		Issue:  {Labels: []string{"agent-ready"}},
		Review: {Only: 12},
	})
	if err := s.Update(func(st *State) error {
		town := st.Towns[x.ID]
		town.Tasks["issue:1"] = &Task{ID: "issue:1", Kind: "issue", Number: 1, House: Issue, Stage: "queued", Labels: []string{"docs"}}
		town.Tasks["issue:2"] = &Task{ID: "issue:2", Kind: "issue", Number: 2, House: Issue, Stage: "queued", Labels: []string{"agent-ready"}}
		town.Tasks["pr:11"] = &Task{ID: "pr:11", Kind: "pr", Number: 11, House: Review, Stage: "queued"}
		town.Tasks["pr:12"] = &Task{ID: "pr:12", Kind: "pr", Number: 12, House: Review, Stage: "queued"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	current := s.Snapshot().Towns[x.ID]
	// Issue #1 sorts first but the filter excludes it.
	if got := nextIssue(current, time.Now()); got == nil || got.ID != "issue:2" {
		t.Fatalf("nextIssue = %v, want issue:2", got)
	}
	if got := nextTask(current, Review, "queued", time.Now()); got == nil || got.ID != "pr:12" {
		t.Fatalf("nextTask = %v, want pr:12", got)
	}
}

func TestPolicyKeepsFilteredWorkVisibleAndLabelled(t *testing.T) {
	s := testStore(t, false)
	x := policyTown(t, s, "acme/visible", map[Role]BotPolicy{Issue: {Labels: []string{"agent-ready"}}})
	if err := s.Update(func(st *State) error {
		town := st.Towns[x.ID]
		town.Tasks["issue:1"] = &Task{ID: "issue:1", Kind: "issue", Number: 1, House: Issue, Stage: "queued", Labels: []string{"docs"}}
		town.Tasks["issue:2"] = &Task{ID: "issue:2", Kind: "issue", Number: 2, House: Issue, Stage: "queued", Labels: []string{"agent-ready"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(s.Snapshot().PublicAt(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(raw, &public); err != nil {
		t.Fatal(err)
	}
	town := public["towns"].(map[string]any)[x.ID].(map[string]any)
	if town["filtered_tasks"].(float64) != 1 {
		t.Fatalf("filtered count = %v, want 1", town["filtered_tasks"])
	}
	tasks := town["tasks"].(map[string]any)
	if excluded, ok := tasks["issue:1"].(map[string]any)["policy_excluded"]; !ok || excluded != true {
		t.Fatal("the excluded issue is not marked in the snapshot")
	}
	if _, marked := tasks["issue:2"].(map[string]any)["policy_excluded"]; marked {
		t.Fatal("eligible work was marked as filtered")
	}
	if _, ok := tasks["issue:1"]; !ok {
		t.Fatal("filtered work vanished from the inventory instead of being marked")
	}
}

func TestPolicyReachesOnlyItsOwnHouse(t *testing.T) {
	cfg := DefaultConfig("acme/houses")
	cfg.Verify = []string{"make", "check"}
	cfg.BotPolicies = map[Role]BotPolicy{
		Review: {Limit: 5, Verify: []string{"make", "review-check"}},
	}
	review, configured := cfg.PolicyForRole(Review)
	if !configured || review.Limit != 5 || len(review.Verify) != 2 || review.Verify[1] != "review-check" {
		t.Fatalf("review policy = %+v (configured %v)", review, configured)
	}
	if _, configured := cfg.PolicyForRole(Issue); configured {
		t.Fatal("a house with no policy reported one")
	}
	// A town-wide verification command is not a work policy: dispatching it as
	// one would make every town need a policy-aware bot.
	bare := DefaultConfig("acme/bare")
	bare.Verify = []string{"make", "check"}
	if _, configured := bare.PolicyForRole(Issue); configured {
		t.Fatal("the shared verify command was reported as a work policy")
	}
}

func TestPolicySettingsRoundTripPerRole(t *testing.T) {
	s := testStore(t, false)
	x := policyTown(t, s, "acme/settings", nil)
	sup := NewSupervisor(s, nil, nil)
	policy := &BotPolicy{Labels: []string{"agent-ready"}, Only: 4, Attempts: 2}
	if err := sup.ApplySettings(x.ID, Issue, AgentSettings{}, TownSettings{WorkPolicy: &PolicyEdit{Policy: policy}}); err != nil {
		t.Fatal(err)
	}
	saved := s.Snapshot().Towns[x.ID].Config.BotPolicies[Issue]
	if saved.Only != 4 || saved.Attempts != 2 || len(saved.Labels) != 1 {
		t.Fatalf("saved policy = %+v", saved)
	}
	if _, exists := s.Snapshot().Towns[x.ID].Config.BotPolicies[Review]; exists {
		t.Fatal("an issue policy leaked into another house")
	}
	// A rejected setting names the house that cannot honour it.
	err := sup.ApplySettings(x.ID, Release, AgentSettings{}, TownSettings{WorkPolicy: &PolicyEdit{Policy: &BotPolicy{Labels: []string{"x"}}}})
	if err == nil || !strings.Contains(err.Error(), "does not filter its work by label") {
		t.Fatalf("release label filter error = %v", err)
	}
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{WorkPolicy: &PolicyEdit{Policy: policy}}); err == nil {
		t.Fatal("a work policy was saved without naming a house")
	}
	// Clearing restores the unfiltered queue.
	if err := sup.ApplySettings(x.ID, Issue, AgentSettings{}, TownSettings{WorkPolicy: &PolicyEdit{}}); err != nil {
		t.Fatal(err)
	}
	if _, exists := s.Snapshot().Towns[x.ID].Config.BotPolicies[Issue]; exists {
		t.Fatal("clearing left a policy saved")
	}
}

func TestPolicySummaryReadsWithoutFieldNames(t *testing.T) {
	cfg := DefaultConfig("acme/summary")
	triage := false
	cfg.BotPolicies = map[Role]BotPolicy{
		Issue:   {Labels: []string{"agent-ready"}, ExcludeLabels: []string{"blocked"}, Only: 4},
		Release: {Release: &ReleasePolicy{DailySeconds: 3600, Triage: &triage, Preflight: []string{"make", "preflight"}}},
		Bug:     {},
	}
	public := cfg.Public().WorkPolicies
	if len(public) != 2 {
		t.Fatalf("an empty policy was published: %+v", public)
	}
	byRole := map[Role]PublicBotPolicy{}
	for _, p := range public {
		byRole[p.Role] = p
	}
	issue := byRole[Issue]
	for _, want := range []string{"only issue #4", "labelled agent-ready", "skipping blocked"} {
		if !strings.Contains(issue.Summary, want) {
			t.Fatalf("issue summary %q does not mention %q", issue.Summary, want)
		}
	}
	release := byRole[Release]
	for _, want := range []string{"daily deadline 3600s", "agent triage off", "a preflight command"} {
		if !strings.Contains(release.Summary, want) {
			t.Fatalf("release summary %q does not mention %q", release.Summary, want)
		}
	}
	if release.Release == nil || release.Release.PreflightArgs != 2 {
		t.Fatalf("release preflight not summarized: %+v", release.Release)
	}
}

func TestPolicyPublicViewWithholdsCommandArguments(t *testing.T) {
	cfg := DefaultConfig("acme/secrets")
	cfg.BotPolicies = map[Role]BotPolicy{
		Issue:   {Verify: []string{"./check", "--token", "s3cret"}},
		Release: {Release: &ReleasePolicy{Preflight: []string{"./preflight", "--key", "s3cret"}}},
	}
	raw, err := json.Marshal(cfg.Public())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cret") {
		t.Fatalf("a command argument reached the public config: %s", raw)
	}
	for _, p := range cfg.Public().WorkPolicies {
		if p.Role == Issue && p.VerifyArgs != 3 {
			t.Fatalf("issue verify length = %d, want 3", p.VerifyArgs)
		}
	}
}

func TestPolicyConfigValidationRejectsUnknownHouse(t *testing.T) {
	cfg := DefaultConfig("acme/unknown")
	cfg.BotPolicies = map[Role]BotPolicy{Role("mayor-bot"): {Limit: 1}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unknown house") {
		t.Fatalf("validation error = %v", err)
	}
	cfg.BotPolicies = map[Role]BotPolicy{Issue: {Focus: "nope"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "issue policy") {
		t.Fatalf("validation does not name the house: %v", err)
	}
}

func TestPolicyIsAbsentFromTownsThatNeverConfiguredOne(t *testing.T) {
	cfg := DefaultConfig("acme/plain")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Public().WorkPolicies) != 0 {
		t.Fatal("a town with no policy published one")
	}
	task := &Task{Kind: "issue", Number: 1, House: Issue, Stage: "queued"}
	if !cfg.eligibleUnderPolicy(Issue, task) {
		t.Fatal("an unconfigured town filtered its own work")
	}
}

func TestPolicyCarriesReleaseGating(t *testing.T) {
	policy := BotPolicy{Release: &ReleasePolicy{Workflows: []string{"ci"}, Assets: []string{"dist/*.tar.gz"}}}
	if err := policy.Validate(Release); err != nil {
		t.Fatal(err)
	}
	public := policy.public(Release)
	if public.Release == nil || len(public.Release.Workflows) != 1 || len(public.Release.Assets) != 1 {
		t.Fatalf("release gating not published: %+v", public.Release)
	}
	for _, want := range []string{"requiring ci", "1 required asset pattern"} {
		if !strings.Contains(public.Summary, want) {
			t.Fatalf("summary %q does not mention %q", public.Summary, want)
		}
	}
	for _, bad := range []ReleasePolicy{
		{Workflows: []string{" "}},
		{Assets: []string{"["}},
	} {
		if err := (&BotPolicy{Release: &bad}).Validate(Release); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

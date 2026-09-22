package town

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// BotPolicy is the work selection and limits Town passes to one bot house. An
// empty field keeps that bot's own default, so a town saved before these
// settings existed behaves exactly as it did.
//
// Every field here is backed by a setting the pinned bot already supports.
// Town refuses a policy a house cannot honour rather than accepting it and
// letting the bot quietly ignore it.
type BotPolicy struct {
	// Labels restricts the house to items carrying at least one of these
	// labels. ExcludeLabels drops items carrying any of them.
	Labels        []string `json:"labels,omitempty"`
	ExcludeLabels []string `json:"exclude_labels,omitempty"`
	// Only pins the house to a single issue or pull request number: the issue
	// number for Issue, the pull request number for Review.
	Only int `json:"only,omitempty"`
	// Focus steers a discovery scan at one area of the repository.
	Focus string `json:"focus,omitempty"`
	// Limit bounds what one run may produce. Its unit is the house's own: filed
	// issues for Bug and Feature, findings for Review, proposals for
	// Simplifier, repair attempts per revision for Repo, bulletin items for
	// Hall.
	Limit int `json:"limit,omitempty"`
	// Attempts is how many tries the bot gives one item before it gives up.
	Attempts int `json:"attempts,omitempty"`
	// Verify replaces the town's shared verification command for this house.
	Verify []string `json:"verify,omitempty"`
	// Release carries the settings only the release house has.
	Release *ReleasePolicy `json:"release,omitempty"`
}

// ReleasePolicy is the release cadence and gating Release Bot supports.
type ReleasePolicy struct {
	// DailySeconds is the deadline after which unreleased commits are released
	// regardless of cadence. MinimumGapSeconds is the shortest interval between
	// two releases. QuietSeconds is how long the branch must be still first.
	DailySeconds      int `json:"daily_seconds,omitempty"`
	MinimumGapSeconds int `json:"minimum_gap_seconds,omitempty"`
	QuietSeconds      int `json:"quiet_seconds,omitempty"`
	// Burst releases early once this many commits land inside
	// BurstWindowSeconds.
	Burst              int `json:"burst,omitempty"`
	BurstWindowSeconds int `json:"burst_window_seconds,omitempty"`
	// Triage asks the agent whether unreleased commits warrant releasing before
	// the cadence. A nil value keeps the bot's default.
	Triage *bool `json:"triage,omitempty"`
	// Preflight runs before a release is prepared; a non-zero exit stops it.
	Preflight []string `json:"preflight,omitempty"`
	// VerificationTimeoutSeconds bounds the independent publication check.
	VerificationTimeoutSeconds int `json:"verification_timeout_seconds,omitempty"`
	// Workflows are the GitHub workflow names a release must see succeed, and
	// Assets the file patterns it must publish, before verification passes.
	Workflows []string `json:"workflows,omitempty"`
	Assets    []string `json:"assets,omitempty"`
}

// policySupport records which settings one house can honour. It is the single
// place that has to change when a bot gains or loses a setting, and it drives
// both validation and the operator-facing explanation of an unsupported one.
type policySupport struct {
	labels        bool
	excludeLabels bool
	only          string // the item a number selects, empty when the house takes none
	focus         bool
	limit         string // what Limit counts, empty when the house takes none
	attempts      bool
	verify        bool
	release       bool
}

var policySupported = map[Role]policySupport{
	Bug:        {labels: true, focus: true, limit: "issues filed", attempts: true, verify: true},
	Feature:    {labels: true, focus: true, limit: "issues filed", attempts: true, verify: true},
	Issue:      {labels: true, excludeLabels: true, only: "issue", attempts: true, verify: true},
	Review:     {labels: true, excludeLabels: true, only: "pull request", focus: true, limit: "findings", attempts: true, verify: true},
	Simplifier: {labels: true, limit: "proposals", verify: true},
	Release:    {attempts: true, verify: true, release: true},
	Repo:       {limit: "repair attempts per revision", verify: true},
	Hall:       {limit: "bulletin items", verify: true},
}

// MaximumPolicyLabels bounds one filter so a pasted list cannot turn every
// inventory into an unbounded query.
const MaximumPolicyLabels = 32

const maximumPolicyLimit = 1000
const maximumPolicyAttempts = 20

// maximumPolicySeconds is thirty days: long enough for any release cadence, and
// short enough that a mistyped value is rejected rather than silently disabling
// the deadline.
const maximumPolicySeconds = 30 * 24 * 60 * 60

// validatePolicyLabels reuses the funnel label rule and adds the count cap that
// keeps one filter from turning every inventory into an unbounded query.
func validatePolicyLabels(field string, labels []string) error {
	if len(labels) > MaximumPolicyLabels {
		return fmt.Errorf("%s allows at most %d labels", field, MaximumPolicyLabels)
	}
	return validateLabels(field, labels)
}

func validateCommand(field string, command []string) error {
	if len(command) == 0 {
		return nil
	}
	if strings.TrimSpace(command[0]) == "" {
		return fmt.Errorf("%s needs an executable as its first argument", field)
	}
	for _, arg := range command {
		if len(arg) > 4096 || strings.ContainsAny(arg, "\x00\r\n") {
			return fmt.Errorf("%s contains an invalid argument", field)
		}
	}
	return nil
}

// Validate rejects a policy this house cannot honour. Reporting the supported
// settings by name keeps an operator from guessing which one was refused.
func (p *BotPolicy) Validate(role Role) error {
	if p == nil {
		return nil
	}
	support, known := policySupported[role]
	if !known {
		return fmt.Errorf("%s has no configurable work policy", role)
	}
	if len(p.Labels) > 0 {
		if !support.labels {
			return fmt.Errorf("%s does not filter its work by label", role)
		}
		if err := validatePolicyLabels("labels", p.Labels); err != nil {
			return err
		}
	}
	if len(p.ExcludeLabels) > 0 {
		if !support.excludeLabels {
			return fmt.Errorf("%s does not support excluded labels", role)
		}
		if err := validatePolicyLabels("exclude_labels", p.ExcludeLabels); err != nil {
			return err
		}
	}
	for _, l := range p.Labels {
		for _, x := range p.ExcludeLabels {
			if strings.EqualFold(strings.TrimSpace(l), strings.TrimSpace(x)) {
				return fmt.Errorf("label %q is both required and excluded", strings.TrimSpace(l))
			}
		}
	}
	if p.Only != 0 {
		if support.only == "" {
			return fmt.Errorf("%s cannot be pinned to a single item", role)
		}
		if p.Only < 1 {
			return fmt.Errorf("the selected %s number must be positive", support.only)
		}
	}
	if strings.TrimSpace(p.Focus) != "" {
		if !support.focus {
			return fmt.Errorf("%s does not take a focus", role)
		}
		if len(p.Focus) > 2000 || strings.ContainsAny(p.Focus, "\x00") {
			return errors.New("focus must be plain text under 2000 characters")
		}
	}
	if p.Limit != 0 {
		if support.limit == "" {
			return fmt.Errorf("%s does not take a limit", role)
		}
		if p.Limit < 1 || p.Limit > maximumPolicyLimit {
			return fmt.Errorf("limit must be between 1 and %d (%s)", maximumPolicyLimit, support.limit)
		}
	}
	if p.Attempts != 0 {
		if !support.attempts {
			return fmt.Errorf("%s does not take an attempt limit", role)
		}
		if p.Attempts < 1 || p.Attempts > maximumPolicyAttempts {
			return fmt.Errorf("attempts must be between 1 and %d", maximumPolicyAttempts)
		}
	}
	if len(p.Verify) > 0 {
		if !support.verify {
			return fmt.Errorf("%s does not run a verification command", role)
		}
		if err := validateCommand("verify", p.Verify); err != nil {
			return err
		}
	}
	if p.Release != nil {
		if !support.release {
			return fmt.Errorf("release settings belong to the release house, not %s", role)
		}
		if err := p.Release.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReleasePolicy) validate() error {
	for _, f := range []struct {
		name  string
		value int
	}{
		{"daily_seconds", r.DailySeconds},
		{"minimum_gap_seconds", r.MinimumGapSeconds},
		{"quiet_seconds", r.QuietSeconds},
		{"burst_window_seconds", r.BurstWindowSeconds},
		{"verification_timeout_seconds", r.VerificationTimeoutSeconds},
	} {
		if f.value < 0 || f.value > maximumPolicySeconds {
			return fmt.Errorf("%s must be between 0 and %d seconds", f.name, maximumPolicySeconds)
		}
	}
	if r.Burst < 0 || r.Burst > 10000 {
		return errors.New("burst must be between 0 and 10000 commits")
	}
	if r.Burst > 0 && r.BurstWindowSeconds == 0 {
		return errors.New("burst releases need burst_window_seconds")
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"workflows", r.Workflows}, {"assets", r.Assets}} {
		if len(field.values) > MaximumPolicyLabels {
			return fmt.Errorf("%s allows at most %d entries", field.name, MaximumPolicyLabels)
		}
		for _, v := range field.values {
			if strings.TrimSpace(v) == "" || len(v) > 200 || strings.ContainsAny(v, "\x00\r\n") {
				return fmt.Errorf("%s contains an empty or invalid entry", field.name)
			}
		}
		if field.name == "assets" {
			for _, pattern := range field.values {
				if _, err := filepath.Match(pattern, ""); err != nil {
					return fmt.Errorf("asset pattern %q is invalid: %w", pattern, err)
				}
			}
		}
	}
	return validateCommand("preflight", r.Preflight)
}

// Empty reports a policy that changes nothing, so Town can drop it rather than
// save a placeholder that later reads as a configured filter.
func (p *BotPolicy) Empty() bool {
	if p == nil {
		return true
	}
	return len(p.Labels) == 0 && len(p.ExcludeLabels) == 0 && p.Only == 0 &&
		strings.TrimSpace(p.Focus) == "" && p.Limit == 0 && p.Attempts == 0 &&
		len(p.Verify) == 0 && p.Release == nil
}

// PolicyForRole is the policy saved for one house, and whether the operator
// configured one at all. The town's shared verification command is deliberately
// not folded in: a town that only sets Config.Verify has configured no policy,
// and must keep dispatching to a bot that never learned to read them.
func (c Config) PolicyForRole(role Role) (BotPolicy, bool) {
	saved, ok := c.BotPolicies[role]
	if !ok || saved.Empty() {
		return BotPolicy{}, false
	}
	return clone(saved), true
}

// eligibleUnderPolicy reports whether this task is work the configured filters
// admit. Town applies the same rule its bots do, so the queue an operator reads
// matches the queue the house will actually take from.
func (c Config) eligibleUnderPolicy(role Role, task *Task) bool {
	policy, ok := c.BotPolicies[role]
	if !ok || task == nil {
		return true
	}
	if policy.Only != 0 && task.Number != policy.Only {
		return false
	}
	if len(policy.Labels) == 0 && len(policy.ExcludeLabels) == 0 {
		return true
	}
	labels := map[string]bool{}
	for _, l := range task.Labels {
		labels[strings.ToLower(strings.TrimSpace(l))] = true
	}
	for _, x := range policy.ExcludeLabels {
		if labels[strings.ToLower(strings.TrimSpace(x))] {
			return false
		}
	}
	if len(policy.Labels) == 0 {
		return true
	}
	for _, l := range policy.Labels {
		if labels[strings.ToLower(strings.TrimSpace(l))] {
			return true
		}
	}
	return false
}

// PublicBotPolicy is the operator-facing summary. Verification and preflight
// commands are reported as their length only: their arguments can carry paths
// and tokens that do not belong in a snapshot every client receives.
type PublicBotPolicy struct {
	Role          Role     `json:"role"`
	Labels        []string `json:"labels,omitempty"`
	ExcludeLabels []string `json:"exclude_labels,omitempty"`
	Only          int      `json:"only,omitempty"`
	Focus         string   `json:"focus,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	LimitUnit     string   `json:"limit_unit,omitempty"`
	Attempts      int      `json:"attempts,omitempty"`
	// VerifyArgs is how many arguments the house's own verification command
	// has; zero means it uses the town's.
	VerifyArgs int                  `json:"verify_args,omitempty"`
	Release    *PublicReleasePolicy `json:"release,omitempty"`
	// Summary is one line an operator can read without knowing the field names.
	Summary string `json:"summary"`
}

type PublicReleasePolicy struct {
	DailySeconds               int      `json:"daily_seconds,omitempty"`
	MinimumGapSeconds          int      `json:"minimum_gap_seconds,omitempty"`
	QuietSeconds               int      `json:"quiet_seconds,omitempty"`
	Burst                      int      `json:"burst,omitempty"`
	BurstWindowSeconds         int      `json:"burst_window_seconds,omitempty"`
	Triage                     *bool    `json:"triage,omitempty"`
	PreflightArgs              int      `json:"preflight_args,omitempty"`
	VerificationTimeoutSeconds int      `json:"verification_timeout_seconds,omitempty"`
	Workflows                  []string `json:"workflows,omitempty"`
	Assets                     []string `json:"assets,omitempty"`
}

func (p BotPolicy) public(role Role) PublicBotPolicy {
	out := PublicBotPolicy{
		Role: role, Labels: p.Labels, ExcludeLabels: p.ExcludeLabels, Only: p.Only,
		Focus: p.Focus, Limit: p.Limit, Attempts: p.Attempts, VerifyArgs: len(p.Verify),
	}
	if support, ok := policySupported[role]; ok {
		out.LimitUnit = support.limit
	}
	if r := p.Release; r != nil {
		out.Release = &PublicReleasePolicy{
			DailySeconds: r.DailySeconds, MinimumGapSeconds: r.MinimumGapSeconds,
			QuietSeconds: r.QuietSeconds, Burst: r.Burst, BurstWindowSeconds: r.BurstWindowSeconds,
			Triage: r.Triage, PreflightArgs: len(r.Preflight), VerificationTimeoutSeconds: r.VerificationTimeoutSeconds,
			Workflows: r.Workflows, Assets: r.Assets,
		}
	}
	out.Summary = p.summary(role)
	return out
}

func (p BotPolicy) summary(role Role) string {
	support := policySupported[role]
	parts := []string{}
	if p.Only > 0 {
		parts = append(parts, fmt.Sprintf("only %s #%d", support.only, p.Only))
	}
	if len(p.Labels) > 0 {
		parts = append(parts, "labelled "+strings.Join(p.Labels, " or "))
	}
	if len(p.ExcludeLabels) > 0 {
		parts = append(parts, "skipping "+strings.Join(p.ExcludeLabels, " and "))
	}
	if strings.TrimSpace(p.Focus) != "" {
		parts = append(parts, "focused on "+strings.TrimSpace(p.Focus))
	}
	if p.Limit > 0 {
		parts = append(parts, fmt.Sprintf("at most %d %s", p.Limit, support.limit))
	}
	if p.Attempts > 0 {
		parts = append(parts, fmt.Sprintf("%d attempts per item", p.Attempts))
	}
	if len(p.Verify) > 0 {
		parts = append(parts, "its own verification command")
	}
	if r := p.Release; r != nil {
		if r.DailySeconds > 0 {
			parts = append(parts, fmt.Sprintf("daily deadline %ds", r.DailySeconds))
		}
		if r.MinimumGapSeconds > 0 {
			parts = append(parts, fmt.Sprintf("minimum gap %ds", r.MinimumGapSeconds))
		}
		if r.QuietSeconds > 0 {
			parts = append(parts, fmt.Sprintf("quiet period %ds", r.QuietSeconds))
		}
		if r.Burst > 0 {
			parts = append(parts, fmt.Sprintf("burst at %d commits in %ds", r.Burst, r.BurstWindowSeconds))
		}
		if r.Triage != nil {
			if *r.Triage {
				parts = append(parts, "agent triage on")
			} else {
				parts = append(parts, "agent triage off")
			}
		}
		if len(r.Preflight) > 0 {
			parts = append(parts, "a preflight command")
		}
		if len(r.Workflows) > 0 {
			parts = append(parts, "requiring "+strings.Join(r.Workflows, " and "))
		}
		if len(r.Assets) > 0 {
			parts = append(parts, fmt.Sprintf("%d required asset pattern(s)", len(r.Assets)))
		}
		if r.VerificationTimeoutSeconds > 0 {
			parts = append(parts, fmt.Sprintf("verification timeout %ds", r.VerificationTimeoutSeconds))
		}
	}
	if len(parts) == 0 {
		return "Takes every item this house is given, with the bot's own defaults."
	}
	return "Takes work " + strings.Join(parts, ", ") + "."
}

// PublicPolicies lists the configured houses in a stable order.
func (c Config) PublicPolicies() []PublicBotPolicy {
	out := make([]PublicBotPolicy, 0, len(c.BotPolicies))
	for role, policy := range c.BotPolicies {
		if policy.Empty() {
			continue
		}
		out = append(out, policy.public(role))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

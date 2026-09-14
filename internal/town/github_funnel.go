package town

// This file contains the GitHub implementation of the source-funnel boundary.
// It deliberately depends on an injected provider.  The provider is the only
// code that knows how to authenticate to GitHub; normalized items, intents and
// receipts never contain credentials.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitHubFunnelErrorKind classifies provider failures.  Callers can make a
// safe retry/operator decision without inspecting provider-specific text.
type GitHubFunnelErrorKind string

const (
	GitHubAuthFailure      GitHubFunnelErrorKind = "auth_failure"
	GitHubRateLimitFailure GitHubFunnelErrorKind = "rate_limited"
	GitHubIncompleteRead   GitHubFunnelErrorKind = "incomplete"
	GitHubUncertainWrite   GitHubFunnelErrorKind = "uncertain_write"
	GitHubUnsupported      GitHubFunnelErrorKind = "unsupported"
	GitHubRevisionConflict GitHubFunnelErrorKind = "revision_conflict"
)

// GitHubFunnelError is returned for typed source failures. RetryAfter is set
// only when the provider supplied a bounded retry hint.
type GitHubFunnelError struct {
	Kind       GitHubFunnelErrorKind
	Operation  string
	RetryAfter time.Duration
	Err        error
}

func (e *GitHubFunnelError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		if e.Operation == "" {
			return string(e.Kind)
		}
		return e.Operation + ": " + string(e.Kind)
	}
	if e.Operation == "" {
		return string(e.Kind) + ": " + e.Err.Error()
	}
	return e.Operation + ": " + string(e.Kind) + ": " + e.Err.Error()
}

func (e *GitHubFunnelError) Unwrap() error { return e.Err }

// IsGitHubFunnelError reports whether err has the requested typed kind,
// including errors wrapped by an adapter or a provider.
func IsGitHubFunnelError(err error, kind GitHubFunnelErrorKind) bool {
	var typed *GitHubFunnelError
	return errors.As(err, &typed) && typed.Kind == kind
}

// classifyGitHubError gives callers typed outcomes even when a small provider
// implementation (or gh's stderr) returns only an error string. Providers may
// return GitHubFunnelError directly; those values are preserved unchanged.
func classifyGitHubError(err error, operation string) error {
	if err == nil {
		return nil
	}
	var typed *GitHubFunnelError
	if errors.As(err, &typed) {
		return err
	}
	message := strings.ToLower(err.Error())
	kind := GitHubFunnelErrorKind("")
	switch {
	case strings.Contains(message, "401"), strings.Contains(message, "bad credentials"), strings.Contains(message, "authentication"), strings.Contains(message, "unauthorized"):
		kind = GitHubAuthFailure
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"), strings.Contains(message, "rate_limit"):
		kind = GitHubRateLimitFailure
	}
	if kind == "" {
		return err
	}
	return &GitHubFunnelError{Kind: kind, Operation: operation, Err: err}
}

// GitHubLabelMapping describes one idempotent label transition. A transition
// is unsupported when both lists are empty; this prevents the adapter from
// claiming a capability it cannot enact.
type GitHubLabelMapping struct {
	Add    []string `json:"add_labels,omitempty"`
	Remove []string `json:"remove_labels,omitempty"`
}

func (m GitHubLabelMapping) empty() bool { return len(m.Add) == 0 && len(m.Remove) == 0 }

// GitHubFunnelConfig is the GitHub-specific location and selector slice. A
// query is passed to GitHub unchanged. Selected issue numbers are fetched by
// identity and take precedence over the paged query, which makes a focused
// run independent of search-index lag.
type GitHubFunnelConfig struct {
	ID             string                        `json:"id,omitempty"`
	FunnelID       string                        `json:"funnel_id,omitempty"`
	Repository     string                        `json:"repository"`
	Query          string                        `json:"query,omitempty"`
	SelectedIssues []int                         `json:"selected_issues,omitempty"`
	IncludeLabels  []string                      `json:"include_labels,omitempty"`
	ExcludeLabels  []string                      `json:"exclude_labels,omitempty"`
	Working        GitHubLabelMapping            `json:"working,omitempty"`
	Blocked        GitHubLabelMapping            `json:"blocked,omitempty"`
	Transitions    map[string]GitHubLabelMapping `json:"transitions,omitempty"`
}

func (c GitHubFunnelConfig) funnelID() string {
	if strings.TrimSpace(c.FunnelID) != "" {
		return strings.TrimSpace(c.FunnelID)
	}
	if strings.TrimSpace(c.ID) != "" {
		return strings.TrimSpace(c.ID)
	}
	return "github:" + strings.ToLower(strings.TrimSpace(c.Repository))
}

func (c GitHubFunnelConfig) mapping(name string) GitHubLabelMapping {
	if m, ok := c.Transitions[name]; ok {
		return m
	}
	switch name {
	case "working":
		return c.Working
	case "blocked":
		return c.Blocked
	default:
		return GitHubLabelMapping{}
	}
}

func validateLabels(field string, labels []string) error {
	seen := map[string]struct{}{}
	for _, raw := range labels {
		label := strings.TrimSpace(raw)
		if label == "" || len(label) > 100 || strings.ContainsAny(label, "\r\n\x00") {
			return fmt.Errorf("%s contains an invalid label", field)
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%s contains duplicate label %q", field, label)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateMapping(field string, mapping GitHubLabelMapping) error {
	if err := validateLabels(field+".add", mapping.Add); err != nil {
		return err
	}
	if err := validateLabels(field+".remove", mapping.Remove); err != nil {
		return err
	}
	added := map[string]struct{}{}
	for _, label := range mapping.Add {
		added[strings.ToLower(strings.TrimSpace(label))] = struct{}{}
	}
	for _, label := range mapping.Remove {
		if _, ok := added[strings.ToLower(strings.TrimSpace(label))]; ok {
			return fmt.Errorf("%s adds and removes %q", field, label)
		}
	}
	return nil
}

// Validate rejects ambiguous or unsafe selectors before the funnel is enabled.
func (c GitHubFunnelConfig) Validate() error {
	if !ValidRepo(strings.TrimSpace(c.Repository)) {
		return errors.New("GitHub funnel repository must be OWNER/REPO")
	}
	if len(c.Query) > 1000 || strings.ContainsAny(c.Query, "\r\n\x00") {
		return errors.New("GitHub funnel query must be at most 1000 characters on one line")
	}
	if len(c.SelectedIssues) > 100 {
		return errors.New("GitHub funnel supports at most 100 selected issues")
	}
	selected := map[int]struct{}{}
	for _, number := range c.SelectedIssues {
		if number < 1 {
			return errors.New("selected GitHub issue numbers must be positive")
		}
		if _, ok := selected[number]; ok {
			return fmt.Errorf("selected GitHub issue #%d appears more than once", number)
		}
		selected[number] = struct{}{}
	}
	if err := validateLabels("include_labels", c.IncludeLabels); err != nil {
		return err
	}
	if err := validateLabels("exclude_labels", c.ExcludeLabels); err != nil {
		return err
	}
	if err := validateMapping("working", c.mapping("working")); err != nil {
		return err
	}
	if err := validateMapping("blocked", c.mapping("blocked")); err != nil {
		return err
	}
	return nil
}

// GitHubIssue is the provider-neutral subset of a GitHub issue or pull
// request. Revision must change whenever a consequential field changes. Fakes
// may provide it directly; the CLI provider derives it when omitted.
type GitHubIssue struct {
	ID          string    `json:"id,omitempty"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body,omitempty"`
	URL         string    `json:"url,omitempty"`
	State       string    `json:"state"`
	Locked      bool      `json:"locked,omitempty"`
	PullRequest bool      `json:"pull_request,omitempty"`
	Labels      []string  `json:"labels,omitempty"`
	Revision    string    `json:"revision"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// GitHubIssuePage is one cursor page. Complete=false means the provider could
// not establish complete coverage; even an empty page is then incomplete.
type GitHubIssuePage struct {
	Items      []GitHubIssue
	NextCursor string
	Complete   bool
}

// GitHubFunnelProvider is intentionally small. Implementations may use gh,
// REST, GraphQL, or a fake; no authentication data crosses this boundary.
type GitHubFunnelProvider interface {
	ListIssues(context.Context, string, string, string) (GitHubIssuePage, error)
	Issue(context.Context, string, int) (GitHubIssue, error)
	UpdateLabels(context.Context, string, int, []string, []string) error
}

// GitHubFunnelItem is the normalized item emitted by Discover. Source identity
// is stable across edits and cursor pages; revision and provenance remain
// separate so stale writes cannot be mistaken for the same item.
type GitHubFunnelItem struct {
	FunnelID     string
	Provider     string
	SourceID     string
	Repository   string
	Number       int
	Kind         string
	Title        string
	Body         string
	URL          string
	State        string
	Locked       bool
	Labels       []string
	Revision     string
	Cursor       string
	Capabilities []string
}

type GitHubDiscovery struct {
	Items      []GitHubFunnelItem
	NextCursor string
	Complete   bool
}

// GitHubAction is the allowlisted lifecycle vocabulary exposed to a model.
type GitHubAction string

const (
	GitHubActionWorking GitHubAction = "working"
	GitHubActionBlocked GitHubAction = "blocked"
)

// GitHubActionIntent is what the caller persists before Execute. Keeping this
// as a separate value makes the write ordering explicit and restart-safe.
type GitHubActionIntent struct {
	ID               string
	FunnelID         string
	Provider         string
	Repository       string
	Number           int
	Action           GitHubAction
	ExpectedRevision string
	Add              []string
	Remove           []string
}

type GitHubActionReceipt struct {
	IntentID   string
	FunnelID   string
	Provider   string
	Repository string
	Number     int
	Action     GitHubAction
	Revision   string
	Confirmed  bool
}

// GitHubFunnel adapts one configured source location. It does not retain
// credentials, and it never retries a mutation after an uncertain response.
type GitHubFunnel struct {
	Config       GitHubFunnelConfig
	Client       GitHubFunnelProvider
	SourceConfig *FunnelConfig
}

func (f GitHubFunnel) ValidateConfig() error {
	if err := f.Config.Validate(); err != nil {
		return err
	}
	if f.Client == nil {
		return errors.New("GitHub funnel provider is required")
	}
	return nil
}

// NewConfiguredGitHubFunnel binds a GitHub provider to the source-neutral
// FunnelConfig.  The provider is responsible for resolving any credential
// reference; the reference itself is never copied into a work item.
func NewConfiguredGitHubFunnel(config FunnelConfig, provider GitHubFunnelProvider) *GitHubFunnel {
	copy := clone(config)
	return &GitHubFunnel{Config: githubConfigFromFunnel(config), Client: provider, SourceConfig: &copy}
}

func githubConfigFromFunnel(config FunnelConfig) GitHubFunnelConfig {
	converted := GitHubFunnelConfig{ID: string(config.ID), FunnelID: string(config.ID), Repository: config.Location["repository"], Query: config.Filter["query"]}
	if converted.Repository == "" {
		converted.Repository = config.Location["repo"]
	}
	if selected := config.Filter["selected_issues"]; selected != "" {
		for _, value := range strings.Split(selected, ",") {
			number, err := strconv.Atoi(strings.TrimSpace(value))
			if err == nil {
				converted.SelectedIssues = append(converted.SelectedIssues, number)
			}
		}
	}
	converted.IncludeLabels = splitConfigList(config.Filter["include_labels"])
	converted.ExcludeLabels = splitConfigList(config.Filter["exclude_labels"])
	for transition, actions := range config.Transitions {
		mapping := GitHubLabelMapping{}
		for _, action := range actions {
			name := strings.ToLower(strings.TrimSpace(action.Name))
			switch name {
			case "labels", "label", "update_labels":
				mapping.Add = append(mapping.Add, splitConfigList(action.Parameters["add"])...)
				mapping.Remove = append(mapping.Remove, splitConfigList(action.Parameters["remove"])...)
			case "add_labels", "add_label":
				mapping.Add = append(mapping.Add, splitConfigList(action.Parameters["labels"])...)
				if len(mapping.Add) == 0 {
					mapping.Add = append(mapping.Add, splitConfigList(action.Parameters["label"])...)
				}
			case "remove_labels", "remove_label":
				mapping.Remove = append(mapping.Remove, splitConfigList(action.Parameters["labels"])...)
				if len(mapping.Remove) == 0 {
					mapping.Remove = append(mapping.Remove, splitConfigList(action.Parameters["label"])...)
				}
			}
		}
		switch transition {
		case TransitionWorking:
			converted.Working = mapping
		case TransitionBlocked:
			converted.Blocked = mapping
		}
	}
	return converted
}

func splitConfigList(value string) []string {
	values := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}

func normalizeRevision(item GitHubIssue) string {
	if strings.TrimSpace(item.Revision) != "" {
		return strings.TrimSpace(item.Revision)
	}
	return Digest(struct {
		ID      string
		Number  int
		Title   string
		Body    string
		State   string
		Locked  bool
		Pull    bool
		Labels  []string
		Updated time.Time
	}{item.ID, item.Number, item.Title, item.Body, item.State, item.Locked, item.PullRequest, item.Labels, item.UpdatedAt})
}

func normalizeLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, raw := range labels {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, label)
	}
	return out
}

func labelSet(labels []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, label := range labels {
		if label = strings.TrimSpace(label); label != "" {
			set[strings.ToLower(label)] = struct{}{}
		}
	}
	return set
}

func (c GitHubFunnelConfig) accepts(item GitHubIssue, selected map[int]struct{}) bool {
	if item.Number < 1 {
		return false
	}
	if len(selected) != 0 {
		if _, ok := selected[item.Number]; !ok {
			return false
		}
	}
	labels := labelSet(item.Labels)
	for _, required := range c.IncludeLabels {
		if _, ok := labels[strings.ToLower(strings.TrimSpace(required))]; !ok {
			return false
		}
	}
	for _, excluded := range c.ExcludeLabels {
		if _, ok := labels[strings.ToLower(strings.TrimSpace(excluded))]; ok {
			return false
		}
	}
	return true
}

func (f GitHubFunnel) item(item GitHubIssue, cursor string) GitHubFunnelItem {
	// Issue number is the provider's stable address within the configured
	// repository and can be refreshed without a lossy reverse lookup.
	id := strconv.Itoa(item.Number)
	kind := "issue"
	if item.PullRequest {
		kind = "pr"
	}
	capabilities := []string{}
	if !f.Config.mapping("working").empty() {
		capabilities = append(capabilities, string(GitHubActionWorking))
	}
	if !f.Config.mapping("blocked").empty() {
		capabilities = append(capabilities, string(GitHubActionBlocked))
	}
	return GitHubFunnelItem{
		FunnelID: f.Config.funnelID(), Provider: "github", SourceID: id,
		Repository: f.Config.Repository, Number: item.Number, Kind: kind,
		Title: item.Title, Body: item.Body, URL: item.URL, State: item.State,
		Locked: item.Locked, Labels: normalizeLabels(item.Labels),
		Revision: normalizeRevision(item), Cursor: cursor, Capabilities: capabilities,
	}
}

func (f GitHubFunnel) selected() map[int]struct{} {
	selected := make(map[int]struct{}, len(f.Config.SelectedIssues))
	for _, n := range f.Config.SelectedIssues {
		selected[n] = struct{}{}
	}
	return selected
}

// Discover reads one source page, preserving its cursor. Focused selections
// use identity reads and never rely on GitHub search indexing.
func (f GitHubFunnel) DiscoverIssues(ctx context.Context, cursor string) (GitHubDiscovery, error) {
	if err := f.ValidateConfig(); err != nil {
		return GitHubDiscovery{}, err
	}
	selected := f.selected()
	if len(selected) != 0 {
		return f.discoverSelected(ctx, cursor, selected)
	}
	page, err := f.Client.ListIssues(ctx, f.Config.Repository, f.Config.Query, cursor)
	if err != nil {
		return GitHubDiscovery{}, classifyGitHubError(err, "discover")
	}
	out := GitHubDiscovery{NextCursor: page.NextCursor, Complete: page.Complete}
	for _, issue := range page.Items {
		if f.Config.accepts(issue, selected) {
			out.Items = append(out.Items, f.item(issue, cursor))
		}
	}
	if !page.Complete {
		return out, &GitHubFunnelError{Kind: GitHubIncompleteRead, Operation: "discover", Err: errors.New("provider did not establish complete coverage")}
	}
	return out, nil
}

func (f GitHubFunnel) discoverSelected(ctx context.Context, cursor string, selected map[int]struct{}) (GitHubDiscovery, error) {
	numbers := append([]int(nil), f.Config.SelectedIssues...)
	start := 0
	if strings.TrimSpace(cursor) != "" {
		var err error
		start, err = strconv.Atoi(cursor)
		if err != nil || start < 0 || start > len(numbers) {
			return GitHubDiscovery{}, &GitHubFunnelError{Kind: GitHubIncompleteRead, Operation: "discover", Err: errors.New("invalid selected-issue cursor")}
		}
	}
	out := GitHubDiscovery{Complete: true}
	for i := start; i < len(numbers); i++ {
		issue, err := f.Client.Issue(ctx, f.Config.Repository, numbers[i])
		if err != nil {
			out.NextCursor = strconv.Itoa(i)
			return out, classifyGitHubError(err, "discover")
		}
		if f.Config.accepts(issue, selected) {
			out.Items = append(out.Items, f.item(issue, strconv.Itoa(i)))
		}
	}
	return out, nil
}

func (f GitHubFunnel) Prepare(item GitHubFunnelItem, action GitHubAction) (GitHubActionIntent, error) {
	if err := f.ValidateConfig(); err != nil {
		return GitHubActionIntent{}, err
	}
	if item.Provider != "github" || item.Repository != f.Config.Repository || item.Number < 1 || item.Revision == "" {
		return GitHubActionIntent{}, errors.New("item does not belong to this GitHub funnel")
	}
	var mapping GitHubLabelMapping
	switch action {
	case GitHubActionWorking:
		mapping = f.Config.mapping("working")
	case GitHubActionBlocked:
		mapping = f.Config.mapping("blocked")
	default:
		return GitHubActionIntent{}, &GitHubFunnelError{Kind: GitHubUnsupported, Operation: "prepare", Err: fmt.Errorf("unsupported GitHub action %q", action)}
	}
	if mapping.empty() {
		return GitHubActionIntent{}, &GitHubFunnelError{Kind: GitHubUnsupported, Operation: "prepare", Err: fmt.Errorf("GitHub action %q is not configured", action)}
	}
	intentID := Key(strings.Join([]string{f.Config.funnelID(), "github", f.Config.Repository, strconv.Itoa(item.Number), string(action), item.Revision, strings.Join(mapping.Add, "\x00"), strings.Join(mapping.Remove, "\x00")}, "\x1f"))
	return GitHubActionIntent{ID: intentID, FunnelID: f.Config.funnelID(), Provider: "github", Repository: f.Config.Repository, Number: item.Number, Action: action, ExpectedRevision: item.Revision, Add: append([]string(nil), mapping.Add...), Remove: append([]string(nil), mapping.Remove...)}, nil
}

// Execute performs exactly one label mutation after rereading the item. A
// stale revision is a conflict; a mutation or confirmation failure is
// uncertain and must be reconciled rather than blindly repeated.
func (f GitHubFunnel) Execute(ctx context.Context, intent GitHubActionIntent) (GitHubActionReceipt, error) {
	if err := f.ValidateConfig(); err != nil {
		return GitHubActionReceipt{}, err
	}
	if intent.Provider != "github" || intent.Repository != f.Config.Repository || intent.Number < 1 || intent.ExpectedRevision == "" {
		return GitHubActionReceipt{}, errors.New("invalid GitHub action intent")
	}
	current, err := f.Client.Issue(ctx, intent.Repository, intent.Number)
	if err != nil {
		return GitHubActionReceipt{}, classifyGitHubError(err, "execute")
	}
	currentRevision := normalizeRevision(current)
	if currentRevision != intent.ExpectedRevision {
		return GitHubActionReceipt{}, &GitHubFunnelError{Kind: GitHubRevisionConflict, Operation: "execute", Err: fmt.Errorf("issue #%d changed from %s to %s", intent.Number, intent.ExpectedRevision, currentRevision)}
	}
	if err := f.Client.UpdateLabels(ctx, intent.Repository, intent.Number, append([]string(nil), intent.Add...), append([]string(nil), intent.Remove...)); err != nil {
		if classified := classifyGitHubError(err, "update labels"); classified != err {
			return GitHubActionReceipt{}, classified
		}
		return GitHubActionReceipt{}, &GitHubFunnelError{Kind: GitHubUncertainWrite, Operation: "update labels", Err: err}
	}
	after, err := f.Client.Issue(ctx, intent.Repository, intent.Number)
	if err != nil {
		if classified := classifyGitHubError(err, "confirm labels"); classified != err {
			return GitHubActionReceipt{}, classified
		}
		return GitHubActionReceipt{}, &GitHubFunnelError{Kind: GitHubUncertainWrite, Operation: "confirm labels", Err: err}
	}
	if !labelsSatisfy(after.Labels, intent.Add, intent.Remove) {
		return GitHubActionReceipt{}, &GitHubFunnelError{Kind: GitHubUncertainWrite, Operation: "confirm labels", Err: errors.New("GitHub did not expose the requested labels")}
	}
	return GitHubActionReceipt{IntentID: intent.ID, FunnelID: intent.FunnelID, Provider: "github", Repository: intent.Repository, Number: intent.Number, Action: intent.Action, Revision: normalizeRevision(after), Confirmed: true}, nil
}

func labelsSatisfy(current, add, remove []string) bool {
	set := labelSet(current)
	for _, label := range add {
		if _, ok := set[strings.ToLower(strings.TrimSpace(label))]; !ok {
			return false
		}
	}
	for _, label := range remove {
		if _, ok := set[strings.ToLower(strings.TrimSpace(label))]; ok {
			return false
		}
	}
	return true
}

// Reconcile reads only. It confirms an already-applied mutation after a lost
// response, and never issues a second update when the receipt is absent.
func (f GitHubFunnel) ReconcileAction(ctx context.Context, intent GitHubActionIntent) (GitHubActionReceipt, error) {
	if err := f.ValidateConfig(); err != nil {
		return GitHubActionReceipt{}, err
	}
	if intent.Provider != "github" || intent.Repository != f.Config.Repository || intent.Number < 1 || intent.ExpectedRevision == "" {
		return GitHubActionReceipt{}, errors.New("invalid GitHub action intent")
	}
	current, err := f.Client.Issue(ctx, intent.Repository, intent.Number)
	if err != nil {
		return GitHubActionReceipt{}, classifyGitHubError(err, "reconcile")
	}
	if !labelsSatisfy(current.Labels, intent.Add, intent.Remove) {
		return GitHubActionReceipt{}, &GitHubFunnelError{Kind: GitHubUncertainWrite, Operation: "reconcile labels", Err: errors.New("receipt not found; requested labels are not confirmed")}
	}
	return GitHubActionReceipt{IntentID: intent.ID, FunnelID: intent.FunnelID, Provider: "github", Repository: intent.Repository, Number: intent.Number, Action: intent.Action, Revision: normalizeRevision(current), Confirmed: true}, nil
}

func (f *GitHubFunnel) Provider() ProviderID { return ProviderID("github") }

func (f *GitHubFunnel) Validate(config FunnelConfig) error {
	if config.Provider != f.Provider() {
		return errors.New("GitHub adapter requires provider github")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	c := githubConfigFromFunnel(config)
	return c.Validate()
}

func (f *GitHubFunnel) sourceConfig(request FunnelConfig) FunnelConfig {
	if f.SourceConfig != nil {
		return *f.SourceConfig
	}
	return request
}

func githubCapabilities(config FunnelConfig) CapabilitySet {
	out := CapabilitySet{}
	for _, pair := range []struct {
		action     CapabilityAction
		transition WorkTransition
	}{{ActionClaim, TransitionWorking}, {ActionReportBlocked, TransitionBlocked}, {ActionRequestHuman, TransitionHuman}, {ActionReportComplete, TransitionComplete}} {
		capability := Capability{Action: pair.action}
		actions, exists := config.Transitions[pair.transition]
		switch {
		case config.ReadOnly:
			capability.State, capability.Reason = CapabilityReadOnly, "GitHub funnel is read-only"
		case !exists:
			capability.State, capability.Reason = CapabilityUnsupported, "transition is not configured"
		case len(actions) == 0:
			capability.State, capability.Reason = CapabilityReadOnly, "transition is explicitly disabled"
		default:
			capability.State, capability.Mapping = CapabilitySupported, "GitHub labels"
		}
		out = append(out, capability)
	}
	return out
}

func (f *GitHubFunnel) Capabilities(_ context.Context, request CapabilityRequest) (CapabilitySet, error) {
	config := f.sourceConfig(request.Funnel)
	if err := f.Validate(config); err != nil {
		return nil, err
	}
	return githubCapabilities(config), nil
}

func githubWorkItem(config FunnelConfig, item GitHubFunnelItem) WorkItem {
	identity := WorkIdentity{Funnel: config.ID, Provider: ProviderID("github"), Item: SourceItemID(item.SourceID)}
	status := WorkQueued
	if item.State == "closed" {
		status = WorkClosed
	}
	return WorkItem{Identity: identity, Title: item.Title, Body: item.Body, Status: status, Eligible: item.State == "open" && !item.Locked, Eligibility: "matched configured GitHub selector", Priority: Priority{Policy: config.PriorityPolicy}, Provenance: Provenance{Identity: identity, URL: item.URL, Revision: Revision(item.Revision), Cursor: Cursor(item.Cursor), ObservedAt: time.Now(), ExternalState: item.State}, Capabilities: githubCapabilities(config)}
}

func (f *GitHubFunnel) Discover(ctx context.Context, request DiscoveryRequest) (DiscoveryPage, error) {
	config := f.sourceConfig(request.Funnel)
	if err := f.Validate(config); err != nil {
		return DiscoveryPage{}, err
	}
	bound := *f
	bound.Config = githubConfigFromFunnel(config)
	discovery, err := bound.DiscoverIssues(ctx, string(request.Cursor))
	page := DiscoveryPage{Funnel: config.ID, Provider: f.Provider(), Cursor: request.Cursor, NextCursor: Cursor(discovery.NextCursor), Complete: discovery.Complete && err == nil, ObservedAt: time.Now()}
	for _, item := range discovery.Items {
		page.Items = append(page.Items, githubWorkItem(config, item))
	}
	if err != nil {
		page.Outcome = githubOutcome(err)
		return page, githubGenericError(err)
	}
	page.Outcome = Outcome{Kind: OutcomeComplete, Covered: page.Complete}
	return page, nil
}

func (f *GitHubFunnel) Refresh(ctx context.Context, request RefreshRequest) (RefreshResult, error) {
	config := f.sourceConfig(request.Funnel)
	if err := f.Validate(config); err != nil {
		return RefreshResult{}, err
	}
	number, err := strconv.Atoi(string(request.Identity.Item))
	if err != nil {
		return RefreshResult{}, errors.New("GitHub source item is not an issue number")
	}
	bound := *f
	bound.Config = githubConfigFromFunnel(config)
	issue, err := bound.Client.Issue(ctx, bound.Config.Repository, number)
	if err != nil {
		return RefreshResult{Outcome: githubOutcome(err), Refreshed: time.Now()}, githubGenericError(err)
	}
	item := githubWorkItem(config, bound.item(issue, string(request.Cursor)))
	return RefreshResult{Item: item, Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, Refreshed: time.Now()}, nil
}

func githubAction(action CapabilityAction) (GitHubAction, error) {
	switch action {
	case ActionClaim:
		return GitHubActionWorking, nil
	case ActionReportBlocked:
		return GitHubActionBlocked, nil
	default:
		return "", &UnsupportedError{Action: action, Reason: "GitHub label mapping unavailable"}
	}
}

func (f *GitHubFunnel) Apply(ctx context.Context, request LifecycleRequest) (LifecycleResult, error) {
	config := f.sourceConfig(FunnelConfig{})
	if err := githubCapabilities(config).Allows(request.Action); err != nil {
		return LifecycleResult{Outcome: OutcomeFromError(err)}, err
	}
	number, err := strconv.Atoi(string(request.Identity.Item))
	if err != nil {
		return LifecycleResult{}, err
	}
	action, err := githubAction(request.Action)
	if err != nil {
		return LifecycleResult{Outcome: OutcomeFromError(err)}, err
	}
	item := GitHubFunnelItem{FunnelID: string(config.ID), Provider: "github", SourceID: string(request.Identity.Item), Repository: f.Config.Repository, Number: number, Revision: string(request.ExpectedRevision)}
	intent, err := f.Prepare(item, action)
	if err != nil {
		return LifecycleResult{Outcome: githubOutcome(err)}, githubGenericError(err)
	}
	intent.ID = request.IntentID
	receipt, err := f.Execute(ctx, intent)
	if err != nil {
		return LifecycleResult{Outcome: githubOutcome(err)}, githubGenericError(err)
	}
	return LifecycleResult{Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, Receipt: &WriteReceipt{IntentID: request.IntentID, ProviderID: receipt.IntentID, Identity: request.Identity, Revision: Revision(receipt.Revision), ConfirmedAt: time.Now(), Detail: "GitHub labels confirmed"}}, nil
}

func (f *GitHubFunnel) Reconcile(ctx context.Context, request ReconcileRequest) (ReconciliationResult, error) {
	config := f.sourceConfig(FunnelConfig{})
	number, err := strconv.Atoi(string(request.Intent.Identity.Item))
	if err != nil {
		return ReconciliationResult{}, err
	}
	action, err := githubAction(request.Intent.Action)
	if err != nil {
		return ReconciliationResult{}, err
	}
	item := GitHubFunnelItem{FunnelID: string(config.ID), Provider: "github", SourceID: string(request.Intent.Identity.Item), Repository: f.Config.Repository, Number: number, Revision: string(request.Intent.ExpectedRevision)}
	intent, err := f.Prepare(item, action)
	if err != nil {
		return ReconciliationResult{}, err
	}
	intent.ID = request.Intent.ID
	receipt, err := f.ReconcileAction(ctx, intent)
	if err != nil {
		failure := githubGenericError(err)
		return ReconciliationResult{Status: ReconciliationUncertain, Outcome: OutcomeFromError(failure), Detail: failure.Error()}, failure
	}
	return ReconciliationResult{Status: ReconciliationConfirmed, Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, Receipt: &WriteReceipt{IntentID: request.Intent.ID, ProviderID: receipt.IntentID, Identity: request.Intent.Identity, Revision: Revision(receipt.Revision), ConfirmedAt: time.Now(), Detail: "GitHub labels confirmed during reconciliation"}}, nil
}

func githubGenericError(err error) error {
	var e *GitHubFunnelError
	if !errors.As(err, &e) {
		return err
	}
	switch e.Kind {
	case GitHubAuthFailure:
		return &AuthenticationError{Provider: "github", Reason: e.Error()}
	case GitHubRateLimitFailure:
		return &RateLimitError{RetryAfter: e.RetryAfter, Reason: e.Error()}
	case GitHubIncompleteRead:
		return &IncompleteError{Reason: e.Error()}
	case GitHubUnsupported:
		return &UnsupportedError{Reason: e.Error()}
	default:
		return &UncertainError{Reason: e.Error()}
	}
}
func githubOutcome(err error) Outcome { return OutcomeFromError(githubGenericError(err)) }

// ListIssues implements GitHubFunnelProvider for GitHubClient using the REST
// search endpoint. Cursor values are opaque to callers but are page numbers
// for this implementation.
func (g GitHubClient) ListIssues(ctx context.Context, repository, query, cursor string) (GitHubIssuePage, error) {
	page := 1
	if strings.TrimSpace(cursor) != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 1 {
			return GitHubIssuePage{}, &GitHubFunnelError{Kind: GitHubIncompleteRead, Operation: "list issues", Err: errors.New("invalid GitHub page cursor")}
		}
		page = parsed
	}
	q := "repo:" + repository
	if strings.TrimSpace(query) != "" {
		q += " " + strings.TrimSpace(query)
	}
	path := "search/issues?" + url.Values{"q": []string{q}, "per_page": []string{"100"}, "page": []string{strconv.Itoa(page)}}.Encode()
	var result struct {
		Items      []githubIssueJSON `json:"items"`
		Incomplete bool              `json:"incomplete_results"`
		Total      int               `json:"total_count"`
	}
	if err := g.api(ctx, "GET", path, nil, &result); err != nil {
		return GitHubIssuePage{}, err
	}
	items := make([]GitHubIssue, 0, len(result.Items))
	for _, raw := range result.Items {
		items = append(items, raw.issue())
	}
	next := ""
	if len(items) == 100 && page*100 < result.Total {
		next = strconv.Itoa(page + 1)
	}
	return GitHubIssuePage{Items: items, NextCursor: next, Complete: !result.Incomplete}, nil
}

func (g GitHubClient) Issue(ctx context.Context, repository string, number int) (GitHubIssue, error) {
	if !ValidRepo(repository) || number < 1 {
		return GitHubIssue{}, errors.New("invalid GitHub issue reference")
	}
	var raw githubIssueJSON
	if err := g.api(ctx, "GET", fmt.Sprintf("repos/%s/issues/%d", repository, number), nil, &raw); err != nil {
		return GitHubIssue{}, err
	}
	return raw.issue(), nil
}

func (g GitHubClient) UpdateLabels(ctx context.Context, repository string, number int, add, remove []string) error {
	if !ValidRepo(repository) || number < 1 {
		return errors.New("invalid GitHub issue reference")
	}
	if len(add) == 0 && len(remove) == 0 {
		return &GitHubFunnelError{Kind: GitHubUnsupported, Operation: "update labels", Err: errors.New("empty label transition")}
	}
	current, err := g.Issue(ctx, repository, number)
	if err != nil {
		return err
	}
	set := labelSet(current.Labels)
	for _, label := range add {
		if value := strings.TrimSpace(label); value != "" {
			set[strings.ToLower(value)] = struct{}{}
		}
	}
	for _, label := range remove {
		delete(set, strings.ToLower(strings.TrimSpace(label)))
	}
	// Preserve current display spelling where possible and append configured
	// labels as supplied. GitHub accepts the complete resulting set through PUT.
	labels := make([]string, 0, len(set))
	for _, value := range current.Labels {
		key := strings.ToLower(strings.TrimSpace(value))
		if _, ok := set[key]; ok {
			labels = append(labels, value)
			delete(set, key)
		}
	}
	for value := range set {
		labels = append(labels, value)
	}
	return g.api(ctx, "PUT", fmt.Sprintf("repos/%s/issues/%d/labels", repository, number), map[string]any{"labels": labels}, nil)
}

type githubIssueJSON struct {
	ID        int64     `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	URL       string    `json:"html_url"`
	State     string    `json:"state"`
	Locked    bool      `json:"locked"`
	UpdatedAt time.Time `json:"updated_at"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullRequest map[string]any `json:"pull_request"`
}

func (i githubIssueJSON) issue() GitHubIssue {
	labels := make([]string, 0, len(i.Labels))
	for _, label := range i.Labels {
		labels = append(labels, label.Name)
	}
	item := GitHubIssue{ID: strconv.FormatInt(i.ID, 10), Number: i.Number, Title: i.Title, Body: i.Body, URL: i.URL, State: i.State, Locked: i.Locked, Labels: labels, UpdatedAt: i.UpdatedAt, PullRequest: len(i.PullRequest) != 0}
	item.Revision = normalizeRevision(item)
	return item
}

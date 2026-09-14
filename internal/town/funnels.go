package town

// This file contains the source-neutral boundary for input funnels.  Funnel
// adapters own provider vocabulary and authentication; the rest of Town sees
// only these contracts.  In particular, none of the types below contain a
// credential value.  SecretRef is an identifier which an adapter resolves
// locally when it is ready to talk to its provider.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// FunnelID, ProviderID, and SourceItemID are deliberately distinct types.  A
// source item can match several funnels, and its funnel is therefore part of
// its stable Town identity.
type FunnelID string
type ProviderID string
type SourceItemID string
type Revision string
type Cursor string

// WorkIdentity is the durable identity of one normalized candidate.  The
// source item ID is not assumed to be numeric (Slack timestamps and Linear
// UUIDs are valid IDs too).
type WorkIdentity struct {
	Funnel   FunnelID     `json:"funnel"`
	Provider ProviderID   `json:"provider"`
	Item     SourceItemID `json:"item"`
}

func (i WorkIdentity) Validate() error {
	if err := validateName("funnel", string(i.Funnel)); err != nil {
		return err
	}
	if err := validateName("provider", string(i.Provider)); err != nil {
		return err
	}
	if strings.TrimSpace(string(i.Item)) == "" || strings.ContainsAny(string(i.Item), "\x00\r\n") || !utf8.ValidString(string(i.Item)) {
		return errors.New("source item ID must be non-empty UTF-8 without control characters")
	}
	return nil
}

// Canonical is unambiguous and stable across process restarts.  JSON is used
// instead of joining fields with a delimiter because provider IDs are
// intentionally opaque to the generic contract.
func (i WorkIdentity) Canonical() string {
	b, _ := json.Marshal(i)
	return string(b)
}

// Key is suitable for maps and durable references.  It is intentionally
// derived from all three identity components, including Funnel.
func (i WorkIdentity) Key() string { return Key(i.Canonical()) }

// String is intended for diagnostics, not for parsing or identity.  It does
// not include a secret value.
func (i WorkIdentity) String() string {
	return string(i.Funnel) + ":" + string(i.Provider) + ":" + string(i.Item)
}

// Provenance retains source-native information needed to refresh an item and
// to prove which observation preceded a write.  Revision and Cursor are opaque
// to Town and must not be discarded by an adapter.
type Provenance struct {
	Identity      WorkIdentity `json:"identity"`
	URL           string       `json:"url,omitempty"`
	Revision      Revision     `json:"revision,omitempty"`
	Cursor        Cursor       `json:"cursor,omitempty"`
	ObservedAt    time.Time    `json:"observed_at"`
	ExternalState string       `json:"external_state,omitempty"`
}

func (p Provenance) Validate() error {
	if err := p.Identity.Validate(); err != nil {
		return err
	}
	if strings.ContainsAny(p.URL, "\x00\r\n") {
		return errors.New("provenance URL contains a control character")
	}
	if strings.ContainsAny(string(p.Revision), "\x00\r\n") || strings.ContainsAny(string(p.Cursor), "\x00\r\n") {
		return errors.New("provenance revision or cursor contains a control character")
	}
	if !p.ObservedAt.IsZero() && p.ObservedAt.Location() == nil {
		return errors.New("provenance observation has no time location")
	}
	return nil
}

// WorkStatus is the small normalized vocabulary exposed to scheduling and
// model-facing prompts. ExternalState in Provenance preserves the provider's
// original state when it is useful for an operator.
type WorkStatus string

const (
	WorkQueued     WorkStatus = "queued"
	WorkWorking    WorkStatus = "working"
	WorkBlocked    WorkStatus = "blocked"
	WorkNeedsHuman WorkStatus = "needs_human"
	WorkComplete   WorkStatus = "complete"
	WorkReleased   WorkStatus = "released"
	WorkClosed     WorkStatus = "closed"
)

func (s WorkStatus) valid() bool {
	switch s {
	case WorkQueued, WorkWorking, WorkBlocked, WorkNeedsHuman, WorkComplete, WorkReleased, WorkClosed:
		return true
	default:
		return false
	}
}

// Terminal reports source-observed completion. Terminal work remains in the
// durable inventory for provenance and audit, but must not be dispatched.
func (s WorkStatus) Terminal() bool {
	return s == WorkComplete || s == WorkClosed
}

// Priority is separate from discovery order. Every item has an explicit
// policy, including the intentional "unprioritized" policy for adapters that
// do not select work by priority.
type Priority struct {
	Value  int    `json:"value"`
	Policy string `json:"policy"`
	Reason string `json:"reason,omitempty"`
}

func (p Priority) Validate() error {
	if strings.TrimSpace(p.Policy) == "" || strings.ContainsAny(p.Policy, "\x00\r\n") {
		return errors.New("priority policy must be explicit")
	}
	if strings.ContainsAny(p.Reason, "\x00\r\n") {
		return errors.New("priority reason contains a control character")
	}
	return nil
}

// WorkItem is the normalized queue payload.  It contains no adapter handle,
// token, secret, or provider-specific authentication state.
type WorkItem struct {
	Identity     WorkIdentity  `json:"identity"`
	Title        string        `json:"title"`
	Body         string        `json:"body,omitempty"`
	Context      []string      `json:"context,omitempty"`
	Status       WorkStatus    `json:"status"`
	Eligible     bool          `json:"eligible"`
	Eligibility  string        `json:"eligibility,omitempty"`
	Priority     Priority      `json:"priority"`
	Provenance   Provenance    `json:"provenance"`
	Capabilities CapabilitySet `json:"capabilities"`
	LastOutcome  Outcome       `json:"last_outcome,omitempty"`
}

func (w WorkItem) Validate() error {
	if err := w.Identity.Validate(); err != nil {
		return err
	}
	if w.Provenance.Identity != w.Identity {
		return errors.New("work item provenance identity does not match item identity")
	}
	if strings.TrimSpace(w.Title) == "" || utf8.RuneCountInString(w.Title) > 1000 || strings.ContainsAny(w.Title, "\x00\r\n") {
		return errors.New("work item title must be 1–1000 characters on one line")
	}
	if strings.ContainsRune(w.Body, 0) || !utf8.ValidString(w.Body) {
		return errors.New("work item body must be valid UTF-8 without NUL")
	}
	if !w.Status.valid() {
		return fmt.Errorf("unknown normalized work status %q", w.Status)
	}
	if w.Status.Terminal() && w.Eligible {
		return errors.New("terminal work item cannot remain eligible")
	}
	if err := w.Priority.Validate(); err != nil {
		return err
	}
	if err := w.Provenance.Validate(); err != nil {
		return err
	}
	if err := w.Capabilities.Validate(); err != nil {
		return err
	}
	return nil
}

// CapabilityAction is the only action vocabulary exposed to model-facing
// callers. Adapters translate these to labels, reactions, comments, state
// changes, or other provider-native operations.
type CapabilityAction string

const (
	ActionClaim          CapabilityAction = "claim"
	ActionReportBlocked  CapabilityAction = "report_blocked"
	ActionRequestHuman   CapabilityAction = "request_human"
	ActionReportComplete CapabilityAction = "report_complete"
)

func (a CapabilityAction) valid() bool {
	switch a {
	case ActionClaim, ActionReportBlocked, ActionRequestHuman, ActionReportComplete:
		return true
	default:
		return false
	}
}

type CapabilityState string

const (
	CapabilitySupported       CapabilityState = "supported"
	CapabilityUnsupported     CapabilityState = "unsupported"
	CapabilityReadOnly        CapabilityState = "read_only"
	CapabilityRequiresMapping CapabilityState = "requires_mapping"
)

// Capability reports a capability even when it cannot currently be used. A
// UI or model can therefore distinguish an unsupported action from an adapter
// that is merely missing an operator mapping.
type Capability struct {
	Action  CapabilityAction `json:"action"`
	State   CapabilityState  `json:"state"`
	Reason  string           `json:"reason,omitempty"`
	Mapping string           `json:"mapping,omitempty"`
}

func (c Capability) Validate() error {
	if !c.Action.valid() {
		return fmt.Errorf("unknown funnel capability %q", c.Action)
	}
	switch c.State {
	case CapabilitySupported, CapabilityUnsupported, CapabilityReadOnly, CapabilityRequiresMapping:
	default:
		return fmt.Errorf("unknown funnel capability state %q", c.State)
	}
	if c.State != CapabilitySupported && strings.TrimSpace(c.Reason) == "" {
		return errors.New("non-supported capability must explain why")
	}
	if strings.ContainsAny(c.Reason, "\x00\r\n") || strings.ContainsAny(c.Mapping, "\x00\r\n") {
		return errors.New("capability contains a control character")
	}
	return nil
}

// CapabilitySet is a slice rather than a map so JSON and snapshots have
// deterministic ordering. Duplicate actions are invalid.
type CapabilitySet []Capability

func (c CapabilitySet) Validate() error {
	seen := map[CapabilityAction]bool{}
	for _, v := range c {
		if err := v.Validate(); err != nil {
			return err
		}
		if seen[v.Action] {
			return fmt.Errorf("duplicate funnel capability %q", v.Action)
		}
		seen[v.Action] = true
	}
	return nil
}

func (c CapabilitySet) Get(action CapabilityAction) (Capability, bool) {
	for _, v := range c {
		if v.Action == action {
			return v, true
		}
	}
	return Capability{}, false
}

func (c CapabilitySet) Allows(action CapabilityAction) error {
	v, ok := c.Get(action)
	if !ok {
		return &UnsupportedError{Action: action, Reason: "capability was not advertised"}
	}
	if v.State != CapabilitySupported {
		return &UnsupportedError{Action: action, Reason: v.Reason}
	}
	return nil
}

// SourceAction is adapter configuration, not model input. The adapter maps it
// to its own API after a lifecycle action has been authorized. Values are
// intentionally ordinary configuration strings; credentials must never be
// placed here.
type SourceAction struct {
	Name       string            `json:"name"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

func (a SourceAction) Validate() error {
	if strings.TrimSpace(a.Name) == "" || strings.ContainsAny(a.Name, "\x00\r\n") {
		return errors.New("source action name must be non-empty")
	}
	if err := validatePublicMap(a.Parameters, "source action parameters"); err != nil {
		return err
	}
	return nil
}

type WorkTransition string

const (
	TransitionWorking  WorkTransition = "working"
	TransitionBlocked  WorkTransition = "blocked"
	TransitionHuman    WorkTransition = "human"
	TransitionComplete WorkTransition = "complete"
)

func (t WorkTransition) valid() bool {
	switch t {
	case TransitionWorking, TransitionBlocked, TransitionHuman, TransitionComplete:
		return true
	default:
		return false
	}
}

// TransitionPolicy makes opt-outs explicit: an omitted transition has no
// configured write, while an entry with an empty action list is a deliberate
// no-op and can be reported as read-only by the adapter.
type TransitionPolicy map[WorkTransition][]SourceAction

func (p TransitionPolicy) Validate() error {
	for transition, actions := range p {
		if !transition.valid() {
			return fmt.Errorf("unknown work transition %q", transition)
		}
		for _, action := range actions {
			if err := action.Validate(); err != nil {
				return fmt.Errorf("%s transition: %w", transition, err)
			}
		}
	}
	return nil
}

// SecretRef is only a lookup reference. It deliberately has no token, value,
// environment contents, or fallback field that could be serialized into a
// snapshot or sent to a model.
type SecretRef struct {
	Name    string `json:"name"`
	Backend string `json:"backend,omitempty"`
}

// SecretReference is a descriptive alias for callers that prefer the longer
// name.
type SecretReference = SecretRef

func (s SecretRef) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("secret reference name must be non-empty")
	}
	if strings.ContainsAny(s.Name, "\x00\r\n") || strings.ContainsAny(s.Backend, "\x00\r\n") {
		return errors.New("secret reference contains a control character")
	}
	return nil
}

// SourceLocation and SourceFilter intentionally preserve provider-specific
// location/filter keys without making them part of Town's lifecycle model.
type SourceLocation map[string]string
type SourceFilter map[string]string

func (l SourceLocation) Validate() error {
	if len(l) == 0 {
		return errors.New("funnel source location is required")
	}
	return validatePublicMap(l, "source location")
}

func (f SourceFilter) Validate() error { return validatePublicMap(f, "source filter") }

type OverlapPolicy string

const (
	// OverlapIndependent retains one item per funnel. This is the default and
	// cannot accidentally turn funnel declaration order into priority.
	OverlapIndependent OverlapPolicy = "independent"
	OverlapDeduplicate OverlapPolicy = "deduplicate"
	OverlapReject      OverlapPolicy = "reject"
)

// FunnelConfig is one named input funnel. Provider adapters perform additional
// provider-specific location/filter validation before enabling it.
type FunnelConfig struct {
	ID             FunnelID         `json:"id"`
	Provider       ProviderID       `json:"provider"`
	Location       SourceLocation   `json:"location"`
	Filter         SourceFilter     `json:"filter,omitempty"`
	Credentials    SecretRef        `json:"credentials,omitempty"`
	Transitions    TransitionPolicy `json:"transitions,omitempty"`
	ReadOnly       bool             `json:"read_only,omitempty"`
	Enabled        bool             `json:"enabled"`
	PriorityPolicy string           `json:"priority_policy"`
	Overlap        OverlapPolicy    `json:"overlap,omitempty"`
}

// InputFunnelConfig is an API-friendly alias.
type InputFunnelConfig = FunnelConfig

func (f FunnelConfig) Validate() error {
	if err := validateName("funnel", string(f.ID)); err != nil {
		return err
	}
	if err := validateName("provider", string(f.Provider)); err != nil {
		return err
	}
	if err := f.Location.Validate(); err != nil {
		return err
	}
	if err := f.Filter.Validate(); err != nil {
		return err
	}
	if f.Credentials.Name != "" {
		if err := f.Credentials.Validate(); err != nil {
			return err
		}
	}
	if err := f.Transitions.Validate(); err != nil {
		return err
	}
	if f.ReadOnly && len(f.Transitions) > 0 {
		return errors.New("read-only funnel cannot configure lifecycle transitions")
	}
	if strings.TrimSpace(f.PriorityPolicy) == "" || strings.ContainsAny(f.PriorityPolicy, "\x00\r\n") {
		return errors.New("funnel priority policy must be explicit")
	}
	if f.Overlap != "" && f.Overlap != OverlapIndependent && f.Overlap != OverlapDeduplicate && f.Overlap != OverlapReject {
		return fmt.Errorf("unknown funnel overlap policy %q", f.Overlap)
	}
	return nil
}

// Public omits credentials entirely, including the otherwise harmless lookup
// name. Callers serving snapshots should use this projection rather than
// serializing FunnelConfig.
type PublicFunnelConfig struct {
	ID             FunnelID         `json:"id"`
	Provider       ProviderID       `json:"provider"`
	Location       SourceLocation   `json:"location"`
	Filter         SourceFilter     `json:"filter,omitempty"`
	Transitions    TransitionPolicy `json:"transitions,omitempty"`
	ReadOnly       bool             `json:"read_only,omitempty"`
	Enabled        bool             `json:"enabled"`
	PriorityPolicy string           `json:"priority_policy"`
	Overlap        OverlapPolicy    `json:"overlap,omitempty"`
}

func (f FunnelConfig) Public() PublicFunnelConfig {
	return PublicFunnelConfig{ID: f.ID, Provider: f.Provider, Location: cloneMap(f.Location), Filter: cloneMap(f.Filter), Transitions: cloneTransitions(f.Transitions), ReadOnly: f.ReadOnly, Enabled: f.Enabled, PriorityPolicy: f.PriorityPolicy, Overlap: f.Overlap}
}

// FunnelConfigs validates a town's collection of named funnels. Declaration
// order is not a scheduling priority and is never used by ResolveOverlap.
type FunnelConfigs []FunnelConfig

func (f FunnelConfigs) Validate() error {
	seen := map[FunnelID]bool{}
	for _, config := range f {
		if err := config.Validate(); err != nil {
			return fmt.Errorf("funnel %q: %w", config.ID, err)
		}
		if seen[config.ID] {
			return fmt.Errorf("duplicate funnel ID %q", config.ID)
		}
		seen[config.ID] = true
	}
	return nil
}

// OutcomeKind is used in persisted observations and results. An empty or
// incomplete result is never equivalent to OutcomeComplete.
type OutcomeKind string

const (
	OutcomeComplete       OutcomeKind = "complete"
	OutcomeIncomplete     OutcomeKind = "incomplete"
	OutcomeUnsupported    OutcomeKind = "unsupported"
	OutcomeAuthentication OutcomeKind = "authentication_failed"
	OutcomeRateLimited    OutcomeKind = "rate_limited"
	OutcomePartial        OutcomeKind = "partial"
	OutcomeUncertain      OutcomeKind = "uncertain"
)

// Outcome is the serializable form of an expected operational result. The
// corresponding typed errors below make the same states usable with Go's
// errors.As/errors.Is APIs.
type Outcome struct {
	Kind     OutcomeKind `json:"kind"`
	Detail   string      `json:"detail,omitempty"`
	Cursor   Cursor      `json:"cursor,omitempty"`
	RetryAt  time.Time   `json:"retry_at,omitempty"`
	IntentID string      `json:"intent_id,omitempty"`
	Covered  bool        `json:"covered"`
}

func (o Outcome) Validate() error {
	switch o.Kind {
	case OutcomeComplete, OutcomeIncomplete, OutcomeUnsupported, OutcomeAuthentication, OutcomeRateLimited, OutcomePartial, OutcomeUncertain:
	default:
		return fmt.Errorf("unknown funnel outcome %q", o.Kind)
	}
	if o.Kind == OutcomeComplete && !o.Covered {
		return errors.New("complete outcome must state that coverage is complete")
	}
	if o.Kind != OutcomeRateLimited && !o.RetryAt.IsZero() {
		return errors.New("only rate-limited outcomes may carry retry_at")
	}
	if strings.ContainsAny(o.Detail, "\x00\r\n") {
		return errors.New("outcome detail contains a control character")
	}
	return nil
}

func (o Outcome) Complete() bool { return o.Kind == OutcomeComplete && o.Covered }

// IncompleteError means the adapter cannot claim that the entire configured
// source location was read. A zero-item page with this error remains
// incomplete, not clean.
type IncompleteError struct {
	Reason  string
	Cursor  Cursor
	Covered bool
}

func (e *IncompleteError) Error() string { return "funnel discovery incomplete: " + e.Reason }

// UnsupportedError is used both for an unadvertised action and a provider
// operation that the configured adapter cannot implement.
type UnsupportedError struct {
	Action CapabilityAction
	Reason string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("funnel action %q unsupported: %s", e.Action, e.Reason)
}

type AuthenticationError struct {
	Provider ProviderID
	Reason   string
}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("funnel authentication failed for %s: %s", e.Provider, e.Reason)
}

// AuthError is retained as a concise spelling for adapters.
type AuthError = AuthenticationError

type RateLimitError struct {
	RetryAfter time.Duration
	RetryAt    time.Time
	Reason     string
}

func (e *RateLimitError) Error() string {
	if !e.RetryAt.IsZero() {
		return fmt.Sprintf("funnel rate limited until %s: %s", e.RetryAt.Format(time.RFC3339), e.Reason)
	}
	return "funnel rate limited: " + e.Reason
}

type PartialError struct {
	Reason    string
	Cursor    Cursor
	Completed int
	Expected  int
}

func (e *PartialError) Error() string { return "funnel operation partial: " + e.Reason }

// UncertainError means a provider write may have been accepted but its receipt
// was not observed. It is intentionally distinct from a normal failure.
type UncertainError struct {
	IntentID string
	Reason   string
}

func (e *UncertainError) Error() string {
	return fmt.Sprintf("funnel write %s is uncertain: %s", e.IntentID, e.Reason)
}

func OutcomeFromError(err error) Outcome {
	if err == nil {
		return Outcome{Kind: OutcomeComplete, Covered: true}
	}
	var incomplete *IncompleteError
	if errors.As(err, &incomplete) {
		return Outcome{Kind: OutcomeIncomplete, Detail: incomplete.Reason, Cursor: incomplete.Cursor, Covered: incomplete.Covered}
	}
	var unsupported *UnsupportedError
	if errors.As(err, &unsupported) {
		return Outcome{Kind: OutcomeUnsupported, Detail: unsupported.Error()}
	}
	var auth *AuthenticationError
	if errors.As(err, &auth) {
		return Outcome{Kind: OutcomeAuthentication, Detail: auth.Error()}
	}
	var rate *RateLimitError
	if errors.As(err, &rate) {
		return Outcome{Kind: OutcomeRateLimited, Detail: rate.Reason, RetryAt: rate.RetryAt}
	}
	var partial *PartialError
	if errors.As(err, &partial) {
		return Outcome{Kind: OutcomePartial, Detail: partial.Reason, Cursor: partial.Cursor, Covered: false}
	}
	var uncertain *UncertainError
	if errors.As(err, &uncertain) {
		return Outcome{Kind: OutcomeUncertain, Detail: uncertain.Reason, IntentID: uncertain.IntentID, Covered: false}
	}
	return Outcome{Kind: OutcomeUncertain, Detail: err.Error(), Covered: false}
}

type DiscoveryRequest struct {
	Funnel FunnelConfig `json:"funnel"`
	Cursor Cursor       `json:"cursor,omitempty"`
	Limit  int          `json:"limit,omitempty"`
}

func (r DiscoveryRequest) Validate() error {
	if err := r.Funnel.Validate(); err != nil {
		return err
	}
	if r.Limit < 0 {
		return errors.New("discovery limit cannot be negative")
	}
	return nil
}

type DiscoveryPage struct {
	Funnel     FunnelID   `json:"funnel"`
	Provider   ProviderID `json:"provider"`
	Items      []WorkItem `json:"items"`
	Cursor     Cursor     `json:"cursor,omitempty"`
	NextCursor Cursor     `json:"next_cursor,omitempty"`
	Complete   bool       `json:"complete"`
	Outcome    Outcome    `json:"outcome"`
	ObservedAt time.Time  `json:"observed_at"`
}

func (p DiscoveryPage) Validate() error {
	if err := validateName("funnel", string(p.Funnel)); err != nil {
		return err
	}
	if err := validateName("provider", string(p.Provider)); err != nil {
		return err
	}
	for _, item := range p.Items {
		if item.Identity.Funnel != p.Funnel || item.Identity.Provider != p.Provider {
			return errors.New("discovery item identity does not match page source")
		}
		if err := item.Validate(); err != nil {
			return err
		}
	}
	if err := p.Outcome.Validate(); err != nil {
		return err
	}
	if p.Complete != p.Outcome.Complete() {
		return errors.New("discovery complete flag disagrees with outcome coverage")
	}
	return nil
}

type RefreshRequest struct {
	Funnel   FunnelConfig `json:"funnel"`
	Identity WorkIdentity `json:"identity"`
	Revision Revision     `json:"revision,omitempty"`
	Cursor   Cursor       `json:"cursor,omitempty"`
}

func (r RefreshRequest) Validate() error {
	if err := r.Funnel.Validate(); err != nil {
		return err
	}
	if err := r.Identity.Validate(); err != nil {
		return err
	}
	if r.Identity.Funnel != r.Funnel.ID || r.Identity.Provider != r.Funnel.Provider {
		return errors.New("refresh identity does not belong to funnel")
	}
	return nil
}

type RefreshResult struct {
	Item      WorkItem  `json:"item"`
	Outcome   Outcome   `json:"outcome"`
	Refreshed time.Time `json:"refreshed"`
}

func (r RefreshResult) Validate() error {
	if err := r.Item.Validate(); err != nil {
		return err
	}
	return r.Outcome.Validate()
}

type CapabilityRequest struct {
	Funnel   FunnelConfig  `json:"funnel"`
	Identity *WorkIdentity `json:"identity,omitempty"`
}

func (r CapabilityRequest) Validate() error {
	if err := r.Funnel.Validate(); err != nil {
		return err
	}
	if r.Identity != nil && (r.Identity.Funnel != r.Funnel.ID || r.Identity.Provider != r.Funnel.Provider) {
		return errors.New("capability identity does not belong to funnel")
	}
	return nil
}

type LifecycleRequest struct {
	Identity         WorkIdentity     `json:"identity"`
	Action           CapabilityAction `json:"action"`
	ExpectedRevision Revision         `json:"expected_revision,omitempty"`
	Cursor           Cursor           `json:"cursor,omitempty"`
	IntentID         string           `json:"intent_id"`
}

func (r LifecycleRequest) Validate() error {
	if err := r.Identity.Validate(); err != nil {
		return err
	}
	if !r.Action.valid() {
		return fmt.Errorf("unknown lifecycle action %q", r.Action)
	}
	if strings.TrimSpace(r.IntentID) == "" || strings.ContainsAny(r.IntentID, "\x00\r\n") {
		return errors.New("lifecycle request requires an intent ID")
	}
	return nil
}

type WriteReceipt struct {
	IntentID    string       `json:"intent_id"`
	ProviderID  string       `json:"provider_receipt,omitempty"`
	Identity    WorkIdentity `json:"identity"`
	Revision    Revision     `json:"revision,omitempty"`
	Cursor      Cursor       `json:"cursor,omitempty"`
	ConfirmedAt time.Time    `json:"confirmed_at"`
	Detail      string       `json:"detail,omitempty"`
}

func (r WriteReceipt) Validate() error {
	if strings.TrimSpace(r.IntentID) == "" {
		return errors.New("write receipt requires an intent ID")
	}
	if err := r.Identity.Validate(); err != nil {
		return err
	}
	if strings.ContainsAny(r.ProviderID, "\x00\r\n") || strings.ContainsAny(r.Detail, "\x00\r\n") {
		return errors.New("write receipt contains a control character")
	}
	return nil
}

type LifecycleResult struct {
	Outcome Outcome       `json:"outcome"`
	Receipt *WriteReceipt `json:"receipt,omitempty"`
}

func (r LifecycleResult) Validate() error {
	if err := r.Outcome.Validate(); err != nil {
		return err
	}
	if r.Receipt != nil {
		if err := r.Receipt.Validate(); err != nil {
			return err
		}
		if r.Outcome.Kind != OutcomeComplete {
			return errors.New("receipt cannot accompany a non-complete lifecycle outcome")
		}
	}
	return nil
}

type IntentStatus string

const (
	IntentPending   IntentStatus = "pending"
	IntentUncertain IntentStatus = "uncertain"
	IntentConfirmed IntentStatus = "confirmed"
	IntentRejected  IntentStatus = "rejected"
)

// WriteIntent is persisted before the adapter is allowed to perform a
// mutation. Its expected revision/cursor prevent a stale refresh from being
// treated as authorization to write.
type WriteIntent struct {
	ID               string           `json:"id"`
	Identity         WorkIdentity     `json:"identity"`
	Action           CapabilityAction `json:"action"`
	ExpectedRevision Revision         `json:"expected_revision,omitempty"`
	Cursor           Cursor           `json:"cursor,omitempty"`
	Status           IntentStatus     `json:"status"`
	Detail           string           `json:"detail,omitempty"`
	Receipt          *WriteReceipt    `json:"receipt,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

func (w WriteIntent) Validate() error {
	if strings.TrimSpace(w.ID) == "" || strings.ContainsAny(w.ID, "\x00\r\n") {
		return errors.New("write intent requires an ID")
	}
	if err := w.Identity.Validate(); err != nil {
		return err
	}
	if !w.Action.valid() {
		return fmt.Errorf("unknown write intent action %q", w.Action)
	}
	switch w.Status {
	case IntentPending, IntentUncertain, IntentConfirmed, IntentRejected:
	default:
		return fmt.Errorf("unknown write intent status %q", w.Status)
	}
	if w.Receipt != nil {
		if err := w.Receipt.Validate(); err != nil {
			return err
		}
		if w.Receipt.IntentID != w.ID || w.Receipt.Identity != w.Identity {
			return errors.New("write receipt does not match intent")
		}
	}
	if strings.ContainsAny(w.Detail, "\x00\r\n") {
		return errors.New("write intent detail contains a control character")
	}
	return nil
}

func NewWriteIntent(req LifecycleRequest, now time.Time) (WriteIntent, error) {
	if err := req.Validate(); err != nil {
		return WriteIntent{}, err
	}
	return WriteIntent{ID: req.IntentID, Identity: req.Identity, Action: req.Action, ExpectedRevision: req.ExpectedRevision, Cursor: req.Cursor, Status: IntentPending, CreatedAt: now, UpdatedAt: now}, nil
}

type ReconcileRequest struct {
	Intent WriteIntent `json:"intent"`
}

func (r ReconcileRequest) Validate() error {
	if err := r.Intent.Validate(); err != nil {
		return err
	}
	if r.Intent.Status != IntentUncertain {
		return errors.New("only uncertain write intents may be reconciled")
	}
	return nil
}

type ReconciliationStatus string

const (
	ReconciliationConfirmed ReconciliationStatus = "confirmed"
	ReconciliationNotFound  ReconciliationStatus = "not_found"
	ReconciliationUncertain ReconciliationStatus = "uncertain"
	ReconciliationConflict  ReconciliationStatus = "conflict"
)

type ReconciliationResult struct {
	Status  ReconciliationStatus `json:"status"`
	Outcome Outcome              `json:"outcome"`
	Receipt *WriteReceipt        `json:"receipt,omitempty"`
	Item    *WorkItem            `json:"item,omitempty"`
	Detail  string               `json:"detail,omitempty"`
}

func (r ReconciliationResult) Validate() error {
	switch r.Status {
	case ReconciliationConfirmed, ReconciliationNotFound, ReconciliationUncertain, ReconciliationConflict:
	default:
		return fmt.Errorf("unknown reconciliation status %q", r.Status)
	}
	if err := r.Outcome.Validate(); err != nil {
		return err
	}
	if r.Receipt != nil && r.Status != ReconciliationConfirmed {
		return errors.New("only confirmed reconciliation may carry a receipt")
	}
	if r.Item != nil {
		if err := r.Item.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// WriteIntentStore is intentionally small so a persistent State store and a
// fake provider test can implement it without sharing provider details.
type WriteIntentStore interface {
	SaveWriteIntent(context.Context, WriteIntent) error
	GetWriteIntent(context.Context, string) (WriteIntent, error)
	UpdateWriteIntent(context.Context, WriteIntent) error
}

// ErrWriteIntentNotFound lets ExecuteLifecycle distinguish a new action from
// an existing intent without treating any other storage failure as safe.
var ErrWriteIntentNotFound = errors.New("write intent not found")

type FunnelDiscovery interface {
	Discover(context.Context, DiscoveryRequest) (DiscoveryPage, error)
	Refresh(context.Context, RefreshRequest) (RefreshResult, error)
}

type FunnelCapabilities interface {
	Capabilities(context.Context, CapabilityRequest) (CapabilitySet, error)
}

type FunnelLifecycle interface {
	Apply(context.Context, LifecycleRequest) (LifecycleResult, error)
	Reconcile(context.Context, ReconcileRequest) (ReconciliationResult, error)
}

// FunnelAdapter is the complete source boundary. Provider-specific adapters
// may reject a config even after generic validation (for example, a GitHub
// adapter can require repository+query while Slack requires channel).
type FunnelAdapter interface {
	FunnelDiscovery
	FunnelCapabilities
	FunnelLifecycle
	Provider() ProviderID
	Validate(FunnelConfig) error
}

// ExecuteLifecycle enforces the write protocol: save intent first, apply once,
// and make every non-confirmed result safe to resume through reconciliation.
// If an intent already exists, it is never blindly applied again.
func ExecuteLifecycle(ctx context.Context, store WriteIntentStore, adapter FunnelLifecycle, req LifecycleRequest, now time.Time) (LifecycleResult, error) {
	if err := req.Validate(); err != nil {
		return LifecycleResult{}, err
	}
	if store == nil || adapter == nil {
		return LifecycleResult{}, errors.New("lifecycle execution requires intent store and adapter")
	}
	if old, err := store.GetWriteIntent(ctx, req.IntentID); err == nil {
		switch old.Status {
		case IntentConfirmed:
			return LifecycleResult{Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, Receipt: old.Receipt}, nil
		case IntentPending, IntentUncertain:
			return LifecycleResult{Outcome: Outcome{Kind: OutcomeUncertain, Detail: "reconcile the existing intent before retrying", IntentID: old.ID}}, &UncertainError{IntentID: old.ID, Reason: "existing intent has no confirmed receipt"}
		case IntentRejected:
			return LifecycleResult{Outcome: Outcome{Kind: OutcomeUnsupported, Detail: old.Detail, IntentID: old.ID}}, &UnsupportedError{Action: old.Action, Reason: old.Detail}
		default:
			return LifecycleResult{}, fmt.Errorf("invalid saved write intent status %q", old.Status)
		}
	} else if !errors.Is(err, ErrWriteIntentNotFound) {
		return LifecycleResult{}, fmt.Errorf("read write intent: %w", err)
	}
	intent, err := NewWriteIntent(req, now)
	if err != nil {
		return LifecycleResult{}, err
	}
	if err = intent.Validate(); err != nil {
		return LifecycleResult{}, err
	}
	if err = store.SaveWriteIntent(ctx, intent); err != nil {
		return LifecycleResult{}, fmt.Errorf("save write intent before apply: %w", err)
	}
	result, applyErr := adapter.Apply(ctx, req)
	if result.Outcome.Kind == "" {
		result.Outcome = OutcomeFromError(applyErr)
	}
	if validationErr := result.Outcome.Validate(); validationErr != nil {
		applyErr = validationErr
		result.Outcome = OutcomeFromError(validationErr)
	}
	if applyErr == nil && result.Outcome.Kind == OutcomeComplete {
		if result.Receipt == nil {
			applyErr = &UncertainError{IntentID: intent.ID, Reason: "adapter returned complete without a receipt"}
		} else {
			result.Receipt.IntentID = intent.ID
			result.Receipt.Identity = intent.Identity
			intent.Status, intent.Receipt, intent.Detail = IntentConfirmed, result.Receipt, "provider receipt confirmed"
			intent.UpdatedAt = now
			if err = store.UpdateWriteIntent(ctx, intent); err != nil {
				return result, &UncertainError{IntentID: intent.ID, Reason: "receipt observed but durable confirmation failed: " + err.Error()}
			}
			return result, nil
		}
	}
	if applyErr == nil && result.Outcome.Kind == OutcomeUnsupported {
		applyErr = &UnsupportedError{Action: req.Action, Reason: result.Outcome.Detail}
	}
	var unsupported *UnsupportedError
	if errors.As(applyErr, &unsupported) {
		// A known unsupported action cannot have changed the provider. Keep the
		// intent as a terminal rejection so a retry cannot turn an opt-out into
		// an accidental write. Unknown provider failures remain uncertain.
		intent.Status, intent.Detail, intent.UpdatedAt = IntentRejected, unsupported.Error(), now
		if updateErr := store.UpdateWriteIntent(ctx, intent); updateErr != nil {
			return result, &UncertainError{IntentID: intent.ID, Reason: "could not persist rejected outcome: " + updateErr.Error()}
		}
		result.Outcome = OutcomeFromError(unsupported)
		result.Outcome.IntentID = intent.ID
		return result, unsupported
	}
	if applyErr == nil {
		applyErr = &UncertainError{IntentID: intent.ID, Reason: result.Outcome.Detail}
	}
	// Unknown provider failures, timeouts, partial writes and explicit
	// uncertain outcomes all retain the intent. A later call must reconcile it.
	intent.Status, intent.Detail, intent.UpdatedAt = IntentUncertain, applyErr.Error(), now
	if updateErr := store.UpdateWriteIntent(ctx, intent); updateErr != nil {
		return result, &UncertainError{IntentID: intent.ID, Reason: "could not persist uncertain outcome: " + updateErr.Error()}
	}
	result.Outcome = OutcomeFromError(applyErr)
	result.Outcome.IntentID = intent.ID
	return result, applyErr
}

// ReconcileLifecycle is the only path that can move an uncertain intent to a
// confirmed write. NotFound deliberately remains uncertain: absence is not a
// receipt and does not authorize a duplicate mutation.
func ReconcileLifecycle(ctx context.Context, store WriteIntentStore, adapter FunnelLifecycle, intentID string, now time.Time) (ReconciliationResult, error) {
	if store == nil || adapter == nil {
		return ReconciliationResult{}, errors.New("lifecycle reconciliation requires intent store and adapter")
	}
	intent, err := store.GetWriteIntent(ctx, intentID)
	if err != nil {
		return ReconciliationResult{}, err
	}
	request := ReconcileRequest{Intent: intent}
	if err = request.Validate(); err != nil {
		return ReconciliationResult{}, err
	}
	result, reconcileErr := adapter.Reconcile(ctx, request)
	if result.Outcome.Kind == "" {
		result.Outcome = OutcomeFromError(reconcileErr)
	}
	if reconcileErr == nil && result.Status == ReconciliationConfirmed && result.Receipt != nil {
		result.Receipt.IntentID, result.Receipt.Identity = intent.ID, intent.Identity
		intent.Status, intent.Receipt, intent.Detail, intent.UpdatedAt = IntentConfirmed, result.Receipt, "provider receipt confirmed during reconciliation", now
		if err = store.UpdateWriteIntent(ctx, intent); err != nil {
			return result, &UncertainError{IntentID: intent.ID, Reason: "reconciled receipt could not be persisted: " + err.Error()}
		}
		return result, nil
	}
	// All other statuses retain uncertainty. In particular, not_found is not a
	// permission to apply the original action again.
	if reconcileErr == nil {
		reconcileErr = &UncertainError{IntentID: intent.ID, Reason: "provider has not confirmed the intent"}
	}
	intent.Status, intent.Detail, intent.UpdatedAt = IntentUncertain, reconcileErr.Error(), now
	if updateErr := store.UpdateWriteIntent(ctx, intent); updateErr != nil {
		return result, &UncertainError{IntentID: intent.ID, Reason: "could not persist reconciliation outcome: " + updateErr.Error()}
	}
	result.Outcome = OutcomeFromError(reconcileErr)
	result.Outcome.IntentID = intent.ID
	return result, reconcileErr
}

// OverlapConflict records a source item observed by multiple funnels. It is
// data, not scheduling priority; operators can inspect the identities and
// choose a configured overlap policy.
type OverlapConflict struct {
	Provider ProviderID     `json:"provider"`
	Item     SourceItemID   `json:"item"`
	Matches  []WorkIdentity `json:"matches"`
}

// ResolveOverlap applies an explicit policy. Deduplication chooses the
// lexicographically canonical identity, never the first funnel in config
// order. Reject returns a conflict error and Independent keeps all matches.
func ResolveOverlap(items []WorkItem, policy OverlapPolicy) ([]WorkItem, []OverlapConflict, error) {
	if policy == "" {
		policy = OverlapIndependent
	}
	groups := map[string][]WorkItem{}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return nil, nil, err
		}
		key := string(item.Identity.Provider) + "\x00" + string(item.Identity.Item)
		groups[key] = append(groups[key], item)
	}
	conflicts := []OverlapConflict{}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		matches := make([]WorkIdentity, len(group))
		for i := range group {
			matches[i] = group[i].Identity
		}
		sort.Slice(matches, func(i, j int) bool { return matches[i].String() < matches[j].String() })
		conflicts = append(conflicts, OverlapConflict{Provider: group[0].Identity.Provider, Item: group[0].Identity.Item, Matches: matches})
	}
	if policy == OverlapReject && len(conflicts) != 0 {
		return nil, conflicts, errors.New("source item matched multiple funnels")
	}
	out := make([]WorkItem, 0, len(items))
	switch policy {
	case OverlapIndependent:
		out = append(out, items...)
	case OverlapDeduplicate:
		for _, group := range groups {
			sort.Slice(group, func(i, j int) bool { return group[i].Identity.String() < group[j].Identity.String() })
			out = append(out, group[0])
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Identity.String() < out[j].Identity.String() })
	default:
		return nil, nil, fmt.Errorf("unknown overlap policy %q", policy)
	}
	return out, conflicts, nil
}

func validateName(label, value string) error {
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") || !utf8.ValidString(value) {
		return fmt.Errorf("%s must be non-empty UTF-8 without control characters", label)
	}
	return nil
}

func validatePublicMap(values map[string]string, label string) error {
	for key, value := range values {
		if err := validateName(label+" key", key); err != nil {
			return err
		}
		lower := strings.ToLower(key)
		for _, forbidden := range []string{"secret", "token", "password", "credential", "authorization"} {
			if strings.Contains(lower, forbidden) {
				return fmt.Errorf("%s must use SecretRef for %q, not a value", label, key)
			}
		}
		if strings.ContainsAny(value, "\x00\r\n") || !utf8.ValidString(value) {
			return fmt.Errorf("%s value for %q contains a control character", label, key)
		}
	}
	return nil
}

func cloneMap[K comparable, V any](in map[K]V) map[K]V {
	if in == nil {
		return nil
	}
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTransitions(in TransitionPolicy) TransitionPolicy {
	if in == nil {
		return nil
	}
	out := make(TransitionPolicy, len(in))
	for transition, actions := range in {
		out[transition] = make([]SourceAction, len(actions))
		for i, action := range actions {
			out[transition][i] = SourceAction{Name: action.Name, Parameters: cloneMap(action.Parameters)}
		}
	}
	return out
}

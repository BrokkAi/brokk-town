package town

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testFunnelConfig() FunnelConfig {
	return FunnelConfig{
		ID:       "ready",
		Provider: "linear",
		Location: SourceLocation{"workspace": "acme", "team": "platform"},
		Filter:   SourceFilter{"state": "started"},
		Credentials: SecretRef{
			Name: "linear-default",
		},
		Transitions: TransitionPolicy{
			TransitionWorking: {{Name: "state", Parameters: map[string]string{"value": "In Progress"}}},
			TransitionBlocked: {{Name: "comment", Parameters: map[string]string{"template": "blocked"}}},
		},
		Enabled:        true,
		PriorityPolicy: "explicit:operator",
		Overlap:        OverlapIndependent,
	}
}

func testIdentity(funnel, provider, item string) WorkIdentity {
	return WorkIdentity{Funnel: FunnelID(funnel), Provider: ProviderID(provider), Item: SourceItemID(item)}
}

func testWorkItem(id WorkIdentity) WorkItem {
	return WorkItem{
		Identity: id,
		Title:    "Fix intake",
		Body:     "A normalized source item",
		Status:   WorkQueued,
		Eligible: true,
		Priority: Priority{Value: 10, Policy: "explicit:operator", Reason: "production incident"},
		Provenance: Provenance{
			Identity:      id,
			URL:           "https://linear.example/acme/issue/ABC-1",
			Revision:      "rev-7",
			Cursor:        "cursor-4",
			ObservedAt:    time.Unix(100, 0).UTC(),
			ExternalState: "started",
		},
		Capabilities: CapabilitySet{
			{Action: ActionClaim, State: CapabilitySupported},
			{Action: ActionReportBlocked, State: CapabilitySupported},
			{Action: ActionRequestHuman, State: CapabilityReadOnly, Reason: "comments are read-only"},
			{Action: ActionReportComplete, State: CapabilityRequiresMapping, Reason: "operator must choose a terminal state", Mapping: "state"},
		},
	}
}

func TestWorkIdentityIncludesFunnelAndStableProvenance(t *testing.T) {
	a := testIdentity("one", "slack", "1700000000.123")
	b := testIdentity("two", "slack", "1700000000.123")
	if a.Key() == b.Key() || a.Canonical() == b.Canonical() {
		t.Fatal("funnel must be part of stable identity")
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := a.String(); got != "one:slack:1700000000.123" {
		t.Fatalf("unexpected diagnostic identity %q", got)
	}
	item := testWorkItem(a)
	if err := item.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"revision":"rev-7"`) || !strings.Contains(string(encoded), `"cursor":"cursor-4"`) {
		t.Fatalf("revision/cursor were not retained: %s", encoded)
	}
}

func TestFunnelValidationRejectsCredentialsAndImplicitPriority(t *testing.T) {
	if err := (FunnelConfig{ID: "x", Provider: "slack", Location: SourceLocation{"channel": "C1"}}).Validate(); err == nil {
		t.Fatal("funnel without explicit priority policy was accepted")
	}
	config := testFunnelConfig()
	config.Filter["access_token"] = "secret-value"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "SecretRef") {
		t.Fatalf("secret-bearing source filter was accepted: %v", err)
	}
	config = testFunnelConfig()
	config.ReadOnly = true
	if err := config.Validate(); err == nil {
		t.Fatal("read-only funnel accepted lifecycle mappings")
	}
	public := config.Public()
	b, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "linear-default") || strings.Contains(string(b), "credentials") {
		t.Fatalf("public funnel leaked secret reference: %s", b)
	}
}

func TestCapabilitiesDistinguishSupportedReadOnlyAndUnconfigured(t *testing.T) {
	set := CapabilitySet{
		{Action: ActionClaim, State: CapabilitySupported},
		{Action: ActionReportBlocked, State: CapabilityUnsupported, Reason: "provider has no status write"},
		{Action: ActionRequestHuman, State: CapabilityReadOnly, Reason: "operator-only"},
	}
	if err := set.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := set.Allows(ActionClaim); err != nil {
		t.Fatal(err)
	}
	for _, action := range []CapabilityAction{ActionReportBlocked, ActionRequestHuman, ActionReportComplete} {
		var unsupported *UnsupportedError
		if err := set.Allows(action); !errors.As(err, &unsupported) {
			t.Fatalf("%s did not return typed unsupported result: %v", action, err)
		}
	}
	if err := (CapabilitySet{{Action: ActionClaim, State: CapabilityUnsupported}}).Validate(); err == nil {
		t.Fatal("unsupported capability without a reason was accepted")
	}
}

func TestOutcomeFromErrorPreservesIncompleteAndTypedUncertainty(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind OutcomeKind
	}{
		{"incomplete", &IncompleteError{Reason: "page 2 timed out", Cursor: "c1", Covered: false}, OutcomeIncomplete},
		{"unsupported", &UnsupportedError{Action: ActionClaim, Reason: "read only"}, OutcomeUnsupported},
		{"auth", &AuthenticationError{Provider: "slack", Reason: "token rejected"}, OutcomeAuthentication},
		{"rate", &RateLimitError{RetryAt: time.Unix(20, 0), Reason: "retry later"}, OutcomeRateLimited},
		{"partial", &PartialError{Reason: "second page malformed", Cursor: "c2", Completed: 1, Expected: 2}, OutcomePartial},
		{"uncertain", &UncertainError{IntentID: "intent-1", Reason: "lost response"}, OutcomeUncertain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := OutcomeFromError(tc.err)
			if out.Kind != tc.kind || out.Covered {
				t.Fatalf("outcome = %#v, want %s and incomplete", out, tc.kind)
			}
			if err := out.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if out := OutcomeFromError(nil); !out.Complete() {
		t.Fatalf("nil error did not produce complete outcome: %#v", out)
	}
}

func TestResolveOverlapIsExplicitAndIndependentOfFunnelOrder(t *testing.T) {
	a := testWorkItem(testIdentity("z-funnel", "slack", "m-1"))
	b := testWorkItem(testIdentity("a-funnel", "slack", "m-1"))
	independent, conflicts, err := ResolveOverlap([]WorkItem{a, b}, OverlapIndependent)
	if err != nil || len(independent) != 2 || len(conflicts) != 1 {
		t.Fatalf("independent overlap = %d items, %d conflicts, %v", len(independent), len(conflicts), err)
	}
	if conflicts[0].Matches[0] != b.Identity {
		t.Fatalf("conflict ordering was not canonical: %#v", conflicts[0].Matches)
	}
	deduped, conflicts, err := ResolveOverlap([]WorkItem{a, b}, OverlapDeduplicate)
	if err != nil || len(deduped) != 1 || len(conflicts) != 1 || deduped[0].Identity != b.Identity {
		t.Fatalf("deduplicated overlap = %#v, %d conflicts, %v", deduped, len(conflicts), err)
	}
	if _, conflicts, err = ResolveOverlap([]WorkItem{a, b}, OverlapReject); err == nil || len(conflicts) != 1 {
		t.Fatalf("reject policy did not preserve conflict: %v %#v", err, conflicts)
	}
}

type funnelIntentStore struct {
	intents map[string]WriteIntent
	saves   int
	updates int
}

func (s *funnelIntentStore) SaveWriteIntent(_ context.Context, intent WriteIntent) error {
	if s.intents == nil {
		s.intents = map[string]WriteIntent{}
	}
	if _, exists := s.intents[intent.ID]; exists {
		return errors.New("intent already exists")
	}
	s.intents[intent.ID] = intent
	s.saves++
	return nil
}

func (s *funnelIntentStore) GetWriteIntent(_ context.Context, id string) (WriteIntent, error) {
	intent, ok := s.intents[id]
	if !ok {
		return WriteIntent{}, ErrWriteIntentNotFound
	}
	return intent, nil
}

func (s *funnelIntentStore) UpdateWriteIntent(_ context.Context, intent WriteIntent) error {
	if _, ok := s.intents[intent.ID]; !ok {
		return ErrWriteIntentNotFound
	}
	s.intents[intent.ID] = intent
	s.updates++
	return nil
}

type funnelLifecycleFake struct {
	apply          func(LifecycleRequest) (LifecycleResult, error)
	reconcile      func(ReconcileRequest) (ReconciliationResult, error)
	applyCalls     int
	reconcileCalls int
	storeAtApply   *funnelIntentStore
}

func (f *funnelLifecycleFake) Apply(_ context.Context, req LifecycleRequest) (LifecycleResult, error) {
	f.applyCalls++
	if f.storeAtApply != nil {
		if _, err := f.storeAtApply.GetWriteIntent(context.Background(), req.IntentID); err != nil {
			return LifecycleResult{}, errors.New("apply ran before intent persistence")
		}
	}
	return f.apply(req)
}

func (f *funnelLifecycleFake) Reconcile(_ context.Context, req ReconcileRequest) (ReconciliationResult, error) {
	f.reconcileCalls++
	return f.reconcile(req)
}

func lifecycleRequest() LifecycleRequest {
	return LifecycleRequest{Identity: testIdentity("ready", "linear", "ABC-1"), Action: ActionClaim, ExpectedRevision: "rev-7", Cursor: "cursor-4", IntentID: "intent-1"}
}

func TestExecuteLifecyclePersistsBeforeApplyAndDoesNotRepeatUncertainWrite(t *testing.T) {
	store := &funnelIntentStore{}
	fake := &funnelLifecycleFake{storeAtApply: store, apply: func(LifecycleRequest) (LifecycleResult, error) {
		return LifecycleResult{}, &UncertainError{IntentID: "intent-1", Reason: "response timed out"}
	}}
	first, err := ExecuteLifecycle(context.Background(), store, fake, lifecycleRequest(), time.Unix(10, 0))
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || first.Outcome.Kind != OutcomeUncertain {
		t.Fatalf("first lifecycle = %#v, %v", first, err)
	}
	if store.saves != 1 || store.intents["intent-1"].Status != IntentUncertain || fake.applyCalls != 1 {
		t.Fatalf("intent ordering/status broken: saves=%d calls=%d intent=%#v", store.saves, fake.applyCalls, store.intents["intent-1"])
	}
	_, err = ExecuteLifecycle(context.Background(), store, fake, lifecycleRequest(), time.Unix(11, 0))
	if !errors.As(err, &uncertain) || fake.applyCalls != 1 {
		t.Fatalf("uncertain intent was blindly applied again: calls=%d err=%v", fake.applyCalls, err)
	}
}

func TestExecuteLifecycleConfirmedIsIdempotentAndUnsupportedIsTerminal(t *testing.T) {
	store := &funnelIntentStore{}
	applyCalls := 0
	fake := &funnelLifecycleFake{apply: func(req LifecycleRequest) (LifecycleResult, error) {
		applyCalls++
		return LifecycleResult{Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, Receipt: &WriteReceipt{ProviderID: "receipt-1", Identity: req.Identity}}, nil
	}}
	result, err := ExecuteLifecycle(context.Background(), store, fake, lifecycleRequest(), time.Unix(10, 0))
	if err != nil || !result.Outcome.Complete() || store.intents["intent-1"].Status != IntentConfirmed {
		t.Fatalf("confirmed lifecycle = %#v, %v; intent=%#v", result, err, store.intents["intent-1"])
	}
	if _, err = ExecuteLifecycle(context.Background(), store, fake, lifecycleRequest(), time.Unix(11, 0)); err != nil || applyCalls != 1 {
		t.Fatalf("confirmed intent was not idempotent: calls=%d err=%v", applyCalls, err)
	}

	unsupportedStore := &funnelIntentStore{}
	unsupported := &funnelLifecycleFake{apply: func(LifecycleRequest) (LifecycleResult, error) {
		return LifecycleResult{Outcome: Outcome{Kind: OutcomeUnsupported, Detail: "read-only"}}, nil
	}}
	_, err = ExecuteLifecycle(context.Background(), unsupportedStore, unsupported, lifecycleRequest(), time.Unix(20, 0))
	var unsupportedErr *UnsupportedError
	if !errors.As(err, &unsupportedErr) || unsupportedStore.intents["intent-1"].Status != IntentRejected {
		t.Fatalf("unsupported action was not terminal: err=%v intent=%#v", err, unsupportedStore.intents["intent-1"])
	}
	if _, err = ExecuteLifecycle(context.Background(), unsupportedStore, unsupported, lifecycleRequest(), time.Unix(21, 0)); !errors.As(err, &unsupportedErr) {
		t.Fatalf("rejected intent became retryable: %v", err)
	}
}

func TestReconcileLifecycleRequiresReceiptAndOnlyThenConfirms(t *testing.T) {
	store := &funnelIntentStore{}
	request := lifecycleRequest()
	intent, err := NewWriteIntent(request, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	intent.Status = IntentUncertain
	store.intents = map[string]WriteIntent{intent.ID: intent}
	fake := &funnelLifecycleFake{reconcile: func(ReconcileRequest) (ReconciliationResult, error) {
		return ReconciliationResult{Status: ReconciliationNotFound, Outcome: Outcome{Kind: OutcomeIncomplete, Covered: false}, Detail: "provider history is still paginated"}, nil
	}}
	result, err := ReconcileLifecycle(context.Background(), store, fake, intent.ID, time.Unix(20, 0))
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || result.Status != ReconciliationNotFound || store.intents[intent.ID].Status != IntentUncertain {
		t.Fatalf("not-found reconciliation = %#v, %v; intent=%#v", result, err, store.intents[intent.ID])
	}
	fake.reconcile = func(req ReconcileRequest) (ReconciliationResult, error) {
		return ReconciliationResult{Status: ReconciliationConfirmed, Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, Receipt: &WriteReceipt{ProviderID: "receipt-2", Identity: req.Intent.Identity}}, nil
	}
	result, err = ReconcileLifecycle(context.Background(), store, fake, intent.ID, time.Unix(30, 0))
	if err != nil || result.Status != ReconciliationConfirmed || store.intents[intent.ID].Status != IntentConfirmed {
		t.Fatalf("confirmed reconciliation = %#v, %v; intent=%#v", result, err, store.intents[intent.ID])
	}
	if !reflect.DeepEqual(store.intents[intent.ID].Receipt, result.Receipt) {
		t.Fatalf("saved receipt differs from returned receipt: %#v != %#v", store.intents[intent.ID].Receipt, result.Receipt)
	}
}

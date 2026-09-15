package town

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeGitHubFunnelProvider struct {
	items               map[int]GitHubIssue
	pages               []GitHubIssuePage
	listCalls           []string
	issueCalls          []int
	updateCalls         []fakeLabelUpdate
	listErr             error
	issueErr            error
	updateErr           error
	mutateBeforeConfirm bool
}

type fakeLabelUpdate struct {
	repository string
	number     int
	add        []string
	remove     []string
}

func (f *fakeGitHubFunnelProvider) ListIssues(_ context.Context, repository, query, cursor string) (GitHubIssuePage, error) {
	f.listCalls = append(f.listCalls, strings.Join([]string{repository, query, cursor}, "|"))
	if f.listErr != nil {
		return GitHubIssuePage{}, f.listErr
	}
	page := 0
	if cursor != "" {
		for i := range f.pages {
			if strings.HasPrefix(f.pages[i].NextCursor, cursor) || cursor == string(rune('0'+i+1)) {
				page = i
				break
			}
		}
	}
	if page >= len(f.pages) {
		return GitHubIssuePage{Complete: true}, nil
	}
	return f.pages[page], nil
}

func (f *fakeGitHubFunnelProvider) Issue(_ context.Context, _ string, number int) (GitHubIssue, error) {
	f.issueCalls = append(f.issueCalls, number)
	if f.issueErr != nil {
		return GitHubIssue{}, f.issueErr
	}
	item, ok := f.items[number]
	if !ok {
		return GitHubIssue{}, errors.New("not found")
	}
	item.Labels = append([]string(nil), item.Labels...)
	return item, nil
}

func (f *fakeGitHubFunnelProvider) UpdateLabels(_ context.Context, repository string, number int, add, remove []string) error {
	f.updateCalls = append(f.updateCalls, fakeLabelUpdate{repository: repository, number: number, add: append([]string(nil), add...), remove: append([]string(nil), remove...)})
	if f.updateErr != nil {
		// A fake may model a lost response after GitHub applied the mutation.
		if f.mutateBeforeConfirm {
			f.apply(number, add, remove)
		}
		return f.updateErr
	}
	f.apply(number, add, remove)
	return nil
}

func (f *fakeGitHubFunnelProvider) apply(number int, add, remove []string) {
	item := f.items[number]
	set := labelSet(item.Labels)
	for _, label := range add {
		set[strings.ToLower(label)] = struct{}{}
	}
	for _, label := range remove {
		delete(set, strings.ToLower(label))
	}
	labels := make([]string, 0, len(set))
	for label := range set {
		labels = append(labels, label)
	}
	item.Labels = labels
	item.Revision = "after-" + item.Revision
	f.items[number] = item
}

func validGitHubFunnel(t *testing.T, provider GitHubFunnelProvider) GitHubFunnel {
	t.Helper()
	return GitHubFunnel{Config: GitHubFunnelConfig{
		ID:            "ready",
		Repository:    "Acme/Orchard",
		Query:         "is:open",
		IncludeLabels: []string{"agent-ready"},
		ExcludeLabels: []string{"wont-fix"},
		Working:       GitHubLabelMapping{Add: []string{"agent-in-progress"}},
		Blocked:       GitHubLabelMapping{Add: []string{"escalated"}, Remove: []string{"agent-in-progress"}},
	}, Client: provider}
}

func TestGitHubFunnelDiscoverFiltersAndPreservesProvenance(t *testing.T) {
	provider := &fakeGitHubFunnelProvider{pages: []GitHubIssuePage{{Complete: true, NextCursor: "next", Items: []GitHubIssue{
		{ID: "one", Number: 1, Title: "ready", Revision: "r1", Labels: []string{"agent-ready"}},
		{ID: "two", Number: 2, Title: "missing label", Revision: "r2", Labels: []string{"other"}},
		{ID: "three", Number: 3, Title: "excluded", Revision: "r3", Labels: []string{"agent-ready", "wont-fix"}},
	}}}}
	funnel := validGitHubFunnel(t, provider)
	discovery, err := funnel.DiscoverIssues(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if discovery.NextCursor != "next" || !discovery.Complete || len(discovery.Items) != 1 {
		t.Fatalf("unexpected discovery: %+v", discovery)
	}
	item := discovery.Items[0]
	if item.FunnelID != "ready" || item.Provider != "github" || item.SourceID != "1" || item.Repository != "Acme/Orchard" || item.Cursor != "" || item.Revision != "r1" {
		t.Fatalf("lost normalized provenance: %+v", item)
	}
	if !reflect.DeepEqual(item.Capabilities, []string{"working", "blocked"}) {
		t.Fatalf("unexpected capabilities: %v", item.Capabilities)
	}
	if got := provider.listCalls; !reflect.DeepEqual(got, []string{"Acme/Orchard|is:open|"}) {
		t.Fatalf("unexpected list call: %v", got)
	}
}

func TestGitHubFunnelSelectedIssuesReadByIdentity(t *testing.T) {
	provider := &fakeGitHubFunnelProvider{items: map[int]GitHubIssue{
		7: {ID: "seven", Number: 7, Title: "selected", Revision: "r7", PullRequest: true, Labels: []string{"agent-ready"}},
		9: {ID: "nine", Number: 9, Title: "not selected", Revision: "r9", Labels: []string{"agent-ready"}},
	}}
	funnel := validGitHubFunnel(t, provider)
	funnel.Config.SelectedIssues = []int{7}
	funnel.Config.IncludeLabels = nil
	discovery, err := funnel.DiscoverIssues(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Items) != 1 || discovery.Items[0].Number != 7 || discovery.Items[0].Kind != "pr" || !discovery.Complete {
		t.Fatalf("unexpected selected discovery: %+v", discovery)
	}
	if !reflect.DeepEqual(provider.issueCalls, []int{7}) || len(provider.listCalls) != 0 {
		t.Fatalf("selected discovery used the wrong provider calls: issues=%v lists=%v", provider.issueCalls, provider.listCalls)
	}
	if _, err := funnel.DiscoverIssues(context.Background(), "bad-cursor"); !IsGitHubFunnelError(err, GitHubIncompleteRead) {
		t.Fatalf("invalid selected cursor was not typed incomplete: %v", err)
	}
}

func TestGitHubFunnelRevisionRereadRejectsStaleIntent(t *testing.T) {
	provider := &fakeGitHubFunnelProvider{items: map[int]GitHubIssue{
		1: {ID: "one", Number: 1, Title: "work", Revision: "r1", Labels: []string{"agent-ready"}},
	}, pages: []GitHubIssuePage{{Complete: true, Items: []GitHubIssue{{ID: "one", Number: 1, Title: "work", Revision: "r1", Labels: []string{"agent-ready"}}}}}}
	funnel := validGitHubFunnel(t, provider)
	discovery, err := funnel.DiscoverIssues(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := funnel.Prepare(discovery.Items[0], GitHubActionWorking)
	if err != nil {
		t.Fatal(err)
	}
	provider.items[1] = GitHubIssue{ID: "one", Number: 1, Title: "edited", Revision: "r2"}
	if _, err := funnel.Execute(context.Background(), intent); !IsGitHubFunnelError(err, GitHubRevisionConflict) {
		t.Fatalf("stale write was not rejected: %v", err)
	}
	if len(provider.updateCalls) != 0 {
		t.Fatal("stale revision reached the mutation")
	}
}

func TestGitHubFunnelLostResponseReconcilesWithoutRetry(t *testing.T) {
	provider := &fakeGitHubFunnelProvider{items: map[int]GitHubIssue{
		1: {ID: "one", Number: 1, Title: "work", Revision: "r1", Labels: []string{"agent-ready"}},
	}, updateErr: errors.New("connection reset"), mutateBeforeConfirm: true}
	funnel := validGitHubFunnel(t, provider)
	item := funnelItemForTest(t, funnel, provider.items[1])
	intent, err := funnel.Prepare(item, GitHubActionWorking)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := funnel.Execute(context.Background(), intent); !IsGitHubFunnelError(err, GitHubUncertainWrite) {
		t.Fatalf("lost response was not uncertain: %v", err)
	}
	receipt, err := funnel.ReconcileAction(context.Background(), intent)
	if err != nil || !receipt.Confirmed || receipt.IntentID != intent.ID {
		t.Fatalf("receipt did not reconcile: receipt=%+v err=%v", receipt, err)
	}
	if len(provider.updateCalls) != 1 {
		t.Fatalf("reconciliation repeated mutation: %d calls", len(provider.updateCalls))
	}
}

func funnelItemForTest(t *testing.T, funnel GitHubFunnel, issue GitHubIssue) GitHubFunnelItem {
	t.Helper()
	item := funnel.item(issue, "")
	if item.Revision == "" {
		t.Fatal("test item has no revision")
	}
	return item
}

func TestGitHubFunnelTypedProviderFailuresAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind GitHubFunnelErrorKind
	}{
		{name: "auth", err: errors.New("gh: HTTP 401 Bad credentials"), kind: GitHubAuthFailure},
		{name: "rate", err: errors.New("GitHub API rate limit exceeded (429)"), kind: GitHubRateLimitFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeGitHubFunnelProvider{listErr: tc.err}
			funnel := validGitHubFunnel(t, provider)
			_, err := funnel.DiscoverIssues(context.Background(), "")
			if !IsGitHubFunnelError(err, tc.kind) {
				t.Fatalf("provider failure was not typed %s: %v", tc.kind, err)
			}
		})
	}
	for _, config := range []GitHubFunnelConfig{
		{Repository: "not a repo"},
		{Repository: "Acme/Orchard", SelectedIssues: []int{0}},
		{Repository: "Acme/Orchard", IncludeLabels: []string{"ready", "READY"}},
		{Repository: "Acme/Orchard", Working: GitHubLabelMapping{Add: []string{"same"}, Remove: []string{"same"}}},
	} {
		if err := config.Validate(); err == nil {
			t.Fatalf("accepted invalid config: %+v", config)
		}
	}
}

func TestConfiguredGitHubFunnelImplementsNormalizedContract(t *testing.T) {
	provider := &fakeGitHubFunnelProvider{pages: []GitHubIssuePage{{Complete: true, Items: []GitHubIssue{{Number: 7, Title: "ready", State: "open", Revision: "r7", Labels: []string{"agent-ready"}}}}}, items: map[int]GitHubIssue{7: {Number: 7, Title: "ready", State: "open", Revision: "r7", Labels: []string{"agent-ready"}}}}
	config := FunnelConfig{ID: "github-ready", Provider: "github", Location: SourceLocation{"repository": "Acme/Orchard"}, Filter: SourceFilter{"include_labels": "agent-ready"}, Transitions: TransitionPolicy{TransitionWorking: {{Name: "labels", Parameters: map[string]string{"add": "agent-in-progress"}}}}, Enabled: true, PriorityPolicy: "operator-explicit"}
	adapter := NewConfiguredGitHubFunnel(config, provider)
	page, err := adapter.Discover(context.Background(), DiscoveryRequest{Funnel: config})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Identity.Item != "7" || !page.Items[0].Eligible || page.Items[0].Priority.Policy != "operator-explicit" {
		t.Fatalf("bad normalized page: %#v", page)
	}
	request := LifecycleRequest{Identity: page.Items[0].Identity, Action: ActionClaim, ExpectedRevision: "r7", IntentID: "intent-7"}
	result, err := adapter.Apply(context.Background(), request)
	if err != nil || result.Receipt == nil || len(provider.updateCalls) != 1 {
		t.Fatalf("normalized lifecycle failed: %#v %v", result, err)
	}
}

func TestGitHubClosedIssueIsDoneAndIneligible(t *testing.T) {
	config := FunnelConfig{ID: "github-done", Provider: "github", Location: SourceLocation{"repository": "Acme/Orchard"}, Enabled: true, ReadOnly: true, PriorityPolicy: "source-status"}
	for _, test := range []struct {
		state    string
		status   WorkStatus
		eligible bool
	}{
		{state: "closed", status: WorkClosed, eligible: false},
		{state: "CLOSED", status: WorkClosed, eligible: false},
		{state: "open", status: WorkQueued, eligible: true},
	} {
		item := githubWorkItem(config, GitHubFunnelItem{SourceID: "7", Title: "work", State: test.state, Revision: "r1"})
		if item.Status != test.status || item.Eligible != test.eligible {
			t.Fatalf("state %q became status=%s eligible=%v", test.state, item.Status, item.Eligible)
		}
	}
}

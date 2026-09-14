package town

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type slackFakeResponse struct {
	status int
	header http.Header
	body   string
}

type slackFakeHTTP struct {
	mu    sync.Mutex
	calls []struct {
		method string
		values url.Values
		token  string
	}
	responses map[string][]slackFakeResponse
	do        func(*http.Request, url.Values) (*http.Response, error)
}

func (f *slackFakeHTTP) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, struct {
		method string
		values url.Values
		token  string
	}{req.URL.Path, values, req.Header.Get("Authorization")})
	do := f.do
	response := slackFakeResponse{}
	if f.responses != nil && len(f.responses[req.URL.Path]) > 0 {
		response = f.responses[req.URL.Path][0]
		f.responses[req.URL.Path] = f.responses[req.URL.Path][1:]
	}
	f.mu.Unlock()
	if do != nil {
		return do(req, values)
	}
	if response.status == 0 {
		response.status = http.StatusOK
	}
	if response.header == nil {
		response.header = make(http.Header)
	}
	return &http.Response{StatusCode: response.status, Header: response.header, Body: io.NopCloser(strings.NewReader(response.body)), Request: req}, nil
}

func slackJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func slackFunnelForTest(fake *slackFakeHTTP, options SlackFunnelOptions) *SlackFunnel {
	options.HTTP = fake
	if options.BaseURL == "" {
		options.BaseURL = "http://slack.test/api"
	}
	if options.Resolve == nil {
		options.Resolve = func(context.Context) (SlackCredential, error) { return SlackCredential{Token: "test-token"}, nil }
	}
	return NewSlackFunnelWithOptions(options)
}

func TestSlackHistoryPaginatesAndResolvesCredentialPerRequest(t *testing.T) {
	fake := &slackFakeHTTP{responses: map[string][]slackFakeResponse{
		"/api/conversations.history": {
			{body: slackJSON(t, map[string]any{"ok": true, "messages": []SlackMessage{{TS: "3.000", Text: "new"}, {TS: "1.000", Text: "old"}}, "has_more": true, "response_metadata": map[string]string{"next_cursor": "next"}})},
			{body: slackJSON(t, map[string]any{"ok": true, "messages": []SlackMessage{{TS: "2.000", Text: "middle"}}, "has_more": false})},
		},
	}}
	credentialCalls := 0
	funnel := slackFunnelForTest(fake, SlackFunnelOptions{Resolve: func(context.Context) (SlackCredential, error) {
		credentialCalls++
		return SlackCredential{Token: "token-" + string(rune('a'+credentialCalls-1))}, nil
	}})
	history, err := funnel.History(context.Background(), "C123")
	if err != nil {
		t.Fatal(err)
	}
	if !history.Complete || history.Pages != 2 || len(history.Messages) != 3 {
		t.Fatalf("unexpected history: %+v", history)
	}
	if got := []string{history.Messages[0].TS, history.Messages[1].TS, history.Messages[2].TS}; strings.Join(got, ",") != "1.000,2.000,3.000" {
		t.Fatalf("history order: %v", got)
	}
	if credentialCalls != 2 {
		t.Fatalf("credential resolver called %d times, want 2", credentialCalls)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.calls[0].token != "Bearer token-a" || fake.calls[1].token != "Bearer token-b" {
		t.Fatalf("credentials were not resolved at call time: %+v", fake.calls)
	}
}

func TestSlackRefreshUsesWindowAndExactMarker(t *testing.T) {
	marker := "<!-- source:town-42 -->"
	fake := &slackFakeHTTP{responses: map[string][]slackFakeResponse{
		"/api/conversations.history": {
			{body: slackJSON(t, map[string]any{"ok": true, "messages": []SlackMessage{{TS: "10.000", Text: "old " + marker}, {TS: "11.000", Text: "unrelated"}}, "has_more": false})},
			{body: slackJSON(t, map[string]any{"ok": true, "messages": []SlackMessage{{TS: "11.000", Text: "unrelated"}, {TS: "12.000", Text: "new " + marker}}, "has_more": false})},
		},
	}}
	funnel := slackFunnelForTest(fake, SlackFunnelOptions{})
	first, err := funnel.DiscoverSlack(context.Background(), "C123", marker)
	if err != nil || !first.History.Complete || len(first.Matches) != 1 || first.Matches[0].TS != "10.000" {
		t.Fatalf("initial discovery: %+v, %v", first, err)
	}
	second, err := funnel.RefreshSlack(context.Background(), "C123", first)
	if err != nil || !second.History.Complete || len(second.Matches) != 1 || second.Matches[0].TS != "12.000" {
		t.Fatalf("refresh: %+v, %v", second, err)
	}
	fake.mu.Lock()
	values := fake.calls[1].values
	fake.mu.Unlock()
	if values.Get("oldest") != "11.000" {
		t.Fatalf("refresh did not use newest timestamp: %q", values.Get("oldest"))
	}
}

func TestSlackHistoryClassifiesPartialAndIncomplete(t *testing.T) {
	partial := &slackFakeHTTP{responses: map[string][]slackFakeResponse{
		"/api/conversations.history": {
			{body: slackJSON(t, map[string]any{"ok": true, "messages": []SlackMessage{{TS: "1.000", Text: "first"}}, "has_more": true, "response_metadata": map[string]string{"next_cursor": "next"}})},
			{status: http.StatusBadGateway, body: "upstream failed"},
		},
	}}
	funnel := slackFunnelForTest(partial, SlackFunnelOptions{})
	history, err := funnel.History(context.Background(), "C123")
	var partialErr *SlackPartialError
	if !errors.As(err, &partialErr) || partialErr.History.Complete || len(history.Messages) != 1 {
		t.Fatalf("expected partial history, got %+v, %v", history, err)
	}

	incomplete := &slackFakeHTTP{responses: map[string][]slackFakeResponse{
		"/api/conversations.history": {{body: slackJSON(t, map[string]any{"ok": true, "messages": []SlackMessage{{TS: "1.000"}}, "has_more": true})}},
	}}
	history, err = slackFunnelForTest(incomplete, SlackFunnelOptions{}).History(context.Background(), "C123")
	var incompleteErr *SlackIncompleteError
	if !errors.As(err, &incompleteErr) || history.Complete {
		t.Fatalf("expected incomplete history, got %+v, %v", history, err)
	}
}

func TestConfiguredSlackFunnelImplementsNormalizedDiscovery(t *testing.T) {
	fake := &slackFakeHTTP{responses: map[string][]slackFakeResponse{"/api/conversations.history": {{body: slackJSON(t, map[string]any{"ok": true, "messages": []SlackMessage{{TS: "10.000", Text: "[town] investigate", Type: "message"}}, "has_more": false})}}}}
	config := FunnelConfig{ID: "slack-ready", Provider: "slack", Location: SourceLocation{"channel": "C123"}, Filter: SourceFilter{"marker": "[town]"}, Enabled: true, ReadOnly: true, PriorityPolicy: "operator-explicit"}
	adapter := NewConfiguredSlackFunnel(config, fake, func(context.Context) (SlackCredential, error) {
		return SlackCredential{Token: "private-test-token"}, nil
	})
	page, err := adapter.Discover(context.Background(), DiscoveryRequest{Funnel: config})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Identity.Item != "10.000" || page.Items[0].Priority.Policy != "operator-explicit" || !page.Items[0].Eligible {
		t.Fatalf("bad normalized Slack page: %#v", page)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "private-test-token") {
		t.Fatal("credential leaked into normalized discovery")
	}
}

func TestSlackCheckReactionIsDoneAndIneligible(t *testing.T) {
	config := FunnelConfig{ID: "slack-ready", Provider: "slack", Location: SourceLocation{"channel": "C123"}, Filter: SourceFilter{"marker": "[town]"}, Enabled: true, ReadOnly: true, PriorityPolicy: "operator-explicit"}
	for _, test := range []struct {
		name      string
		reactions []SlackReaction
		status    WorkStatus
		eligible  bool
	}{
		{name: "white check", reactions: []SlackReaction{{Name: "construction", Count: 1}, {Name: "white_check_mark", Count: 1}}, status: WorkComplete, eligible: false},
		{name: "heavy check", reactions: []SlackReaction{{Name: "heavy_check_mark", Count: 2}}, status: WorkComplete, eligible: false},
		{name: "removed check", reactions: []SlackReaction{{Name: "white_check_mark", Count: 0}}, status: WorkQueued, eligible: true},
		{name: "other emoji", reactions: []SlackReaction{{Name: "thumbsup", Count: 1}}, status: WorkQueued, eligible: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := NewConfiguredSlackFunnel(config, &slackFakeHTTP{}, func(context.Context) (SlackCredential, error) { return SlackCredential{}, nil })
			item := adapter.slackWorkItem(config, SlackMessage{TS: "10.000", Type: "message", Text: "[town] investigate", Reactions: test.reactions}, "")
			if item.Status != test.status || item.Eligible != test.eligible {
				t.Fatalf("status=%s eligible=%v, want %s %v", item.Status, item.Eligible, test.status, test.eligible)
			}
			if item.Provenance.ExternalState != "message" {
				t.Fatalf("provider message type was not preserved: %#v", item.Provenance)
			}
		})
	}
}

func TestSlackAuthAndRateLimitAreTyped(t *testing.T) {
	for _, test := range []struct {
		name     string
		response slackFakeResponse
		want     any
	}{
		{name: "api auth", response: slackFakeResponse{body: `{"ok":false,"error":"invalid_auth"}`}, want: (*SlackAuthError)(nil)},
		{name: "http auth", response: slackFakeResponse{status: http.StatusUnauthorized, body: "nope"}, want: (*SlackAuthError)(nil)},
		{name: "http rate", response: slackFakeResponse{status: http.StatusTooManyRequests, header: http.Header{"Retry-After": []string{"7"}}, body: "slow down"}, want: (*SlackRateLimitError)(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &slackFakeHTTP{responses: map[string][]slackFakeResponse{"/api/conversations.history": {test.response}}}
			_, err := slackFunnelForTest(fake, SlackFunnelOptions{}).History(context.Background(), "C123")
			switch test.want.(type) {
			case *SlackAuthError:
				var typed *SlackAuthError
				if !errors.As(err, &typed) {
					t.Fatalf("error %T is not auth: %v", err, err)
				}
			case *SlackRateLimitError:
				var typed *SlackRateLimitError
				if !errors.As(err, &typed) || typed.RetryAfter != 7*time.Second {
					t.Fatalf("error %T is not rate limit: %v", err, err)
				}
			}
		})
	}
}

func TestSlackReadOnlyAndCapabilitiesBlockWrites(t *testing.T) {
	funnel := slackFunnelForTest(&slackFakeHTTP{}, SlackFunnelOptions{ReadOnly: true})
	result, err := funnel.ApplyLifecycle(context.Background(), SlackLifecycleEvent{Channel: "C123", MessageTS: "1.000", Lifecycle: SlackLifecycleCompleted})
	if !errors.Is(err, ErrSlackReadOnly) || result.Write.Status != SlackWriteBlocked {
		t.Fatalf("read-only write: %+v, %v", result, err)
	}

	funnel = slackFunnelForTest(&slackFakeHTTP{}, SlackFunnelOptions{Capabilities: SlackReadOnlyCapabilities})
	_, err = funnel.ApplyLifecycle(context.Background(), SlackLifecycleEvent{Channel: "C123", MessageTS: "1.000", Lifecycle: SlackLifecycleCompleted})
	var capabilityErr *SlackCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Capability != "reactions" {
		t.Fatalf("capability error: %v", err)
	}
}

func TestSlackLifecycleWritesStableMarkerAndReconcilesLostReply(t *testing.T) {
	marker := MarkerForOperation("event-42")
	fake := &slackFakeHTTP{}
	replyLost := true
	fake.do = func(req *http.Request, values url.Values) (*http.Response, error) {
		path := req.URL.Path
		switch path {
		case "/api/reactions.add":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: req}, nil
		case "/api/chat.postMessage":
			if replyLost {
				replyLost = false
				return nil, errors.New("connection lost after Slack accepted reply")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"ts":"2.000"}`)), Request: req}, nil
		case "/api/conversations.replies":
			body := map[string]any{"ok": true, "messages": []SlackMessage{{TS: "1.000", Text: "source" + marker}, {TS: "2.000", ThreadTS: "1.000", Text: "completed\n" + marker}}, "has_more": false}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(slackJSON(t, body))), Request: req}, nil
		default:
			t.Fatalf("unexpected Slack method %s", path)
			return nil, errors.New("unexpected method")
		}
	}
	funnel := slackFunnelForTest(fake, SlackFunnelOptions{})
	event := SlackLifecycleEvent{Channel: "C123", MessageTS: "1.000", OperationID: "event-42", Lifecycle: SlackLifecycleCompleted, Text: "completed"}
	result, err := funnel.ApplyLifecycle(context.Background(), event)
	var uncertain *SlackUncertainError
	if !errors.As(err, &uncertain) || result.Write.Status != SlackWriteUncertain || result.Write.Marker != marker {
		t.Fatalf("lost reply was not uncertain: %+v, %v", result, err)
	}
	receipt, err := funnel.ReconcileWrite(context.Background(), result.Write)
	if err != nil || !receipt.Found || !receipt.Complete || receipt.Write.Status != SlackWriteConfirmed {
		t.Fatalf("reconciliation: %+v, %v", receipt, err)
	}
	if receipt.Detail == "" {
		t.Fatal("missing reconciliation detail")
	}
	fake.mu.Lock()
	if len(fake.calls) != 3 {
		t.Fatalf("unexpected call count: %d", len(fake.calls))
	}
	reactionValues, replyValues := fake.calls[0].values, fake.calls[1].values
	fake.mu.Unlock()
	if reactionValues.Get("name") != "white_check_mark" || reactionValues.Get("client_msg_id") != marker {
		t.Fatalf("bad reaction mapping: %v", reactionValues)
	}
	if replyValues.Get("thread_ts") != "1.000" || !strings.Contains(replyValues.Get("text"), marker) || replyValues.Get("client_msg_id") != marker {
		t.Fatalf("bad thread reply: %v", replyValues)
	}
}

func TestMarkerForOperationIsStableAndNonSecret(t *testing.T) {
	first, second := MarkerForOperation("event-42"), MarkerForOperation("event-42")
	if first != second || !strings.HasPrefix(first, slackMarkerPrefix) || !strings.HasSuffix(first, slackMarkerSuffix) || strings.Contains(first, "event-42") {
		t.Fatalf("unstable or secret marker: %q %q", first, second)
	}
}

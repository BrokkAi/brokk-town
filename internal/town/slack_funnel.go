package town

// This file contains the small, deliberately boring Slack boundary used by
// Town's source-funnel proof.  It speaks the Slack Web API over an injected
// HTTP client.  Keeping the boundary here (rather than importing a Slack SDK)
// makes it possible to exercise the complete protocol with a fake server and
// keeps credentials out of Town state and snapshots.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSlackAPIURL    = "https://slack.com/api/"
	defaultSlackPageSize  = 100
	defaultSlackPageLimit = 1000
	slackMarkerPrefix     = "<!-- brokk-town-slack:"
	slackMarkerSuffix     = " -->"
)

// SlackHTTPDoer is intentionally the narrowest useful HTTP seam.  A
// *http.Client, a fake client, or a client with custom transport can be
// supplied by the caller.
type SlackHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// SlackCredential is private operational data.  It is resolved immediately
// before each request and is never copied into a funnel result or Town state.
type SlackCredential struct {
	Token string
}

// SlackCredentialResolver lets the application obtain a token from its
// private credential store at call time.  It must not be called at
// construction time: rotation and revocation should take effect on the next
// request without restarting Town.
type SlackCredentialResolver func(context.Context) (SlackCredential, error)

// SlackCapabilities describes the operations the caller has enabled.  Slack
// scope discovery is intentionally not guessed from names: these are explicit
// local capabilities, and the API remains authoritative for missing scopes.
type SlackCapabilities struct {
	History       bool `json:"history"`
	ThreadReplies bool `json:"thread_replies"`
	Reactions     bool `json:"reactions"`
	ThreadWrites  bool `json:"thread_writes"`
}

var SlackReadOnlyCapabilities = SlackCapabilities{History: true, ThreadReplies: true}
var SlackReadWriteCapabilities = SlackCapabilities{History: true, ThreadReplies: true, Reactions: true, ThreadWrites: true}

// SlackFunnelOptions configures the HTTP boundary.  BaseURL is useful for a
// local fake and is expected to end in a slash; it is normalized either way.
type SlackFunnelOptions struct {
	HTTP    SlackHTTPDoer
	Resolve SlackCredentialResolver
	BaseURL string
	// Config is optional for the low-level history API. Generic FunnelAdapter
	// methods require it so an identity can be mapped back to its channel.
	Config         FunnelConfig
	Capabilities   SlackCapabilities
	ReadOnly       bool
	PageSize       int
	PageLimit      int
	RequestTimeout time.Duration
}

// SlackFunnel performs read and (unless ReadOnly is set) lifecycle operations
// against Slack.  pending is only an in-memory aid for reconciliation; the
// stable marker in each write is the durable idempotency key.
type SlackFunnel struct {
	http           SlackHTTPDoer
	resolve        SlackCredentialResolver
	baseURL        string
	capabilities   SlackCapabilities
	readOnly       bool
	pageSize       int
	pageLimit      int
	requestTimeout time.Duration
	config         FunnelConfig

	mu      sync.Mutex
	pending map[string]SlackWrite
}

func NewSlackFunnel(client SlackHTTPDoer, resolve SlackCredentialResolver) *SlackFunnel {
	return NewSlackFunnelWithOptions(SlackFunnelOptions{HTTP: client, Resolve: resolve})
}

func NewSlackFunnelWithOptions(options SlackFunnelOptions) *SlackFunnel {
	client := options.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	base := options.BaseURL
	if base == "" {
		base = defaultSlackAPIURL
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	pageSize := options.PageSize
	if pageSize < 1 || pageSize > 200 {
		pageSize = defaultSlackPageSize
	}
	pageLimit := options.PageLimit
	if pageLimit < 1 {
		pageLimit = defaultSlackPageLimit
	}
	capabilities := options.Capabilities
	if capabilities == (SlackCapabilities{}) {
		if options.ReadOnly {
			capabilities = SlackReadOnlyCapabilities
		} else {
			capabilities = SlackReadWriteCapabilities
		}
	}
	return &SlackFunnel{
		http: client, resolve: options.Resolve, baseURL: base, capabilities: capabilities,
		readOnly: options.ReadOnly, pageSize: pageSize, pageLimit: pageLimit,
		requestTimeout: options.RequestTimeout, config: options.Config, pending: map[string]SlackWrite{},
	}
}

func (f *SlackFunnel) ReadOnly() bool { return f.readOnly }

func (f *SlackFunnel) SlackCapabilities() SlackCapabilities { return f.capabilities }

// SlackMessage is the subset of a Slack message needed by the funnel.  Extra
// fields are intentionally not retained, which avoids putting arbitrary Slack
// payloads in Town snapshots.
type SlackMessage struct {
	Type      string          `json:"type,omitempty"`
	TS        string          `json:"ts"`
	ThreadTS  string          `json:"thread_ts,omitempty"`
	Channel   string          `json:"channel,omitempty"`
	User      string          `json:"user,omitempty"`
	BotID     string          `json:"bot_id,omitempty"`
	Text      string          `json:"text"`
	Reactions []SlackReaction `json:"reactions,omitempty"`
}

type SlackReaction struct {
	Name  string   `json:"name"`
	Users []string `json:"users,omitempty"`
	Count int      `json:"count,omitempty"`
}

// SlackHistory is a complete, deterministically ordered history when Complete
// is true.  On a SlackPartialError it contains the pages that were safely
// decoded before the failure, while Complete remains false.
type SlackHistory struct {
	Channel  string
	Messages []SlackMessage
	Cursor   string
	Complete bool
	Pages    int
}

// SlackDiscovery is an explicitly source-scoped view.  Marker filtering is
// exact and is based on the stable Town marker, not on display names.
type SlackDiscovery struct {
	History SlackHistory
	Matches []SlackMessage
	Marker  string
}

// SlackLifecycle is the small lifecycle vocabulary mapped to Slack reaction
// names.  Unknown values are rejected instead of silently becoming a success.
type SlackLifecycle string

const (
	SlackLifecycleQueued       SlackLifecycle = "queued"
	SlackLifecycleStarted      SlackLifecycle = "started"
	SlackLifecycleCompleted    SlackLifecycle = "completed"
	SlackLifecycleFailed       SlackLifecycle = "failed"
	SlackLifecycleInconclusive SlackLifecycle = "inconclusive"
	SlackLifecycleUncertain    SlackLifecycle = "uncertain"
)

// SlackLifecycleEvent identifies one source message and one stable lifecycle
// transition.  Marker is the source marker supplied by the upstream funnel;
// OperationID may be a durable event ID, and is preferred for idempotency.
type SlackLifecycleEvent struct {
	Channel     string
	MessageTS   string
	Marker      string
	OperationID string
	Lifecycle   SlackLifecycle
	Text        string
}

type SlackWriteKind string

const (
	SlackReactionWrite SlackWriteKind = "reaction"
	SlackThreadWrite   SlackWriteKind = "thread_reply"
)

type SlackWriteStatus string

const (
	SlackWriteConfirmed      SlackWriteStatus = "confirmed"
	SlackWriteAlreadyPresent SlackWriteStatus = "already_present"
	SlackWriteUncertain      SlackWriteStatus = "uncertain"
	SlackWriteBlocked        SlackWriteStatus = "blocked"
)

// SlackWrite is safe to persist as an intent: it contains no token and its
// Marker is stable across retries and process restarts.
type SlackWrite struct {
	Kind        SlackWriteKind
	Status      SlackWriteStatus
	Channel     string
	MessageTS   string
	Marker      string
	Reaction    string
	Text        string
	OperationID string
	Detail      string
}

type SlackWriteResult struct {
	Write     SlackWrite
	MessageTS string
}

// SlackReconciliation reports what a read-only reconciliation found.  A
// missing receipt after a complete read is not a write success; the caller
// must decide whether to retry the saved intent.
type SlackReconciliation struct {
	Write    SlackWrite
	Found    bool
	Complete bool
	Detail   string
}

// SlackAPIError preserves Slack's machine-readable error while keeping typed
// classes for auth, rate limits, partial reads and incomplete reads.
type SlackAPIError struct {
	Method     string
	Code       string
	HTTPStatus int
	Detail     string
}

func (e *SlackAPIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("Slack %s: %s", e.Method, e.Detail)
	}
	return fmt.Sprintf("Slack %s: %s", e.Method, e.Code)
}

type SlackAuthError struct{ Cause error }

func (e *SlackAuthError) Error() string { return "Slack authentication failed" }
func (e *SlackAuthError) Unwrap() error { return e.Cause }

type SlackRateLimitError struct {
	Cause      error
	RetryAfter time.Duration
}

func (e *SlackRateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("Slack rate limit (retry after %s): %v", e.RetryAfter, e.Cause)
	}
	return "Slack rate limit: " + e.Cause.Error()
}
func (e *SlackRateLimitError) Unwrap() error { return e.Cause }

// SlackPartialError means a response stream contained useful decoded pages but
// a later page could not be trusted.  Callers may display the pages but must
// not treat them as a complete inventory.
type SlackPartialError struct {
	History SlackHistory
	Cause   error
}

func (e *SlackPartialError) Error() string {
	return fmt.Sprintf("Slack history is partial after %d pages: %v", e.History.Pages, e.Cause)
}
func (e *SlackPartialError) Unwrap() error { return e.Cause }

// SlackIncompleteError means the API itself did not provide enough pagination
// information to prove completeness, or the configured safety limit was hit.
type SlackIncompleteError struct {
	History SlackHistory
	Reason  string
}

func (e *SlackIncompleteError) Error() string { return "Slack history incomplete: " + e.Reason }

type SlackUncertainError struct {
	Write SlackWrite
	Cause error
}

func (e *SlackUncertainError) Error() string {
	return "Slack write outcome is uncertain: " + e.Cause.Error()
}
func (e *SlackUncertainError) Unwrap() error { return e.Cause }

type SlackCapabilityError struct {
	Capability string
}

func (e *SlackCapabilityError) Error() string {
	return "Slack capability is unavailable: " + e.Capability
}

var ErrSlackReadOnly = errors.New("Slack funnel is read-only")

func (f *SlackFunnel) require(capability string, enabled bool) error {
	if !enabled {
		return &SlackCapabilityError{Capability: capability}
	}
	return nil
}

func (f *SlackFunnel) resolveCredential(ctx context.Context) (SlackCredential, error) {
	if f.resolve == nil {
		return SlackCredential{}, &SlackAuthError{Cause: errors.New("no private credential resolver configured")}
	}
	credential, err := f.resolve(ctx)
	if err != nil {
		return SlackCredential{}, &SlackAuthError{Cause: err}
	}
	if strings.TrimSpace(credential.Token) == "" {
		return SlackCredential{}, &SlackAuthError{Cause: errors.New("credential resolver returned an empty token")}
	}
	return credential, nil
}

type slackEnvelope struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Messages []SlackMessage `json:"messages,omitempty"`
	HasMore  bool           `json:"has_more,omitempty"`
	Metadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata,omitempty"`
	Message SlackMessage `json:"message,omitempty"`
	TS      string       `json:"ts,omitempty"`
	Channel string       `json:"channel,omitempty"`
}

func (f *SlackFunnel) call(ctx context.Context, method string, values url.Values, out *slackEnvelope) (*http.Response, error) {
	credential, err := f.resolveCredential(ctx)
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = url.Values{}
	}
	body := bytes.NewBufferString(values.Encode())
	requestCtx := ctx
	var cancel context.CancelFunc
	if f.requestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, f.requestTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, f.baseURL+method, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential.Token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := f.http.Do(req)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("Slack returned no HTTP response")
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if readErr != nil {
		return response, readErr
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return response, &SlackRateLimitError{Cause: errors.New("HTTP 429"), RetryAfter: slackRetryAfter(response.Header.Get("Retry-After"))}
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return response, &SlackAuthError{Cause: fmt.Errorf("HTTP %d", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, &SlackAPIError{Method: method, HTTPStatus: response.StatusCode, Code: "http_error", Detail: fmt.Sprintf("HTTP %d", response.StatusCode)}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return response, fmt.Errorf("decode Slack %s response: %w", method, err)
	}
	if !out.OK {
		apiErr := &SlackAPIError{Method: method, Code: out.Error, Detail: out.Error}
		switch strings.ToLower(out.Error) {
		case "invalid_auth", "not_authed", "token_revoked", "account_inactive", "missing_scope", "invalid_token":
			return response, &SlackAuthError{Cause: apiErr}
		case "ratelimited", "rate_limited":
			return response, &SlackRateLimitError{Cause: apiErr}
		default:
			return response, apiErr
		}
	}
	return response, nil
}

func slackRetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 1 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func (f *SlackFunnel) historyPage(ctx context.Context, channel, cursor, oldest, latest string) (slackEnvelope, error) {
	values := url.Values{}
	values.Set("channel", channel)
	values.Set("limit", strconv.Itoa(f.pageSize))
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	if oldest != "" {
		values.Set("oldest", oldest)
	}
	if latest != "" {
		values.Set("latest", latest)
	}
	var page slackEnvelope
	_, err := f.call(ctx, "conversations.history", values, &page)
	return page, err
}

// History discovers all history pages and proves completeness before
// returning it.  It never returns an apparently complete prefix.
func (f *SlackFunnel) History(ctx context.Context, channel string) (SlackHistory, error) {
	return f.history(ctx, channel, "", "", "")
}

func (f *SlackFunnel) history(ctx context.Context, channel, cursor, oldest, latest string) (SlackHistory, error) {
	if err := f.require("history", f.capabilities.History); err != nil {
		return SlackHistory{}, err
	}
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return SlackHistory{}, errors.New("Slack channel is required")
	}
	history := SlackHistory{Channel: channel}
	seenCursors := map[string]bool{}
	for pageNumber := 1; pageNumber <= f.pageLimit; pageNumber++ {
		if cursor != "" && seenCursors[cursor] {
			return history, &SlackIncompleteError{History: history, Reason: "Slack repeated a pagination cursor"}
		}
		if cursor != "" {
			seenCursors[cursor] = true
		}
		page, err := f.historyPage(ctx, channel, cursor, oldest, latest)
		history.Pages = pageNumber
		if err != nil {
			if len(history.Messages) > 0 {
				return history, &SlackPartialError{History: history, Cause: err}
			}
			return history, err
		}
		history.Messages = append(history.Messages, page.Messages...)
		cursor = strings.TrimSpace(page.Metadata.NextCursor)
		if !page.HasMore && cursor == "" {
			history.Cursor = ""
			history.Complete = true
			return normalizeSlackHistory(history), nil
		}
		if cursor == "" {
			return normalizeSlackHistory(history), &SlackIncompleteError{History: normalizeSlackHistory(history), Reason: "Slack reported more history without a next cursor"}
		}
		history.Cursor = cursor
	}
	return normalizeSlackHistory(history), &SlackIncompleteError{History: normalizeSlackHistory(history), Reason: "pagination safety limit reached"}
}

func normalizeSlackHistory(history SlackHistory) SlackHistory {
	byTS := map[string]SlackMessage{}
	for _, message := range history.Messages {
		if strings.TrimSpace(message.TS) == "" {
			continue
		}
		byTS[message.TS] = message
	}
	history.Messages = history.Messages[:0]
	for _, message := range byTS {
		history.Messages = append(history.Messages, message)
	}
	sort.SliceStable(history.Messages, func(i, j int) bool {
		return slackTimestamp(history.Messages[i].TS) < slackTimestamp(history.Messages[j].TS)
	})
	return history
}

func slackTimestamp(value string) float64 {
	parts := strings.SplitN(value, ".", 2)
	seconds, _ := strconv.ParseFloat(parts[0], 64)
	if len(parts) == 1 {
		return seconds
	}
	fraction, _ := strconv.ParseFloat("0."+parts[1], 64)
	return seconds + fraction
}

// Discover performs paginated history discovery and exact marker matching.
// marker may be supplied with or without the HTML comment delimiters.
func (f *SlackFunnel) DiscoverSlack(ctx context.Context, channel, marker string) (SlackDiscovery, error) {
	history, err := f.History(ctx, channel)
	result := SlackDiscovery{History: history, Marker: marker}
	for _, message := range history.Messages {
		if marker == "" || strings.Contains(message.Text, marker) {
			result.Matches = append(result.Matches, message)
		}
	}
	return result, err
}

// Refresh re-reads the history window represented by previous.  The newest
// observed timestamp is used as oldest, and duplicate messages are removed by
// normalizeSlackHistory.  An empty prior result performs a full discovery.
func (f *SlackFunnel) RefreshSlack(ctx context.Context, channel string, previous SlackDiscovery) (SlackDiscovery, error) {
	if previous.History.Channel == "" || len(previous.History.Messages) == 0 {
		return f.DiscoverSlack(ctx, channel, previous.Marker)
	}
	newest := previous.History.Messages[len(previous.History.Messages)-1].TS
	history, err := f.history(ctx, channel, "", newest, "")
	result := SlackDiscovery{History: history, Marker: previous.Marker}
	for _, message := range history.Messages {
		if previous.Marker == "" || strings.Contains(message.Text, previous.Marker) {
			result.Matches = append(result.Matches, message)
		}
	}
	return result, err
}

// ThreadReplies retrieves a source message's current thread.  It is kept
// separate from History because Slack exposes thread replies through another
// paginated endpoint.
func (f *SlackFunnel) ThreadReplies(ctx context.Context, channel, threadTS string) (SlackHistory, error) {
	if err := f.require("thread_replies", f.capabilities.ThreadReplies); err != nil {
		return SlackHistory{}, err
	}
	if strings.TrimSpace(channel) == "" || strings.TrimSpace(threadTS) == "" {
		return SlackHistory{}, errors.New("Slack channel and thread timestamp are required")
	}
	history := SlackHistory{Channel: channel}
	var cursor string
	seenCursors := map[string]bool{}
	for pageNumber := 1; pageNumber <= f.pageLimit; pageNumber++ {
		if cursor != "" && seenCursors[cursor] {
			return history, &SlackIncompleteError{History: history, Reason: "Slack repeated a thread cursor"}
		}
		if cursor != "" {
			seenCursors[cursor] = true
		}
		values := url.Values{"channel": []string{channel}, "ts": []string{threadTS}, "limit": []string{strconv.Itoa(f.pageSize)}}
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		var page slackEnvelope
		_, err := f.call(ctx, "conversations.replies", values, &page)
		history.Pages = pageNumber
		if err != nil {
			if len(history.Messages) > 0 {
				return history, &SlackPartialError{History: history, Cause: err}
			}
			return history, err
		}
		// replies returns the parent as the first message on each independent
		// call; deduplication below makes that harmless.
		history.Messages = append(history.Messages, page.Messages...)
		cursor = strings.TrimSpace(page.Metadata.NextCursor)
		if !page.HasMore && cursor == "" {
			history.Complete = true
			history.Cursor = ""
			return normalizeSlackHistory(history), nil
		}
		if cursor == "" {
			return normalizeSlackHistory(history), &SlackIncompleteError{History: history, Reason: "Slack reported more replies without a next cursor"}
		}
		history.Cursor = cursor
	}
	return normalizeSlackHistory(history), &SlackIncompleteError{History: history, Reason: "thread pagination safety limit reached"}
}

// MarkerForOperation gives callers a stable, non-secret marker suitable for a
// persisted write intent.  The operation ID is deliberately included in the
// digest, so retries with the same event cannot create a second reply.
func MarkerForOperation(operationID string) string {
	hash := sha256.Sum256([]byte(operationID))
	return slackMarkerPrefix + hex.EncodeToString(hash[:])[:24] + slackMarkerSuffix
}

func slackMarker(operationID, fallback string) string {
	if strings.TrimSpace(operationID) == "" {
		operationID = fallback
	}
	if strings.HasPrefix(operationID, slackMarkerPrefix) && strings.HasSuffix(operationID, slackMarkerSuffix) {
		return operationID
	}
	return MarkerForOperation(operationID)
}

var slackReactionForLifecycle = map[SlackLifecycle]string{
	SlackLifecycleQueued:       "hourglass_flowing_sand",
	SlackLifecycleStarted:      "construction",
	SlackLifecycleCompleted:    "white_check_mark",
	SlackLifecycleFailed:       "x",
	SlackLifecycleInconclusive: "question",
	SlackLifecycleUncertain:    "warning",
}

func (f *SlackFunnel) mapLifecycle(event SlackLifecycleEvent) (SlackWrite, error) {
	if strings.TrimSpace(event.Channel) == "" {
		return SlackWrite{}, errors.New("Slack channel is required")
	}
	if event.MessageTS == "" && event.Marker == "" {
		return SlackWrite{}, errors.New("Slack source message or marker is required")
	}
	reaction, ok := slackReactionForLifecycle[event.Lifecycle]
	if !ok {
		return SlackWrite{}, fmt.Errorf("unsupported Slack lifecycle %q", event.Lifecycle)
	}
	operationID := event.OperationID
	if operationID == "" {
		operationID = strings.Join([]string{event.Channel, event.MessageTS, event.Marker, string(event.Lifecycle), event.Text}, "\x00")
	}
	marker := slackMarker(operationID, event.Marker)
	return SlackWrite{Kind: SlackReactionWrite, Status: SlackWriteConfirmed, Channel: event.Channel, MessageTS: event.MessageTS, Marker: marker, Reaction: reaction, OperationID: operationID}, nil
}

// ApplyLifecycle maps one Town lifecycle event to a reaction and a thread
// reply.  Both writes share the same stable operation marker, and an API
// outcome that could have been lost is surfaced as SlackUncertainError.
func (f *SlackFunnel) ApplyLifecycle(ctx context.Context, event SlackLifecycleEvent) (SlackWriteResult, error) {
	if f.readOnly {
		return SlackWriteResult{Write: SlackWrite{Status: SlackWriteBlocked}}, ErrSlackReadOnly
	}
	if err := f.require("reactions", f.capabilities.Reactions); err != nil {
		return SlackWriteResult{}, err
	}
	if err := f.require("thread_writes", f.capabilities.ThreadWrites); err != nil {
		return SlackWriteResult{}, err
	}
	write, err := f.mapLifecycle(event)
	if err != nil {
		return SlackWriteResult{}, err
	}
	if write.MessageTS == "" {
		discovery, discoveryErr := f.DiscoverSlack(ctx, event.Channel, event.Marker)
		if discoveryErr != nil {
			return SlackWriteResult{Write: write}, discoveryErr
		}
		if len(discovery.Matches) != 1 {
			return SlackWriteResult{Write: SlackWrite{Status: SlackWriteBlocked, Marker: write.Marker}}, fmt.Errorf("Slack source marker matched %d messages; expected exactly one", len(discovery.Matches))
		}
		write.MessageTS = discovery.Matches[0].TS
	}
	text := strings.TrimSpace(event.Text)
	if text == "" {
		text = string(event.Lifecycle)
	}
	text = text + "\n" + write.Marker

	f.mu.Lock()
	if prior, ok := f.pending[write.Marker]; ok {
		if prior.Channel != write.Channel || prior.MessageTS != write.MessageTS || prior.Reaction != write.Reaction || prior.Text != text {
			f.mu.Unlock()
			return SlackWriteResult{Write: SlackWrite{Status: SlackWriteBlocked, Marker: write.Marker}}, errors.New("Slack operation marker already belongs to different content")
		}
	}
	f.pending[write.Marker] = SlackWrite{Kind: SlackThreadWrite, Status: SlackWriteUncertain, Channel: write.Channel, MessageTS: write.MessageTS, Marker: write.Marker, Reaction: write.Reaction, Text: text, OperationID: write.OperationID}
	f.mu.Unlock()

	if err := f.addReaction(ctx, write.Channel, write.MessageTS, write.Reaction, write.Marker); err != nil {
		return f.uncertain(write, err)
	}
	threadWrite := SlackWrite{Kind: SlackThreadWrite, Status: SlackWriteConfirmed, Channel: write.Channel, MessageTS: write.MessageTS, Marker: write.Marker, Reaction: write.Reaction, Text: text, OperationID: write.OperationID}
	if err := f.postThreadReply(ctx, threadWrite); err != nil {
		return f.uncertain(threadWrite, err)
	}
	threadWrite.Status = SlackWriteConfirmed
	f.mu.Lock()
	delete(f.pending, write.Marker)
	f.mu.Unlock()
	return SlackWriteResult{Write: threadWrite}, nil
}

func (f *SlackFunnel) addReaction(ctx context.Context, channel, timestamp, reaction, marker string) error {
	values := url.Values{"channel": []string{channel}, "timestamp": []string{timestamp}, "name": []string{reaction}, "client_msg_id": []string{marker}}
	_, err := f.call(ctx, "reactions.add", values, &slackEnvelope{})
	return err
}

func (f *SlackFunnel) postThreadReply(ctx context.Context, write SlackWrite) error {
	values := url.Values{"channel": []string{write.Channel}, "thread_ts": []string{write.MessageTS}, "text": []string{write.Text}, "client_msg_id": []string{write.Marker}}
	var response slackEnvelope
	_, err := f.call(ctx, "chat.postMessage", values, &response)
	if err != nil {
		return err
	}
	if strings.TrimSpace(response.TS) == "" && strings.TrimSpace(response.Message.TS) == "" {
		return errors.New("Slack reply response omitted message timestamp")
	}
	return nil
}

func (f *SlackFunnel) uncertain(write SlackWrite, cause error) (SlackWriteResult, error) {
	write.Status = SlackWriteUncertain
	write.Detail = "Inspect Slack with the stable marker before retrying this write."
	f.mu.Lock()
	f.pending[write.Marker] = write
	f.mu.Unlock()
	err := &SlackUncertainError{Write: write, Cause: cause}
	return SlackWriteResult{Write: write}, err
}

// Reconcile reads the source thread and checks the stable marker.  This method
// is intentionally read-only and never retries an uncertain write itself.
func (f *SlackFunnel) reconcileSlack(ctx context.Context, write SlackWrite) (SlackReconciliation, error) {
	if err := f.require("thread_replies", f.capabilities.ThreadReplies); err != nil {
		return SlackReconciliation{Write: write}, err
	}
	history, err := f.ThreadReplies(ctx, write.Channel, write.MessageTS)
	result := SlackReconciliation{Write: write, Complete: history.Complete}
	for _, message := range history.Messages {
		if strings.Contains(message.Text, write.Marker) {
			result.Found, result.Detail = true, "Slack receipt found by stable client marker."
			write.Status = SlackWriteConfirmed
			result.Write = write
			f.mu.Lock()
			delete(f.pending, write.Marker)
			f.mu.Unlock()
			return result, err
		}
	}
	if err != nil {
		return result, err
	}
	result.Detail = "No Slack receipt found in a complete thread read; write remains unconfirmed."
	return result, nil
}

// ReconcileWrite is an alias with an explicit name for callers that model
// uncertain writes as durable intents.
func (f *SlackFunnel) ReconcileWrite(ctx context.Context, write SlackWrite) (SlackReconciliation, error) {
	return f.reconcileSlack(ctx, write)
}

// NewConfiguredSlackFunnel binds the generic source-funnel configuration to
// the Slack transport. The low-level constructor remains useful for a caller
// that only needs history discovery.
func NewConfiguredSlackFunnel(config FunnelConfig, client SlackHTTPDoer, resolve SlackCredentialResolver) *SlackFunnel {
	return NewSlackFunnelWithOptions(SlackFunnelOptions{Config: config, HTTP: client, Resolve: resolve, ReadOnly: config.ReadOnly})
}

func (f *SlackFunnel) Provider() ProviderID { return ProviderID("slack") }

func (f *SlackFunnel) Validate(config FunnelConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if config.Provider != f.Provider() {
		return fmt.Errorf("Slack funnel provider must be %q", f.Provider())
	}
	if _, err := slackChannelFromConfig(config); err != nil {
		return err
	}
	if marker := slackMarkerFromConfig(config); strings.ContainsAny(marker, "\x00\r\n") {
		return errors.New("Slack funnel marker contains a control character")
	}
	return nil
}

func slackChannelFromConfig(config FunnelConfig) (string, error) {
	for _, key := range []string{"channel", "channel_id", "conversation"} {
		if value := strings.TrimSpace(config.Location[key]); value != "" {
			return value, nil
		}
	}
	return "", errors.New("Slack funnel location requires a channel")
}

func slackMarkerFromConfig(config FunnelConfig) string {
	for _, key := range []string{"marker", "text_contains", "source_marker"} {
		if value := strings.TrimSpace(config.Filter[key]); value != "" {
			return value
		}
	}
	return ""
}

func (f *SlackFunnel) configured() (FunnelConfig, error) {
	if err := f.Validate(f.config); err != nil {
		return FunnelConfig{}, err
	}
	return f.config, nil
}

func slackGenericError(err error) error {
	if err == nil {
		return nil
	}
	var auth *SlackAuthError
	if errors.As(err, &auth) {
		return errors.Join(err, &AuthenticationError{Provider: ProviderID("slack"), Reason: auth.Error()})
	}
	var rate *SlackRateLimitError
	if errors.As(err, &rate) {
		return errors.Join(err, &RateLimitError{RetryAfter: rate.RetryAfter, Reason: rate.Error()})
	}
	var partial *SlackPartialError
	if errors.As(err, &partial) {
		return errors.Join(err, &PartialError{Reason: partial.Error(), Completed: len(partial.History.Messages), Cursor: Cursor(partial.History.Cursor)})
	}
	var incomplete *SlackIncompleteError
	if errors.As(err, &incomplete) {
		return errors.Join(err, &IncompleteError{Reason: incomplete.Reason, Cursor: Cursor(incomplete.History.Cursor), Covered: incomplete.History.Complete})
	}
	var uncertain *SlackUncertainError
	if errors.As(err, &uncertain) {
		return errors.Join(err, &UncertainError{IntentID: uncertain.Write.OperationID, Reason: uncertain.Error()})
	}
	var capability *SlackCapabilityError
	if errors.As(err, &capability) {
		return errors.Join(err, &UnsupportedError{Reason: capability.Error()})
	}
	return err
}

func slackOutcome(err error) Outcome {
	return OutcomeFromError(slackGenericError(err))
}

func slackMessageRevision(message SlackMessage) Revision {
	return Revision(Digest(struct {
		TS        string
		ThreadTS  string
		Text      string
		User      string
		Reactions []SlackReaction
	}{message.TS, message.ThreadTS, message.Text, message.User, message.Reactions}))
}

func slackMessageTitle(message SlackMessage) string {
	text := strings.TrimSpace(strings.SplitN(message.Text, "\n", 2)[0])
	if text == "" {
		text = "Slack message " + message.TS
	}
	if len(text) > 1000 {
		text = text[:1000]
	}
	return text
}

func slackMessageStatus(message SlackMessage) WorkStatus {
	// Completion wins regardless of reaction order. Slack does not promise an
	// ordering that would make a construction reaction more authoritative than
	// a later check mark.
	for _, reaction := range message.Reactions {
		if reaction.Count != 0 && (strings.EqualFold(reaction.Name, "white_check_mark") || strings.EqualFold(reaction.Name, "heavy_check_mark")) {
			return WorkComplete
		}
	}
	for _, reaction := range message.Reactions {
		switch strings.ToLower(reaction.Name) {
		case "construction":
			return WorkWorking
		case "x", "no_entry", "warning":
			return WorkBlocked
		case "question":
			return WorkNeedsHuman
		}
	}
	return WorkQueued
}

func (f *SlackFunnel) slackWorkItem(config FunnelConfig, message SlackMessage, cursor string) WorkItem {
	identity := WorkIdentity{Funnel: config.ID, Provider: f.Provider(), Item: SourceItemID(message.TS)}
	status := slackMessageStatus(message)
	eligible := !status.Terminal()
	eligibility := "matched configured Slack marker"
	if !eligible {
		eligibility = "done reaction observed at source"
	}
	return WorkItem{
		Identity: identity, Title: slackMessageTitle(message), Body: message.Text,
		Status: status, Eligible: eligible, Eligibility: eligibility,
		Priority:     Priority{Policy: config.PriorityPolicy},
		Provenance:   Provenance{Identity: identity, ObservedAt: time.Now(), Revision: slackMessageRevision(message), Cursor: Cursor(cursor), ExternalState: message.Type},
		Capabilities: f.capabilitySet(config),
	}
}

func (f *SlackFunnel) capabilitySet(config FunnelConfig) CapabilitySet {
	actions := []struct {
		action     CapabilityAction
		transition WorkTransition
	}{
		{ActionClaim, TransitionWorking},
		{ActionReportBlocked, TransitionBlocked},
		{ActionRequestHuman, TransitionHuman},
		{ActionReportComplete, TransitionComplete},
	}
	out := make(CapabilitySet, 0, len(actions))
	readOnly := f.readOnly || config.ReadOnly
	for _, entry := range actions {
		capability := Capability{Action: entry.action}
		mapping, configured := config.Transitions[entry.transition]
		switch {
		case readOnly:
			capability.State, capability.Reason = CapabilityReadOnly, "Slack funnel is configured read-only"
		case !configured:
			capability.State, capability.Reason = CapabilityUnsupported, "no Slack transition mapping is configured"
		case len(mapping) == 0:
			capability.State, capability.Reason = CapabilityReadOnly, "transition is explicitly disabled"
		case !f.capabilities.Reactions || !f.capabilities.ThreadWrites:
			capability.State, capability.Reason = CapabilityUnsupported, "Slack reaction and thread-write capabilities are unavailable"
		default:
			capability.State = CapabilitySupported
			capability.Mapping = "reaction + thread reply"
		}
		out = append(out, capability)
	}
	return out
}

// Capabilities reports normalized actions without contacting Slack. Provider
// authorization remains call-time, while read-only and configuration facts are
// safe to show to an operator immediately.
func (f *SlackFunnel) Capabilities(_ context.Context, request CapabilityRequest) (CapabilitySet, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if err := f.Validate(request.Funnel); err != nil {
		return nil, err
	}
	return f.capabilitySet(request.Funnel), nil
}

func (f *SlackFunnel) genericDiscovery(ctx context.Context, request DiscoveryRequest) (DiscoveryPage, error) {
	if err := request.Validate(); err != nil {
		return DiscoveryPage{}, err
	}
	if err := f.Validate(request.Funnel); err != nil {
		return DiscoveryPage{}, err
	}
	channel, _ := slackChannelFromConfig(request.Funnel)
	discovery, err := f.DiscoverSlack(ctx, channel, slackMarkerFromConfig(request.Funnel))
	page := DiscoveryPage{Funnel: request.Funnel.ID, Provider: f.Provider(), Cursor: request.Cursor, NextCursor: Cursor(discovery.History.Cursor), Complete: discovery.History.Complete && err == nil, ObservedAt: time.Now()}
	for _, message := range discovery.Matches {
		page.Items = append(page.Items, f.slackWorkItem(request.Funnel, message, discovery.History.Cursor))
	}
	if err != nil {
		page.Complete = false
		page.Outcome = slackOutcome(err)
		return page, slackGenericError(err)
	}
	page.Outcome = Outcome{Kind: OutcomeComplete, Covered: page.Complete}
	if !page.Complete {
		page.Outcome = Outcome{Kind: OutcomeIncomplete, Detail: "Slack did not establish complete history coverage", Cursor: page.NextCursor, Covered: false}
		return page, &IncompleteError{Reason: "Slack did not establish complete history coverage", Cursor: page.NextCursor, Covered: false}
	}
	return page, nil
}

// Discover implements FunnelAdapter. It deliberately returns safely decoded
// items alongside partial/incomplete outcomes, but Complete is false unless
// every history page was proved.
func (f *SlackFunnel) Discover(ctx context.Context, request DiscoveryRequest) (DiscoveryPage, error) {
	return f.genericDiscovery(ctx, request)
}

func (f *SlackFunnel) genericRefresh(ctx context.Context, request RefreshRequest) (RefreshResult, error) {
	if err := request.Validate(); err != nil {
		return RefreshResult{}, err
	}
	if err := f.Validate(request.Funnel); err != nil {
		return RefreshResult{}, err
	}
	channel, _ := slackChannelFromConfig(request.Funnel)
	history, err := f.ThreadReplies(ctx, channel, string(request.Identity.Item))
	if err != nil {
		return RefreshResult{Outcome: slackOutcome(err), Refreshed: time.Now()}, slackGenericError(err)
	}
	var found *SlackMessage
	for i := range history.Messages {
		message := history.Messages[i]
		if message.TS == string(request.Identity.Item) {
			found = &message
			break
		}
	}
	if found == nil {
		found = &SlackMessage{TS: string(request.Identity.Item), Text: "Slack source message was not returned"}
		failure := &IncompleteError{Reason: "Slack source message was not found in a complete thread read", Cursor: Cursor(history.Cursor), Covered: history.Complete}
		return RefreshResult{Item: f.slackWorkItem(request.Funnel, *found, history.Cursor), Outcome: OutcomeFromError(failure), Refreshed: time.Now()}, failure
	}
	item := f.slackWorkItem(request.Funnel, *found, history.Cursor)
	if request.Revision != "" && request.Revision != item.Provenance.Revision {
		failure := &UncertainError{Reason: "Slack source revision changed before refresh"}
		return RefreshResult{Item: item, Outcome: OutcomeFromError(failure), Refreshed: time.Now()}, failure
	}
	return RefreshResult{Item: item, Outcome: Outcome{Kind: OutcomeComplete, Covered: history.Complete}, Refreshed: time.Now()}, nil
}

func (f *SlackFunnel) Refresh(ctx context.Context, request RefreshRequest) (RefreshResult, error) {
	return f.genericRefresh(ctx, request)
}

func slackTransitionForAction(action CapabilityAction) (SlackLifecycle, WorkTransition, error) {
	switch action {
	case ActionClaim:
		return SlackLifecycleStarted, TransitionWorking, nil
	case ActionReportBlocked:
		return SlackLifecycleFailed, TransitionBlocked, nil
	case ActionRequestHuman:
		return SlackLifecycleInconclusive, TransitionHuman, nil
	case ActionReportComplete:
		return SlackLifecycleCompleted, TransitionComplete, nil
	default:
		return "", "", &UnsupportedError{Action: action, Reason: "Slack has no mapping for this lifecycle action"}
	}
}

func (f *SlackFunnel) Apply(ctx context.Context, request LifecycleRequest) (LifecycleResult, error) {
	if err := request.Validate(); err != nil {
		return LifecycleResult{}, err
	}
	config, err := f.configured()
	if err != nil {
		return LifecycleResult{}, err
	}
	if request.Identity.Funnel != config.ID || request.Identity.Provider != f.Provider() {
		return LifecycleResult{}, errors.New("lifecycle identity does not belong to Slack funnel")
	}
	if f.readOnly || config.ReadOnly {
		failure := &UnsupportedError{Action: request.Action, Reason: "Slack funnel is read-only"}
		return LifecycleResult{Outcome: OutcomeFromError(failure)}, failure
	}
	lifecycle, transition, err := slackTransitionForAction(request.Action)
	if err != nil {
		return LifecycleResult{Outcome: OutcomeFromError(err)}, err
	}
	if mapping, ok := config.Transitions[transition]; !ok || len(mapping) == 0 {
		failure := &UnsupportedError{Action: request.Action, Reason: "Slack transition mapping is not configured"}
		return LifecycleResult{Outcome: OutcomeFromError(failure)}, failure
	}
	result, applyErr := f.ApplyLifecycle(ctx, SlackLifecycleEvent{Channel: mustSlackChannel(config), MessageTS: string(request.Identity.Item), OperationID: request.IntentID, Lifecycle: lifecycle, Text: string(request.Action)})
	if applyErr != nil {
		mapped := slackGenericError(applyErr)
		return LifecycleResult{Outcome: slackOutcome(applyErr)}, mapped
	}
	receipt := &WriteReceipt{IntentID: request.IntentID, ProviderID: result.Write.Marker, Identity: request.Identity, Revision: request.ExpectedRevision, Cursor: request.Cursor, ConfirmedAt: time.Now(), Detail: "Slack reaction and thread reply confirmed"}
	return LifecycleResult{Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, Receipt: receipt}, nil
}

func mustSlackChannel(config FunnelConfig) string {
	channel, _ := slackChannelFromConfig(config)
	return channel
}

func (f *SlackFunnel) Reconcile(ctx context.Context, request ReconcileRequest) (ReconciliationResult, error) {
	if err := request.Validate(); err != nil {
		return ReconciliationResult{}, err
	}
	config, err := f.configured()
	if err != nil {
		return ReconciliationResult{}, err
	}
	intent := request.Intent
	if intent.Identity.Funnel != config.ID || intent.Identity.Provider != f.Provider() {
		return ReconciliationResult{}, errors.New("reconciliation identity does not belong to Slack funnel")
	}
	write := SlackWrite{Kind: SlackThreadWrite, Status: SlackWriteUncertain, Channel: mustSlackChannel(config), MessageTS: string(intent.Identity.Item), Marker: MarkerForOperation(intent.ID), OperationID: intent.ID}
	receipt, reconcileErr := f.ReconcileWrite(ctx, write)
	if reconcileErr != nil {
		return ReconciliationResult{Status: ReconciliationUncertain, Outcome: slackOutcome(reconcileErr), Detail: reconcileErr.Error()}, slackGenericError(reconcileErr)
	}
	if !receipt.Complete {
		failure := &UncertainError{IntentID: intent.ID, Reason: "Slack thread read was not complete"}
		return ReconciliationResult{Status: ReconciliationUncertain, Outcome: OutcomeFromError(failure), Detail: failure.Error()}, failure
	}
	if !receipt.Found {
		return ReconciliationResult{Status: ReconciliationNotFound, Outcome: Outcome{Kind: OutcomeUncertain, IntentID: intent.ID, Covered: false}, Detail: receipt.Detail}, nil
	}
	confirmed := receipt.Write
	return ReconciliationResult{
		Status:  ReconciliationConfirmed,
		Outcome: Outcome{Kind: OutcomeComplete, Covered: true, IntentID: intent.ID},
		Receipt: &WriteReceipt{IntentID: intent.ID, ProviderID: confirmed.Marker, Identity: intent.Identity, Revision: intent.ExpectedRevision, Cursor: intent.Cursor, ConfirmedAt: time.Now(), Detail: receipt.Detail},
	}, nil
}

package mjolnir

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxDiffBytes = 16 << 20
const maxFileBytes = 16 << 20
const maxBundleBytes = 32 << 20
const transcriptPageSize = 200

// ArtifactError preserves refusal versus failure without retaining the daemon's
// error body, which may contain paths, credentials or subprocess output.
type ArtifactError struct {
	Artifact string
	Status   int
}

func (e *ArtifactError) Error() string {
	switch e.Status {
	case http.StatusNotFound:
		return "Mjolnir " + e.Artifact + " is missing; retain the run for reconciliation."
	case http.StatusConflict:
		return "Mjolnir refused " + e.Artifact + "; check the session state, checkout and worker capabilities in Mjolnir."
	default:
		return fmt.Sprintf("Mjolnir %s failed with HTTP %d; retain the run and check the daemon.", e.Artifact, e.Status)
	}
}

// SessionIdentity must come from a saved launch receipt, not a matching title or
// a current catalog selection. It identifies the session, not its runtime pin or
// exact repository checkout; those still require separate evidence.
type SessionIdentity struct {
	Session   string `json:"session_id"`
	Workspace string `json:"workspace_id"`
	Bundle    string `json:"bundle_id"`
	Selection
}

func (s SessionIdentity) validate() error {
	if !validSessionID(s.Session) || !ValidID(s.Workspace) || !ValidID(s.Bundle) || !s.Managed() || s.Selection.Validate() != nil {
		return errors.New("Mjolnir evidence requires the saved session, workspace, bundle, target and profile identity")
	}
	return nil
}

// SessionState excludes diagnostic text, runtime configuration and private
// paths. Idle alone does not prove successful work or an unchanged checkout.
type SessionState struct {
	Identity                SessionIdentity `json:"identity"`
	State                   string          `json:"state"`
	Idle                    bool            `json:"is_idle"`
	Lifecycle               string          `json:"lifecycle,omitempty"`
	ChatPhase               string          `json:"chat_phase,omitempty"`
	HasError                *bool           `json:"has_error,omitempty"`
	Checkout                *ExactCheckout  `json:"checkout,omitempty"`
	ExpectedRuntimeIdentity string          `json:"expected_runtime_identity,omitempty"`
	Runtime                 *RuntimeReceipt `json:"runtime,omitempty"`
}

func (c *Catalog) ReadSession(ctx context.Context, expected SessionIdentity) (SessionState, error) {
	if err := expected.validate(); err != nil {
		return SessionState{}, err
	}
	data, err := c.artifact(ctx, "GET", sessionPath(expected.Session), "session identity", "application/json", nil, maxBytes, 30*time.Second)
	if err != nil {
		return SessionState{}, err
	}
	// Explicit tags keep this projection independent of the daemon's extra fields.
	var record struct {
		ID                      string          `json:"id"`
		Workspace               string          `json:"workspace_id"`
		Bundle                  string          `json:"bundle_id"`
		Target                  string          `json:"target_id"`
		Profile                 string          `json:"profile_id"`
		State                   string          `json:"state"`
		Idle                    *bool           `json:"is_idle"`
		Lifecycle               string          `json:"lifecycle"`
		ChatPhase               string          `json:"chat_phase"`
		HasError                *bool           `json:"has_error"`
		Checkout                *ExactCheckout  `json:"checkout"`
		ExpectedRuntimeIdentity string          `json:"expected_runtime_identity"`
		Runtime                 json.RawMessage `json:"runtime"`
	}
	if json.Unmarshal(data, &record) != nil || record.Idle == nil || !ValidID(record.State) {
		return SessionState{}, errors.New("Mjolnir returned incomplete session identity")
	}
	actual := SessionIdentity{record.ID, record.Workspace, record.Bundle, Selection{record.Target, record.Profile}}
	if actual != expected {
		return SessionState{}, errors.New("Mjolnir session identity does not match the saved launch receipt")
	}
	if record.Checkout != nil && record.Checkout.Validate() != nil {
		return SessionState{}, errors.New("Mjolnir returned an invalid exact checkout receipt")
	}
	if (record.ExpectedRuntimeIdentity != "" && !validRuntimeID(record.ExpectedRuntimeIdentity)) ||
		(record.Lifecycle != "" && !ValidID(record.Lifecycle)) || (record.ChatPhase != "" && !ValidID(record.ChatPhase)) {
		return SessionState{}, errors.New("Mjolnir returned invalid launch receipt fields")
	}
	runtime, err := readRuntimeReceipt(record.Runtime)
	if err != nil {
		return SessionState{}, err
	}
	return SessionState{
		Identity: actual, State: record.State, Idle: *record.Idle,
		Lifecycle: record.Lifecycle, ChatPhase: record.ChatPhase, HasError: record.HasError,
		Checkout: record.Checkout, ExpectedRuntimeIdentity: record.ExpectedRuntimeIdentity, Runtime: runtime,
	}, nil
}

// DiffEvidence contains private repository content. Do not put the patch in a
// public snapshot or log. The ancestry pointer distinguishes an older worker's
// missing capability from an affirmative ancestry check.
type DiffEvidence struct {
	Session              string `json:"session_id"`
	Base                 string `json:"base"`
	Head                 string `json:"head"`
	Patch                string `json:"-"`
	HeadDescendsFromBase *bool  `json:"head_descends_from_base,omitempty"`
}

func exactCommit(v string) bool {
	return (len(v) == 40 || len(v) == 64) && strings.Trim(v, "0123456789abcdef") == ""
}

func sessionPath(id string) string { return "/sessions/" + url.PathEscape(id) }

func validSessionID(id string) bool { return ValidID(id) && id != "." && id != ".." }

func (c *Catalog) ReadDiff(ctx context.Context, session, base string) (DiffEvidence, error) {
	if !validSessionID(session) || !exactCommit(base) {
		return DiffEvidence{}, errors.New("Mjolnir diff requires a session ID and exact base commit")
	}
	query := url.Values{"base": {base}, "json": {"true"}}
	data, err := c.artifact(ctx, "GET", sessionPath(session)+"/diff?"+query.Encode(), "diff", "application/json", nil, maxDiffBytes, 30*time.Second)
	if err != nil {
		return DiffEvidence{}, err
	}
	var record struct {
		Diff     *string `json:"diff"`
		Base     string  `json:"base"`
		Head     string  `json:"head"`
		Descends *bool   `json:"head_descends_from_base"`
	}
	if json.Unmarshal(data, &record) != nil || record.Diff == nil || record.Base != base || !exactCommit(record.Head) {
		return DiffEvidence{}, errors.New("Mjolnir diff is incomplete or does not match the expected revision")
	}
	return DiffEvidence{session, record.Base, record.Head, *record.Diff, record.Descends}, nil
}

// CheckReviewTree checks checkout evidence only. It is not a review verdict:
// callers must still validate the independent review's complete, exact-revision
// receipt. An empty patch or zero comments must never manufacture a clean review.
func (d DiffEvidence) CheckReviewTree(session, head string) error {
	if !validSessionID(session) || !exactCommit(head) || d.Session != session || d.Base != head || d.Head != head || d.Patch != "" || (d.HeadDescendsFromBase != nil && !*d.HeadDescendsFromBase) {
		return errors.New("Mjolnir review checkout is changed or does not match the dispatched session and revision")
	}
	return nil
}

// CheckRepairHistory is necessary, not sufficient, for a repair. Import and
// verify the exported bundle in a private local checkout, run verification on
// its exact head and independently review it before using existing write gates.
func (d DiffEvidence) CheckRepairHistory(session, base string) error {
	if !validSessionID(session) || !exactCommit(base) || d.Session != session || d.Base != base || !exactCommit(d.Head) || d.Head == base || strings.TrimSpace(d.Patch) == "" {
		return errors.New("Mjolnir repair lacks a nonempty change at the dispatched session and revision")
	}
	if d.HeadDescendsFromBase == nil {
		return errors.New("Mjolnir worker does not report repair ancestry; upgrade the worker before accepting this evidence")
	}
	if !*d.HeadDescendsFromBase {
		return errors.New("Mjolnir repair rewrote history; retain the session for reconciliation")
	}
	return nil
}

func (c *Catalog) ReadFile(ctx context.Context, session, filename string) ([]byte, error) {
	// Town receipts belong to one repository. Deliberately do not use the API's
	// optional sibling-repository traversal, absolute paths or filename headers.
	if !validSessionID(session) || filename == "" || strings.TrimSpace(filename) != filename || len(filename) > 4096 || path.IsAbs(filename) || path.Clean(filename) != filename || filename == "." || filename == ".." || strings.HasPrefix(filename, "../") || strings.Contains(filename, "\\") || strings.ContainsFunc(filename, unicode.IsControl) {
		return nil, errors.New("Mjolnir file evidence requires a session ID and a relative path within its repository")
	}
	query := url.Values{"path": {filename}}
	return c.artifact(ctx, "GET", sessionPath(session)+"/files?"+query.Encode(), "file", "application/octet-stream", nil, maxFileBytes, 30*time.Second)
}

// ExportBundle checkpoints the session and reads its committed work. There is
// intentionally no branch-export method: GitHub writes remain Town's authority.
// A timeout leaves checkpoint completion uncertain; this method never retries.
// Returned bytes still require git bundle verification and exact-head checks.
func (c *Catalog) ExportBundle(ctx context.Context, session string) ([]byte, error) {
	if !validSessionID(session) {
		return nil, errors.New("Mjolnir bundle export requires a session ID")
	}
	data, err := c.artifact(ctx, "POST", sessionPath(session)+"/export", "bundle export", "application/octet-stream", strings.NewReader(`{"kind":"bundle"}`), maxBundleBytes, 2*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("Mjolnir bundle export was not confirmed; retain the session and reconcile its checkpoint: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("Mjolnir returned an empty bundle; committed repair evidence is unavailable")
	}
	return data, nil
}

// TranscriptItem retains the documented projection, not the unstable raw body.
// Text is private evidence and must not enter public snapshots or logs.
type TranscriptItem struct {
	ID       string `json:"stable_id"`
	Position uint64 `json:"position"`
	Sequence uint64 `json:"seq"`
	Role     string `json:"role"`
	Text     string `json:"-"`
}

type TranscriptPage struct {
	Session string
	Latest  uint64
	Next    uint64
	Items   []TranscriptItem
}

func (p TranscriptPage) Complete() bool { return p.Session != "" && p.Next == p.Latest }

func (c *Catalog) ReadTranscript(ctx context.Context, session string, after uint64) (TranscriptPage, error) {
	if !validSessionID(session) {
		return TranscriptPage{}, errors.New("Mjolnir transcript requires a session ID")
	}
	query := url.Values{"after_seq": {strconv.FormatUint(after, 10)}, "limit": {strconv.Itoa(transcriptPageSize)}}
	data, err := c.artifact(ctx, "GET", sessionPath(session)+"/transcript?"+query.Encode(), "transcript", "application/json", nil, maxBytes, 30*time.Second)
	if err != nil {
		return TranscriptPage{}, err
	}
	var record struct {
		Session string  `json:"session_id"`
		Latest  *uint64 `json:"latest_seq"`
		Next    *uint64 `json:"next_after_seq"`
		Items   []struct {
			ID       string  `json:"stable_id"`
			Position uint64  `json:"position"`
			Sequence uint64  `json:"seq"`
			Role     string  `json:"role"`
			Text     *string `json:"text"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &record) != nil || record.Session != session || record.Latest == nil || *record.Latest < after || record.Items == nil {
		return TranscriptPage{}, errors.New("Mjolnir returned incomplete or mismatched transcript evidence")
	}
	page := TranscriptPage{Session: session, Latest: *record.Latest, Next: after, Items: []TranscriptItem{}}
	seen := map[string]bool{}
	for _, item := range record.Items {
		// One event can produce several items with the same sequence. The daemon
		// keeps the entire group together, even beyond the requested page size.
		if !ValidID(item.ID) || seen[item.ID] || !ValidID(item.Role) || item.Text == nil || item.Sequence <= after || item.Sequence < page.Next || item.Sequence > page.Latest {
			return TranscriptPage{}, errors.New("Mjolnir returned invalid transcript sequence or content")
		}
		seen[item.ID] = true
		page.Items = append(page.Items, TranscriptItem{item.ID, item.Position, item.Sequence, item.Role, *item.Text})
		page.Next = item.Sequence
	}
	if record.Next != nil && *record.Next != page.Next {
		return TranscriptPage{}, errors.New("Mjolnir transcript cursor skipped evidence")
	}
	if len(page.Items) == 0 && !page.Complete() {
		return TranscriptPage{}, errors.New("Mjolnir transcript did not advance; evidence remains incomplete")
	}
	return page, nil
}

func (c *Catalog) artifact(ctx context.Context, method, route, name, mediaType string, body io.Reader, limit int64, timeout time.Duration) ([]byte, error) {
	response, err := c.request(ctx, method, route, body, timeout)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &ArtifactError{name, response.StatusCode}
	}
	actualType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || actualType != mediaType {
		return nil, errors.New("Mjolnir returned an unsupported " + name + " representation; evidence is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Mjolnir artifact read interrupted: %w", ctx.Err())
	}
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("Mjolnir " + name + " exceeded its size limit or was interrupted; evidence is incomplete")
	}
	return data, nil
}

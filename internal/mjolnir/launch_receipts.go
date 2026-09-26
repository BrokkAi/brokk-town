package mjolnir

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

// ExactCheckout is the daemon's immutable starting selection for one bundle
// repository. It records intent while provisioning, and is not the current HEAD
// after work or checkpoint recovery. Branch may be empty for detached reviews.
type ExactCheckout struct {
	Repository string `json:"repository_id"`
	Commit     string `json:"commit"`
	Branch     string `json:"branch,omitempty"`
}

func (c ExactCheckout) Validate() error {
	if !ValidID(c.Repository) || !exactCommit(c.Commit) || strings.Trim(c.Commit, "0") == "" || (c.Branch != "" && !validCheckoutBranch(c.Branch)) {
		return errors.New("Mjolnir exact checkout requires a repository ID, full nonzero commit ID and valid optional private branch")
	}
	return nil
}

func validCheckoutBranch(branch string) bool {
	if len(branch) > 256 || branch == "HEAD" || strings.HasPrefix(branch, "-") || strings.HasSuffix(branch, ".") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return !strings.ContainsAny(branch, " ~^:?*[\\") && !strings.ContainsFunc(branch, unicode.IsControl)
}

// RuntimeReceipt is one initialization reported by the executing worker. The
// opaque ID belongs to Mjolnir: never reconstruct it from versions or catalog
// revisions. EventOrdinal and ObservedAtMS distinguish retained initializations.
// Callers must save each accepted receipt rather than overwrite a prior run with
// a later session lookup. Missing ID means explicit unavailable provenance.
type RuntimeReceipt struct {
	ID                string             `json:"id,omitempty"`
	Harness           string             `json:"harness"`
	Platform          string             `json:"platform"`
	Provenance        string             `json:"provenance"`
	Components        []RuntimeComponent `json:"components"`
	UnavailableReason string             `json:"unavailable_reason,omitempty"`
	EventOrdinal      uint64             `json:"event_ordinal"`
	ObservedAtMS      int64              `json:"observed_at_ms"`
}

type RuntimeComponent struct {
	Name    string  `json:"name"`
	Version *string `json:"version"`
	SHA256  *string `json:"sha256"`
}

func validRuntimeID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if c < '!' || c > '~' {
			return false
		}
	}
	return true
}

func readRuntimeReceipt(data json.RawMessage) (*RuntimeReceipt, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil // Older workers and sessions not yet initialized.
	}
	var record struct {
		ID                *string            `json:"id"`
		Harness           string             `json:"harness"`
		Platform          string             `json:"platform"`
		Provenance        string             `json:"provenance"`
		Components        []RuntimeComponent `json:"components"`
		UnavailableReason *string            `json:"unavailable_reason"`
		EventOrdinal      *uint64            `json:"event_ordinal"`
		ObservedAtMS      *int64             `json:"observed_at_ms"`
	}
	invalid := errors.New("Mjolnir returned incomplete or invalid runtime evidence; check its worker and API version")
	if json.Unmarshal(data, &record) != nil || !ValidID(record.Harness) || !ValidID(record.Platform) || record.EventOrdinal == nil || record.ObservedAtMS == nil || *record.ObservedAtMS <= 0 || record.Components == nil || len(record.Components) > 64 {
		return nil, invalid
	}
	if record.Provenance != "managed_installation" && record.Provenance != "target_installation" {
		return nil, invalid
	}
	r := &RuntimeReceipt{
		Harness: record.Harness, Platform: record.Platform, Provenance: record.Provenance,
		Components: record.Components, EventOrdinal: *record.EventOrdinal, ObservedAtMS: *record.ObservedAtMS,
	}
	if record.ID != nil {
		if !validRuntimeID(*record.ID) || record.UnavailableReason != nil || len(record.Components) == 0 {
			return nil, invalid
		}
		r.ID = *record.ID
	} else {
		if record.UnavailableReason == nil || strings.TrimSpace(*record.UnavailableReason) == "" {
			return nil, invalid
		}
		// Never retain raw worker diagnostics, which can include local paths or
		// private environment values. Keep the unknown state explicit instead.
		r.UnavailableReason = "Runtime provenance is unavailable; inspect the target runtime in Mjolnir."
	}
	seen := map[string]bool{}
	for _, component := range r.Components {
		if !ValidID(component.Name) || seen[component.Name] || (component.Version != nil && (!ValidID(*component.Version))) || (component.SHA256 != nil && (len(*component.SHA256) != 64 || strings.Trim(*component.SHA256, "0123456789abcdef") != "")) {
			return nil, invalid
		}
		seen[component.Name] = true
	}
	return r, nil
}

// CheckLaunchReceipt checks a newly created, exclusively owned session's launch
// declarations. It is necessary, not sufficient, before a prompt: callers must
// also verify the bundle/repository mapping, compare current diff evidence with
// the exact commit, persist the receipt, and configure model/effort. A resumed
// session can retain its original checkout declaration after changing HEAD.
//
// Checking runtime.ID alone would race a worker replacement. Require the saved
// expected_runtime_identity too: Mjolnir enforces it at prompt admission and on
// resume. This read does not itself authorize a prompt or any GitHub write.
func (s SessionState) CheckLaunchReceipt(checkout ExactCheckout, runtimeID string) error {
	if s.Identity.validate() != nil || checkout.Validate() != nil || !validRuntimeID(runtimeID) {
		return errors.New("Mjolnir launch checks require a saved session, exact checkout and selected runtime identity")
	}
	if s.Checkout == nil || *s.Checkout != checkout {
		return errors.New("Mjolnir checkout selection does not match the saved launch intent; retain the session for reconciliation")
	}
	if s.Lifecycle != "live" || !s.Idle || s.ChatPhase != "idle" || s.HasError == nil || *s.HasError {
		return errors.New("Mjolnir session is not confirmed ready; inspect its preparation and worker state before prompting")
	}
	if s.Runtime == nil {
		return errors.New("Mjolnir runtime receipt is missing; wait for initialization or upgrade the worker before prompting")
	}
	if s.Runtime.ID == "" || s.Runtime.UnavailableReason != "" {
		return errors.New("Mjolnir runtime identity is unavailable; select a target runtime with known provenance")
	}
	if s.Runtime.ID != runtimeID {
		return errors.New("Mjolnir runtime changed; discover its current identity and explicitly update the selection")
	}
	if s.ExpectedRuntimeIdentity != runtimeID {
		return errors.New("Mjolnir session does not enforce the selected runtime identity; a guarded launch is required before prompting")
	}
	return nil
}

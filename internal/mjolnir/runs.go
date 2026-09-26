package mjolnir

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RunPlan struct {
	ID         string        `json:"id"`
	Repository string        `json:"repository"`
	Selection  Selection     `json:"selection"`
	Placement  Placement     `json:"placement"`
	Checkout   ExactCheckout `json:"checkout"`
	Runtime    RuntimePin    `json:"runtime"`
}

func (p RunPlan) Validate() error {
	if p.ID == "" || len(p.ID) > 128 || strings.Trim(p.ID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" || !ValidID(p.Repository) || p.Selection.Validate() != nil || !p.Selection.Managed() || p.Placement.Validate() != nil || p.Checkout.Validate() != nil || p.Runtime.Validate() != nil || p.Runtime.Source.Selection != p.Selection || p.Checkout.Repository != p.Placement.Repository || p.Checkout.Branch != "town/"+p.ID {
		return errors.New("invalid Mjolnir run plan; exact revision, private branch, placement and selected runtime are required")
	}
	return nil
}

type RunRecord struct {
	Version    int           `json:"version"`
	Connection string        `json:"connection"`
	Plan       RunPlan       `json:"plan"`
	State      string        `json:"state"`
	Session    string        `json:"session_id,omitempty"`
	Receipt    *SessionState `json:"receipt,omitempty"`
	Evidence   string        `json:"evidence_sha256,omitempty"`
	Updated    time.Time     `json:"updated"`
}

func (r RunRecord) validate() error {
	if r.Version != 1 || r.Plan.Validate() != nil || r.Updated.IsZero() {
		return errors.New("invalid Mjolnir run record")
	}
	switch r.State {
	case "creating", "uncertain":
		if r.Session != "" && !validSessionID(r.Session) {
			return errors.New("invalid uncertain session identity")
		}
	case "preparing", "held":
		if !validSessionID(r.Session) {
			return errors.New("missing retained session identity")
		}
	case "ready", "prompting", "evidence", "destroying", "uncertain_cleanup", "destroyed":
		expected := SessionIdentity{r.Session, r.Plan.Placement.Workspace.ID, r.Plan.Placement.Bundle, r.Plan.Selection}
		if !validSessionID(r.Session) || r.Receipt == nil || r.Receipt.Identity != expected || r.Receipt.CheckLaunchReceipt(r.Plan.Checkout, r.Plan.Runtime.Runtime.ID) != nil {
			return errors.New("missing confirmed launch receipt")
		}
	default:
		return errors.New("unknown Mjolnir run outcome")
	}
	if r.State == "evidence" || r.State == "destroying" || r.State == "uncertain_cleanup" || r.State == "destroyed" {
		if len(r.Evidence) != 64 || strings.Trim(r.Evidence, "0123456789abcdef") != "" {
			return errors.New("missing saved Mjolnir evidence digest")
		}
	}
	return nil
}

// Runs is private durable state owned by Town, independent of the daemon's
// session storage. The daemon does not support idempotent session creation:
// existence of a run intent always prevents another submission with that ID.
type Runs struct {
	Catalog   *Catalog
	Directory string
}

type Run struct {
	mu        sync.Mutex
	owner     *Runs
	record    RunRecord
	directory string
}

func (r *Run) Record() RunRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, _ := json.Marshal(r.record)
	var copy RunRecord
	_ = json.Unmarshal(data, &copy)
	return copy
}

func (r *Run) save(state string) error {
	next := r.record
	next.State, next.Updated = state, time.Now().UTC()
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := writeCache(filepath.Join(r.directory, "run.json"), data); err != nil {
		return err
	}
	if err := syncDirectory(r.directory); err != nil {
		return err
	}
	r.record = next
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// SessionCreator permits the ACP-owned path to return session/new's ID through
// this same durable boundary. It must create once, without sending any prompt.
type SessionCreator func(context.Context, RunPlan) (string, error)

func (rs *Runs) Prepare(ctx context.Context, plan RunPlan, create SessionCreator) (*Run, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	// Configuration editors and callers must not retain mutable component
	// slices/pointers into the dispatched runtime receipt.
	encoded, _ := json.Marshal(plan)
	var frozen RunPlan
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return nil, err
	}
	plan = frozen
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if rs.Catalog == nil || rs.Catalog.listing.Demo || rs.Catalog.identity == "" {
		return nil, errors.New("Mjolnir execution requires a configured connection outside demo mode")
	}
	placement, err := rs.Catalog.ResolvePlacement(ctx, plan.Repository, plan.Placement.Workspace.Name, plan.Placement.Bundle)
	if err != nil {
		return nil, err
	}
	if placement != plan.Placement {
		return nil, errors.New("Mjolnir repository mapping changed before launch")
	}
	if err := os.MkdirAll(rs.Directory, 0700); err != nil {
		return nil, err
	}
	directory := filepath.Join(rs.Directory, plan.ID)
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, errors.New("Mjolnir run intent already exists or cannot be saved; inspect recovery before submitting any replacement")
	}
	r := &Run{owner: rs, directory: directory, record: RunRecord{Version: 1, Connection: rs.Catalog.identity, Plan: plan}}
	if err := r.save("creating"); err != nil {
		return r, err
	}
	if err := syncDirectory(rs.Directory); err != nil {
		return r, err
	}
	if create == nil {
		create = rs.Catalog.createSession
	}
	id, err := create(ctx, plan)
	if validSessionID(id) {
		r.record.Session = id
	}
	if err != nil || !validSessionID(id) {
		return r, errors.Join(errors.New("Mjolnir session creation was not confirmed; retain this run and never automatically resubmit"), ctx.Err(), r.save("uncertain"))
	}
	// Bind the returned identity durably before reading readiness or permitting
	// a prompt. No title matching or session-list adoption can replace this ID.
	if err := r.save("preparing"); err != nil {
		return r, err
	}
	ready, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		receipt, err := rs.Catalog.ReadSession(ready, r.identity())
		if err != nil {
			return r, errors.Join(err, r.save("uncertain"))
		}
		if receipt.Lifecycle == "live" && receipt.Idle {
			if err := receipt.CheckLaunchReceipt(plan.Checkout, plan.Runtime.Runtime.ID); err != nil {
				return r, errors.Join(err, r.save("held"))
			}
			diff, err := rs.Catalog.ReadDiff(ready, id, plan.Checkout.Commit)
			if err == nil {
				err = diff.CheckReviewTree(id, plan.Checkout.Commit)
			}
			if err != nil {
				return r, errors.Join(err, r.save("held"))
			}
			r.record.Receipt = &receipt
			return r, r.save("ready")
		}
		if receipt.Lifecycle != "live" && receipt.Lifecycle != "starting" {
			return r, errors.Join(errors.New("Mjolnir checkout preparation failed or stopped; inspect the retained session"), r.save("held"))
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ready.Done():
			timer.Stop()
			return r, errors.Join(ready.Err(), r.save("uncertain"))
		case <-timer.C:
		}
	}
}

func (c *Catalog) createSession(ctx context.Context, p RunPlan) (string, error) {
	data, _ := json.Marshal(struct {
		Workspace string `json:"workspace_id"`
		Selection
		Bundle   string        `json:"bundle_id"`
		Checkout ExactCheckout `json:"checkout"`
		Runtime  string        `json:"expected_runtime_identity"`
		Managed  bool          `json:"create_managed_worktree"`
	}{p.Placement.Workspace.ID, p.Selection, p.Placement.Bundle, p.Checkout, p.Runtime.Runtime.ID, true})
	var result struct {
		Session string  `json:"session_id"`
		Turn    *uint64 `json:"turn_id"`
	}
	err := c.mutation(ctx, "/sessions", data, &result, 201)
	if err == nil && result.Turn != nil {
		err = errors.New("Mjolnir unexpectedly accepted a turn during checkout preparation")
	}
	return result.Session, err
}

func (r *Run) identity() SessionIdentity {
	return SessionIdentity{r.record.Session, r.record.Plan.Placement.Workspace.ID, r.record.Plan.Placement.Bundle, r.record.Plan.Selection}
}

// MarkPrompting is durable before any prompt is submitted. Even a lost prompt
// response keeps the run held; only complete artifact evidence can release it.
func (r *Run) MarkPrompting() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record.State != "ready" {
		return errors.New("Mjolnir run is not ready for its single prompt")
	}
	return r.save("prompting")
}

// SaveEvidence durably stores the caller's complete validated evidence before
// permitting cleanup. Private content never enters the public run projection.
func (r *Run) SaveEvidence(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record.State != "prompting" || len(data) == 0 || len(data) > 32<<20 {
		return errors.New("Mjolnir run requires complete saved evidence before cleanup")
	}
	if err := writeCache(filepath.Join(r.directory, "evidence.json"), data); err != nil {
		return err
	}
	if err := syncDirectory(r.directory); err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	r.record.Evidence = hex.EncodeToString(digest[:])
	return r.save("evidence")
}

// Destroy is used only after evidence has been saved. It never retries a lost
// acknowledgement and never touches bundle/workspace records or source branches.
func (r *Run) Destroy(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record.State != "evidence" || r.record.Evidence == "" {
		return errors.New("Mjolnir cleanup requires confirmed saved evidence")
	}
	if err := verifySavedEvidence(r.directory, r.record.Evidence); err != nil {
		return err
	}
	receipt, err := r.owner.Catalog.ReadSession(ctx, r.identity())
	if err != nil {
		return err
	}
	if receipt.Lifecycle != "live" || !receipt.Idle || receipt.ChatPhase != "idle" || receipt.HasError == nil || *receipt.HasError || receipt.Runtime == nil || r.record.Receipt == nil || receipt.Runtime.EventOrdinal != r.record.Receipt.Runtime.EventOrdinal {
		return errors.New("Mjolnir session changed or is busy; retain it instead of cleaning up")
	}
	if err := r.save("destroying"); err != nil {
		return err
	}
	if err := r.owner.Catalog.mutation(ctx, sessionPath(r.record.Session)+"/destroy", []byte(`{}`), nil, 202); err != nil {
		return errors.Join(err, r.save("uncertain_cleanup"))
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		_, err := r.owner.Catalog.ReadSession(deadline, r.identity())
		var artifact *ArtifactError
		if errors.As(err, &artifact) && artifact.Status == 404 {
			return r.save("destroyed")
		}
		if err != nil {
			return errors.Join(err, r.save("uncertain_cleanup"))
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-deadline.Done():
			timer.Stop()
			return errors.Join(deadline.Err(), r.save("uncertain_cleanup"))
		case <-timer.C:
		}
	}
}

// Records is read-only recovery: unfinished records stay unfinished. A missing
// creation ID is uncertainty, not evidence that the daemon did nothing.
func (rs *Runs) Records() ([]RunRecord, error) {
	if rs.Catalog == nil || rs.Catalog.listing.Demo || rs.Catalog.identity == "" {
		return nil, errors.New("Mjolnir recovery requires its configured daemon connection outside demo mode")
	}
	entries, err := os.ReadDir(rs.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return []RunRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records []RunRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := readBounded(filepath.Join(rs.Directory, entry.Name(), "run.json"), maxBytes)
		if err != nil {
			return nil, errors.New("Mjolnir run has an unreadable intent; retain its directory for recovery")
		}
		var record RunRecord
		if json.Unmarshal(data, &record) != nil || record.validate() != nil || record.Plan.ID != entry.Name() || record.Connection != rs.Catalog.identity {
			return nil, errors.New("Mjolnir run has invalid evidence or belongs to another daemon connection; retain it for recovery")
		}
		if record.Evidence != "" {
			if err := verifySavedEvidence(filepath.Join(rs.Directory, entry.Name()), record.Evidence); err != nil {
				return nil, err
			}
		}
		records = append(records, record)
	}
	return records, nil
}

func verifySavedEvidence(directory, digest string) error {
	data, err := readBounded(filepath.Join(directory, "evidence.json"), 32<<20)
	if err != nil {
		return errors.New("saved Mjolnir evidence is unavailable; retain the session")
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != digest {
		return errors.New("saved Mjolnir evidence changed; retain the session")
	}
	return nil
}

// ReconcileCleanup confirms only a previously submitted cleanup. It cannot
// submit another destroy or adopt a session by title after a lost create reply.
func (rs *Runs) ReconcileCleanup(ctx context.Context, id string) error {
	records, err := rs.Records()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Plan.ID != id {
			continue
		}
		if record.State == "destroyed" {
			return nil
		}
		if record.State != "destroying" && record.State != "uncertain_cleanup" {
			return errors.New("Mjolnir run has no submitted cleanup to reconcile")
		}
		r := &Run{owner: rs, record: record, directory: filepath.Join(rs.Directory, id)}
		_, err := rs.Catalog.ReadSession(ctx, r.identity())
		var artifact *ArtifactError
		if errors.As(err, &artifact) && artifact.Status == 404 {
			return r.save("destroyed")
		}
		if err != nil {
			return err
		}
		return errors.New("Mjolnir cleanup is not confirmed; retained run remains uncertain")
	}
	return errors.New("unknown Mjolnir run")
}

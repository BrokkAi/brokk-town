package mjolnir

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

// Artifacts is private evidence, never a public snapshot or a review verdict.
// The bot must separately parse and validate its complete semantic receipt.
type Artifacts struct {
	Head   string
	Bundle []byte
}

type privateTranscriptItem struct {
	TranscriptItem
	Text string `json:"text"`
}

type privateEvidence struct {
	Session      SessionState            `json:"session"`
	Diff         DiffEvidence            `json:"diff"`
	Patch        string                  `json:"patch"`
	Transcript   []privateTranscriptItem `json:"transcript"`
	Sequence     uint64                  `json:"sequence"`
	Answer       string                  `json:"answer"`
	BundleSHA256 string                  `json:"bundle_sha256,omitempty"`
}

func (r *Run) checkCurrentSession(ctx context.Context) (SessionState, error) {
	s, err := r.owner.Catalog.ReadSession(ctx, r.identity())
	if err != nil {
		return s, err
	}
	if err := s.CheckLaunchReceipt(r.record.Plan.Checkout, r.record.Plan.Runtime.Runtime.ID); err != nil {
		return s, err
	}
	initial := r.record.Receipt
	if initial == nil || initial.Runtime == nil || s.Runtime.EventOrdinal != initial.Runtime.EventOrdinal || s.Runtime.ObservedAtMS != initial.Runtime.ObservedAtMS {
		return s, errors.New("Mjolnir worker reinitialized during the run; retain its evidence and inspect the session")
	}
	return s, nil
}

// Collect binds a finished ACP answer to complete retained transcript and exact
// checkout evidence. Missing artifacts are refusals. Repair export is submitted
// once after a durable intent because exporting can checkpoint the workspace.
func (r *Run) Collect(ctx context.Context, repair bool, answer string) (Artifacts, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result Artifacts
	if r.record.State != "prompting" || strings.TrimSpace(answer) == "" || len(answer) > 1<<20 {
		return result, errors.New("Mjolnir evidence requires a single completed prompt with a bounded answer")
	}
	c := r.owner.Catalog
	s, err := r.checkCurrentSession(ctx)
	if err != nil {
		return result, err
	}
	session, base := r.record.Session, r.record.Plan.Checkout.Commit
	diff, err := c.ReadDiff(ctx, session, base)
	if err != nil {
		return result, err
	}
	if repair {
		err = diff.CheckRepairHistory(session, base)
	} else {
		err = diff.CheckReviewTree(session, base)
	}
	if err != nil {
		return result, err
	}
	evidence := privateEvidence{Session: s, Diff: diff, Patch: diff.Patch, Answer: answer}
	var latest uint64
	seen := map[string]bool{}
	lastAgent, lastPosition := "", uint64(0)
	total := 0
	for pages := 0; ; pages++ {
		if pages >= 100 {
			return result, errors.New("Mjolnir transcript exceeds the evidence page limit; retain the session")
		}
		page, err := c.ReadTranscript(ctx, session, evidence.Sequence)
		if err != nil {
			return result, err
		}
		if pages == 0 {
			latest = page.Latest
		} else if page.Latest != latest {
			return result, errors.New("Mjolnir transcript changed while collecting evidence; retain the session")
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				return result, errors.New("Mjolnir transcript repeated an item across pages")
			}
			seen[item.ID] = true
			total += len(item.Text) + len(item.ID) + 128
			if total > 8<<20 {
				return result, errors.New("Mjolnir transcript exceeds the evidence size limit; retain the session")
			}
			evidence.Transcript = append(evidence.Transcript, privateTranscriptItem{item, item.Text})
			if item.Role == "agent" && (lastAgent == "" || item.Position > lastPosition) {
				lastAgent, lastPosition = item.Text, item.Position
			}
		}
		evidence.Sequence = page.Next
		if page.Complete() {
			break
		}
	}
	if strings.TrimSpace(lastAgent) != strings.TrimSpace(answer) {
		return result, errors.New("Mjolnir transcript does not contain the completed ACP answer; retain the session")
	}
	if repair {
		clean, err := c.ReadDiff(ctx, session, diff.Head)
		if err != nil {
			return result, err
		}
		if err := clean.CheckReviewTree(session, diff.Head); err != nil {
			return result, errors.New("Mjolnir repair contains uncommitted work; commit it before accepting repair evidence")
		}
		if err := r.save("exporting"); err != nil {
			return result, err
		}
		bundle, err := c.ExportBundle(ctx, session)
		if err != nil {
			return result, err
		}
		if err := writeCache(filepath.Join(r.directory, "repair.bundle"), bundle); err != nil {
			return result, err
		}
		if err := syncDirectory(r.directory); err != nil {
			return result, err
		}
		hash := sha256.Sum256(bundle)
		evidence.BundleSHA256 = hex.EncodeToString(hash[:])
		result.Bundle = bundle
	}
	// Recheck after transcript/export: checkpointing must not silently turn dirty
	// work into a different commit, and a concurrent turn must not be certified.
	after, err := c.ReadDiff(ctx, session, base)
	if err != nil {
		return Artifacts{}, err
	}
	if after.Head != diff.Head || after.Patch != diff.Patch {
		return Artifacts{}, errors.New("Mjolnir checkout changed while collecting evidence; retain the session")
	}
	if repair {
		if err := after.CheckRepairHistory(session, base); err != nil {
			return Artifacts{}, err
		}
	}
	clean, err := c.ReadDiff(ctx, session, diff.Head)
	if err != nil {
		return Artifacts{}, err
	}
	if err := clean.CheckReviewTree(session, diff.Head); err != nil {
		return Artifacts{}, err
	}
	end, err := c.ReadTranscript(ctx, session, evidence.Sequence)
	if err != nil {
		return Artifacts{}, err
	}
	if !end.Complete() || len(end.Items) != 0 || end.Latest != latest {
		return Artifacts{}, errors.New("Mjolnir transcript advanced after the completed turn; retain the session")
	}
	if _, err := r.checkCurrentSession(ctx); err != nil {
		return Artifacts{}, err
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return Artifacts{}, err
	}
	if err := r.saveEvidence(data); err != nil {
		return Artifacts{}, err
	}
	result.Head = diff.Head
	return result, nil
}

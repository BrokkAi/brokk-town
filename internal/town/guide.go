package town

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const guideTurns = 20
const guideQuestionBytes = 4096
const guideAnswerBytes = 16 << 10

type GuideConversation struct {
	Revision uint64      `json:"revision"`
	Next     uint64      `json:"next"`
	Turns    []GuideTurn `json:"turns"`
}
type GuideTurn struct {
	ID       string               `json:"id"`
	Sequence uint64               `json:"sequence"`
	Question string               `json:"question"`
	Answer   string               `json:"answer"`
	Status   string               `json:"status"`
	Detail   string               `json:"detail,omitempty"`
	Started  time.Time            `json:"started"`
	Updated  time.Time            `json:"updated"`
	Profile  PublicBotAgentConfig `json:"profile"`
	Houses   []Role               `json:"houses"`
	Proposal *GuideProposal       `json:"proposal,omitempty"`
}
type GuideProposal struct {
	Action      string `json:"action"`
	Role        Role   `json:"role"`
	Description string `json:"description"`
	Digest      string `json:"digest"`
	Status      string `json:"status"`
}
type GuideQuestion struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	Question string `json:"question"`
}

func guideBusy(status string) bool {
	return status == "queued" || status == "gathering" || status == "answering"
}
func guideID(id string) bool {
	return len(id) >= 8 && len(id) <= 64 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") == ""
}
func (g *GuideConversation) turn(id string) *GuideTurn {
	if g != nil {
		for i := range g.Turns {
			if g.Turns[i].ID == id {
				return &g.Turns[i]
			}
		}
	}
	return nil
}
func (g *GuideConversation) validate() error {
	if g == nil {
		return nil
	}
	if len(g.Turns) > guideTurns {
		return errors.New("too many guide turns")
	}
	var previous uint64
	busy := 0
	ids := map[string]bool{}
	for i, t := range g.Turns {
		if !guideID(t.ID) || ids[t.ID] || len(t.Question) > guideQuestionBytes || strings.TrimSpace(t.Question) == "" || len(t.Answer) > guideAnswerBytes || !utf8.ValidString(t.Question) || !utf8.ValidString(t.Answer) || t.Sequence >= g.Next || (i > 0 && t.Sequence <= previous) {
			return errors.New("invalid guide conversation")
		}
		ids[t.ID] = true
		previous = t.Sequence
		switch t.Status {
		case "queued", "gathering", "answering":
			busy++
		case "complete", "failed", "cancelled", "interrupted":
		default:
			return errors.New("invalid guide outcome")
		}
		if p := t.Proposal; p != nil {
			if t.Status != "complete" || p.Action != "pause" || !ValidRole(p.Role) || len(p.Digest) != 64 || (p.Status != "proposed" && p.Status != "confirmed") {
				return errors.New("invalid guide proposal")
			}
		}
	}
	if busy > 1 {
		return errors.New("multiple guide questions in flight")
	}
	return nil
}
func (s *Supervisor) AskGuide(id string, q GuideQuestion) (GuideTurn, error) {
	id = strings.ToLower(id)
	q.Question = strings.TrimSpace(q.Question)
	if !guideID(q.ID) || q.Question == "" || len(q.Question) > guideQuestionBytes || !utf8.ValidString(q.Question) {
		return GuideTurn{}, errors.New("guide question requires an 8–64 character request ID and 1–4096 bytes of text")
	}
	var answer GuideTurn
	err := s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		q.Question = guideRedactor(t.Config)(q.Question)
		if t.Guide == nil {
			t.Guide = &GuideConversation{Turns: []GuideTurn{}}
		}
		g := t.Guide
		if previous := g.turn(q.ID); previous != nil {
			if previous.Question != q.Question || previous.Sequence != q.Sequence {
				return errors.New("request ID already belongs to another question")
			}
			answer = clone(*previous)
			return nil
		}
		if q.Sequence != g.Next {
			return errors.New("conversation changed; refresh before submitting a new question")
		}
		if g.Next == ^uint64(0) {
			return errors.New("conversation sequence exhausted")
		}
		for _, turn := range g.Turns {
			if guideBusy(turn.Status) {
				return errors.New("wait for or cancel the current guide answer")
			}
		}
		now := s.now()
		answer = GuideTurn{ID: q.ID, Sequence: g.Next, Question: q.Question, Status: "queued", Started: now, Updated: now, Houses: []Role{}}
		g.Next++
		g.Revision++
		g.Turns = append(g.Turns, answer)
		if len(g.Turns) > guideTurns {
			g.Turns = append([]GuideTurn(nil), g.Turns[len(g.Turns)-guideTurns:]...)
		}
		return nil
	})
	return answer, err
}
func (s *Supervisor) CancelGuide(id, turnID string) error {
	return s.Store.Update(func(st *State) error {
		t := st.Towns[strings.ToLower(id)]
		if t == nil || t.Deleted {
			return errors.New("unknown town")
		}
		turn := t.Guide.turn(turnID)
		if turn == nil {
			return errors.New("unknown guide question")
		}
		if guideBusy(turn.Status) {
			t.Guide.Revision++
			turn.Status = "cancelled"
			turn.Detail = "Answer cancelled. No proposed control was dispatched."
			turn.Updated = s.now()
		}
		return nil
	})
}
func (s *Supervisor) ConfirmGuide(id, turnID, digest string) error {
	id = strings.ToLower(id)
	snapshot := s.Store.Snapshot()
	t := snapshot.Towns[id]
	if t == nil || t.Deleted {
		return errors.New("unknown town")
	}
	turn := t.Guide.turn(turnID)
	if turn == nil || turn.Proposal == nil {
		return errors.New("unknown guide proposal")
	}
	p := turn.Proposal
	if p.Digest != digest {
		return errors.New("proposal changed; inspect the exact action before confirming")
	}
	if p.Status == "confirmed" {
		return nil
	}
	return s.control(id, p.Role, p.Action, "", func(current *Town) error {
		turn := current.Guide.turn(turnID)
		if turn == nil || turn.Status != "complete" || turn.Proposal == nil {
			return errors.New("guide proposal is no longer available")
		}
		proposal := turn.Proposal
		if proposal.Status == "confirmed" {
			return errGuideAlreadyConfirmed
		}
		if proposal.Digest != digest || guideControlDigest(current, proposal.Role) != digest {
			return errors.New("worker state changed; ask for a fresh proposal")
		}
		current.Guide.Revision++
		proposal.Status = "confirmed"
		turn.Updated = s.now()
		return nil
	})
}

var errGuideAlreadyConfirmed = errors.New("guide proposal was already confirmed")

func guideControlDigest(t *Town, role Role) string {
	// Scheduling, settings and recovery changes invalidate a stale proposal. Logs
	// and timestamps do not change the meaning of pausing this specific worker.
	w := t.Workers[role]
	data, _ := json.Marshal(struct {
		Town     string
		Role     Role
		Enabled  bool
		Status   string
		Run      *WorkerRun
		Recovery *WorkerRecovery
	}{t.ID, role, w.Enabled, w.Status, w.Run, w.Recovery})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func guideProposal(text string, t *Town) (string, *GuideProposal, error) {
	const marker = "TOWN_GUIDE_PROPOSAL "
	line := strings.LastIndex(text, "\n"+marker)
	if strings.HasPrefix(text, marker) {
		line = -1
	} else if line < 0 {
		return text, nil, nil
	}
	start := line + 1
	raw := strings.TrimSpace(strings.TrimPrefix(text[start:], marker))
	var proposal struct {
		Action string `json:"action"`
		Role   Role   `json:"role"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&proposal) != nil || dec.Decode(new(any)) != io.EOF || proposal.Action != "pause" || !ValidRole(proposal.Role) {
		return strings.TrimSpace(text[:start]), nil, errors.New("guide proposed an unsupported control; use the inspector")
	}
	p := &GuideProposal{Action: "pause", Role: proposal.Role, Description: fmt.Sprintf("Pause the %s worker in %s after its current work finishes.", proposal.Role, t.ID), Digest: guideControlDigest(t, proposal.Role), Status: "proposed"}
	return strings.TrimSpace(text[:start]), p, nil
}

// Hide the optional machine-readable proposal footer while text is streaming.
// Only the validated, inert proposal becomes a client control on completion.
func guideVisibleText(text string) string {
	const marker = "TOWN_GUIDE_PROPOSAL "
	if strings.HasPrefix(text, marker) {
		return ""
	}
	if n := strings.Index(text, "\n"+marker); n >= 0 {
		return strings.TrimRight(text[:n], "\n")
	}
	start := strings.LastIndex(text, "\n") + 1
	if line := text[start:]; line != "" && strings.HasPrefix(marker, line) {
		return strings.TrimRight(text[:start], "\n")
	}
	return text
}

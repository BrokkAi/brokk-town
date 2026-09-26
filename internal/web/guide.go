package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func (s *Server) guide(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Town     string `json:"town"`
		Action   string `json:"action"`
		ID       string `json:"id,omitempty"`
		Sequence uint64 `json:"sequence,omitempty"`
		Question string `json:"question,omitempty"`
		Digest   string `json:"digest,omitempty"`
	}
	if err := decode(w, r, &input); err != nil {
		rejectInput(w, err)
		return
	}
	input.Town = strings.ToLower(input.Town)
	var err error
	switch input.Action {
	case "read":
		if input.ID != "" || input.Question != "" || input.Digest != "" || input.Sequence != 0 {
			err = errors.New("read accepts only the town")
		}
	case "ask":
		if input.Digest != "" {
			err = errors.New("ask cannot confirm proposals")
		} else {
			_, err = s.Supervisor.AskGuide(input.Town, town.GuideQuestion{ID: input.ID, Sequence: input.Sequence, Question: input.Question})
		}
	case "cancel", "confirm":
		if input.Question != "" || input.Sequence != 0 {
			err = errors.New("control cannot submit questions")
		} else if input.Action == "cancel" {
			if input.Digest != "" {
				err = errors.New("cancel cannot confirm proposals")
			} else {
				err = s.Supervisor.CancelGuide(input.Town, input.ID)
			}
		} else {
			err = s.Supervisor.ConfirmGuide(input.Town, input.ID, input.Digest)
		}
	default:
		err = errors.New("unknown guide action")
	}
	if err != nil {
		problem(w, err.Error(), http.StatusBadRequest)
		return
	}
	t := s.Store.Snapshot().Towns[input.Town]
	if t == nil || t.Deleted {
		problem(w, "unknown town", http.StatusBadRequest)
		return
	}
	conversation := t.Guide
	if conversation == nil {
		conversation = &town.GuideConversation{Turns: []town.GuideTurn{}}
	}
	respond(w, conversation)
}

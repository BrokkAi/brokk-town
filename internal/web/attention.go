package web

import (
	"github.com/BrokkAi/brokk-town/internal/town"
	"net/http"
)

func (s *Server) attentionHook(w http.ResponseWriter, r *http.Request) {
	var edit town.AttentionHookEdit
	if err := decode(w, r, &edit); err != nil {
		rejectInput(w, err)
		return
	}
	if err := s.Supervisor.SetAttentionHook(edit); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	respond(w, s.Store.Snapshot().ServiceConfig.AttentionHook.Public())
}

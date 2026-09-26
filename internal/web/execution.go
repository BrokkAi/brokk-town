package web

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
	"github.com/BrokkAi/brokk-town/internal/town"
)

func (s *Server) executionOptions(w http.ResponseWriter, r *http.Request) {
	respond(w, s.Supervisor.Mjolnir.List())
}
func (s *Server) refreshExecutionOptions(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		rejectInput(w, err)
		return
	}
	s.Supervisor.Mjolnir.RequestRefresh()
	respond(w, s.Supervisor.Mjolnir.List())
}
func (s *Server) execution(w http.ResponseWriter, r *http.Request) {
	// A missing selection is an error, while JSON null explicitly inherits.
	var input struct {
		Town      string          `json:"town"`
		Role      town.Role       `json:"role,omitempty"`
		Selection json.RawMessage `json:"selection"`
	}
	if err := decode(w, r, &input); err != nil {
		rejectInput(w, err)
		return
	}
	if len(input.Selection) == 0 {
		problem(w, "selection is required (null to inherit)", 400)
		return
	}
	var selection *mjolnir.Selection
	decoder := json.NewDecoder(bytes.NewReader(input.Selection))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&selection); err != nil {
		problem(w, "invalid execution selection", 400)
		return
	}
	if err := s.Supervisor.SetExecution(input.Town, input.Role, selection); err != nil {
		problem(w, err.Error(), 400)
		return
	}
	respond(w, map[string]bool{"ok": true})
}

package web

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Town  string `json:"town"`
		After string `json:"after,omitempty"`
		Limit int    `json:"limit,omitempty"`
		Task  string `json:"task,omitempty"`
	}
	if err := decode(w, r, &input); err != nil {
		rejectInput(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if input.Task != "" {
		if input.After != "" || input.Limit != 0 {
			problem(w, "task detail cannot include pagination", http.StatusBadRequest)
			return
		}
		result, err := s.Store.HistoryTask(ctx, input.Town, input.Task)
		if err != nil {
			problem(w, err.Error(), http.StatusBadRequest)
			return
		}
		respond(w, result)
		return
	}
	result, err := s.Store.History(ctx, input.Town, input.After, input.Limit)
	if err != nil {
		problem(w, err.Error(), http.StatusBadRequest)
		return
	}
	respond(w, result)
}

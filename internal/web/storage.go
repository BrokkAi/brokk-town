package web

import "net/http"

type storageRequest struct {
	Town            string   `json:"town"`
	MinimumAgeHours *int     `json:"minimum_age_hours,omitempty"`
	IDs             []string `json:"ids,omitempty"`
}

func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	var input storageRequest
	if err := decode(w, r, &input); err != nil {
		rejectInput(w, err)
		return
	}
	age := 168
	if input.MinimumAgeHours != nil {
		age = *input.MinimumAgeHours
	}
	if r.URL.Path == "/api/storage/cleanup" {
		result, err := s.Supervisor.CleanupStorage(r.Context(), input.Town, age, input.IDs)
		if err != nil {
			problem(w, err.Error(), http.StatusBadRequest)
			return
		}
		respond(w, result)
		return
	}
	if len(input.IDs) > 0 {
		problem(w, "inventory does not accept a cleanup selection", http.StatusBadRequest)
		return
	}
	result, err := s.Supervisor.Storage(r.Context(), input.Town, age)
	if err != nil {
		problem(w, err.Error(), http.StatusBadRequest)
		return
	}
	respond(w, result)
}

package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

// projectJSON is a project as the board's project picker shows it.
type projectJSON struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	KeyPrefix string    `json:"key_prefix"`
}

// listProjects lists every project by name (M5).
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.Store.ListProjects(r.Context())
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := make([]projectJSON, len(projects))
	for i, p := range projects {
		out[i] = projectJSON{ID: p.ID, Name: p.Name, KeyPrefix: p.KeyPrefix}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

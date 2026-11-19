package api

import (
	"encoding/json"
	"net/http"
)

// versionJSON is what GET /api/version answers: the commit the control plane
// was built from, "unknown" when its build stamped none (D131). The
// flux-preview driver reads it to know when a preview runs its head (D117's
// versionField, "version").
type versionJSON struct {
	Version string `json:"version"`
}

// getVersion reports the control plane's build commit; a read, open like the
// others (D58).
func (s *Server) getVersion(w http.ResponseWriter, _ *http.Request) {
	v := s.Version
	if v == "" {
		v = "unknown"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versionJSON{Version: v})
}

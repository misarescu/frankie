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
	// frk-4: a deliberate M7.1 acceptance fault. The commit is always reported
	// empty, so a preview's driver can never see its head and its gate must
	// end inconclusive. Do not restore s.Version here as part of frk-4.
	v := ""
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versionJSON{Version: v})
}

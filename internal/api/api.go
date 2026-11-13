// Package api is the control plane's HTTP API for the web UI (web/) and the CLI,
// on the API port, which agent pods cannot reach. M1: runs, transcript replay and
// live transcript streaming (docs/spec/07-observability.md, FR-OB-1, FR-OB-2).
package api

import (
	"log/slog"
	"net/http"

	"github.com/misarescu/frankie/internal/store"
)

// Server holds the API's dependencies.
type Server struct {
	Store *store.Store
	Hub   *Hub
	Log   *slog.Logger
}

// Register adds the API routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("GET /api/runs/{id}/events", s.listEvents)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.streamEvents)
}

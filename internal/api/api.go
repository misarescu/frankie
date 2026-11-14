// Package api is the control plane's HTTP API for the web UI (web/) and the CLI,
// on the API port, which agent pods cannot reach. M1: runs, transcript replay and
// live transcript streaming (docs/spec/07-observability.md, FR-OB-1, FR-OB-2).
// M3: the approvals inbox and decisions on human gates (docs/spec/03, FR-WF-3).
// M4: a run's steps, and steering or aborting a running agent step (docs/spec/04).
// M5: every write needs the API token (D58); reads stay open.
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
	// Signaler delivers human gate decisions, steers and aborts to
	// workflows; nil when the control plane runs without Temporal.
	Signaler Signaler
	// Token is the bearer token every write request must carry (D58); with
	// none set, writes are refused.
	Token string
}

// Register adds the API routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("GET /api/runs/{id}/events", s.listEvents)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.streamEvents)
	mux.HandleFunc("GET /api/approvals", s.listApprovals)
	mux.HandleFunc("POST /api/gates/{id}/decision", s.requireToken(s.decideGate))
	mux.HandleFunc("GET /api/runs/{id}/steps", s.listSteps)
	mux.HandleFunc("POST /api/steps/{id}/steer", s.requireToken(s.steerStep))
	mux.HandleFunc("POST /api/steps/{id}/abort", s.requireToken(s.abortStep))
	mux.HandleFunc("GET /api/questions", s.listQuestions)
	mux.HandleFunc("POST /api/questions/{id}/answer", s.requireToken(s.answerQuestion))
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
)

// Signaler delivers a signal to a running workflow; the Temporal client is one.
type Signaler interface {
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg any) error
}

// approvalJSON is a pending approval as the inbox shows it.
type approvalJSON struct {
	StepRunID        uuid.UUID `json:"step_run_id"`
	RunID            uuid.UUID `json:"run_id"`
	Step             string    `json:"step"`
	IssueKey         string    `json:"issue_key"`
	IssueTitle       string    `json:"issue_title"`
	Reason           string    `json:"reason"`
	CommitSHA        string    `json:"commit_sha,omitempty"`
	ChangeRequestURL string    `json:"change_request_url,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// listApprovals is the approvals inbox: human gates waiting for a decision.
func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	pending, err := s.Store.ListPendingApprovals(r.Context())
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := make([]approvalJSON, 0, len(pending))
	for _, p := range pending {
		out = append(out, approvalJSON{StepRunID: p.StepRunID, RunID: p.RunID, Step: p.StepID, IssueKey: p.IssueKey,
			IssueTitle: p.IssueTitle, Reason: p.Reason, CommitSHA: p.CommitSHA, ChangeRequestURL: p.ChangeRequestURL,
			ExpiresAt: p.ExpiresAt, CreatedAt: p.CreatedAt})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// decisionRequest is the body of POST /api/gates/{id}/decision.
type decisionRequest struct {
	Decision string `json:"decision"` // approve or reject
	Comment  string `json:"comment"`
	By       string `json:"by"` // who decides; until auth exists (open question 7) the client says
}

const (
	maxDecisionBody = 16 << 10
	maxComment      = 4000
	maxBy           = 100
)

// decideGate sends a person's decision on a pending human gate to the run's
// workflow (FR-WF-3). The workflow records it; a second decision on the same
// gate finds it no longer pending. Only JSON bodies are accepted, so a
// cross-site form cannot post one.
func (s *Server) decideGate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid gate id", http.StatusBadRequest)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	var req decisionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDecisionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Comment, req.By = strings.TrimSpace(req.Comment), strings.TrimSpace(req.By)
	switch {
	case req.Decision != "approve" && req.Decision != "reject":
		http.Error(w, `decision must be "approve" or "reject"`, http.StatusBadRequest)
		return
	case len(req.Comment) > maxComment || len(req.By) > maxBy:
		http.Error(w, "comment or by is too long", http.StatusBadRequest)
		return
	}
	if req.By == "" {
		req.By = "web"
	}
	if s.Signaler == nil {
		http.Error(w, "workflows are not running", http.StatusServiceUnavailable)
		return
	}

	p, err := s.Store.GetPendingApproval(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if _, err := s.Store.GetApproval(r.Context(), id); err == nil {
			http.Error(w, "the gate was already decided", http.StatusConflict)
		} else {
			http.NotFound(w, r)
		}
		return
	case err != nil:
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	decision := workflow.HumanDecision{StepRunID: id.String(), Approved: req.Decision == "approve", By: req.By, Comment: req.Comment}
	if err := s.Signaler.SignalWorkflow(r.Context(), p.TemporalWorkflowID, p.TemporalRunID, workflow.HumanDecisionSignal, decision); err != nil {
		s.Log.Warn("signal human decision", "gate", id, "error", err)
		http.Error(w, "could not reach the run's workflow", http.StatusBadGateway)
		return
	}
	s.Log.Info("human decision sent", "gate", id, "issue", p.IssueKey, "decision", req.Decision, "by", req.By)
	w.WriteHeader(http.StatusAccepted)
}

package api

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
)

// questionJSON is a pending question as the inbox shows it.
type questionJSON struct {
	ID         uuid.UUID `json:"id"`
	StepRunID  uuid.UUID `json:"step_run_id"`
	RunID      uuid.UUID `json:"run_id"`
	Step       string    `json:"step"`
	IssueKey   string    `json:"issue_key"`
	IssueTitle string    `json:"issue_title"`
	Question   string    `json:"question"`
	CreatedAt  time.Time `json:"created_at"`
}

// listQuestions is the questions inbox: what agents of running steps asked
// with the ask-human tool and nobody answered yet (D55).
func (s *Server) listQuestions(w http.ResponseWriter, r *http.Request) {
	pending, err := s.Store.ListPendingQuestions(r.Context())
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := make([]questionJSON, 0, len(pending))
	for _, p := range pending {
		out = append(out, questionJSON{ID: p.ID, StepRunID: p.StepRunID, RunID: p.RunID, Step: p.StepID, IssueKey: p.IssueKey,
			IssueTitle: p.IssueTitle, Question: p.Text, CreatedAt: p.CreatedAt})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// answerRequest is the body of POST /api/questions/{id}/answer.
type answerRequest struct {
	Text string `json:"text"`
	By   string `json:"by"` // until auth exists (open question 7) the client says
}

// answerQuestion sends a person's answer to a pending question to the run's
// workflow, which relays it to the step's runner and records it (D55). The
// first answer the runner takes stands; a question already answered, or whose
// step ended, is a conflict. Only JSON bodies are accepted, so a cross-site
// form cannot post one.
func (s *Server) answerQuestion(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid question id", http.StatusBadRequest)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	var req answerRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInterventionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	text, by := strings.TrimSpace(req.Text), strings.TrimSpace(req.By)
	switch {
	case text == "":
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	case len(text) > maxInterventionText || len(by) > maxBy:
		http.Error(w, "text or by is too long", http.StatusBadRequest)
		return
	}
	if by == "" {
		by = "web"
	}
	if s.Signaler == nil {
		http.Error(w, "workflows are not running", http.StatusServiceUnavailable)
		return
	}

	q, err := s.Store.GetPendingQuestion(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if cur, err := s.Store.GetQuestion(r.Context(), id); err == nil {
			msg := "the question was already answered"
			if cur.Status != domain.QuestionAnswered {
				msg = "the agent no longer waits for an answer"
			}
			http.Error(w, msg, http.StatusConflict)
		} else {
			http.NotFound(w, r)
		}
		return
	case err != nil:
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	in := workflow.StepIntervention{StepRunID: q.StepRunID.String(), QuestionID: q.QuestionID, By: by, Text: text}
	if err := s.Signaler.SignalWorkflow(r.Context(), q.TemporalWorkflowID, q.TemporalRunID, workflow.AnswerSignal, in); err != nil {
		s.Log.Warn("signal answer", "question", id, "error", err)
		http.Error(w, "could not reach the run's workflow", http.StatusBadGateway)
		return
	}
	s.Log.Info("answer sent", "question", id, "issue", q.IssueKey, "by", by)
	w.WriteHeader(http.StatusAccepted)
}

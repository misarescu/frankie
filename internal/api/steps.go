package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
)

// Limits on an intervention's body.
const (
	maxInterventionBody = 16 << 10
	maxInterventionText = 8000
)

// stepJSON is a step execution as the run page shows it.
type stepJSON struct {
	ID        uuid.UUID `json:"id"`
	StepID    string    `json:"step_id"`
	Attempt   int       `json:"attempt"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	CommitSHA string    `json:"commit_sha,omitempty"`
	// Model usage of an agent step's sessions (D53).
	CostUSD      float64    `json:"cost_usd"`
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	// WaitsFor says what a step waits for: from a person, "approval" (a human
	// gate's decision) or "answer" (an ask-human question); or the build of
	// its image, "toolchain-image" or "dependency-image" (D93).
	WaitsFor string `json:"waits_for,omitempty"`
	stepDetail
}

// stepDetail is what the run's snapshot says is behind a step (M5.1): an agent
// step's role, harness and model, or a gate's type. Model is empty when neither
// the step nor its role names one and the platform's default applies.
type stepDetail struct {
	Role    string `json:"role,omitempty"`
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Gate    string `json:"gate,omitempty"`
}

// stepDetails reads a run's snapshot (invariant 4) for what is behind each of its
// steps, by step ID, including the branches of parallel groups. A snapshot it
// cannot read (an early run's, or an invalid configuration's) gives no details.
func stepDetails(raw json.RawMessage) map[string]stepDetail {
	var snap snapshot.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil
	}
	out := map[string]stepDetail{}
	var walk func(steps []config.Step)
	walk = func(steps []config.Step) {
		for _, st := range steps {
			var d stepDetail
			if st.Gate != nil {
				d.Gate = string(st.Gate.Type)
			}
			if a := st.Agent; a != nil {
				d.Role, d.Harness = a.RoleName(), a.Harness
				d.Model = cmp.Or(a.Model, snap.Roles[a.RoleName()].DefaultModel)
			}
			if d != (stepDetail{}) {
				out[st.ID] = d
			}
			walk(st.Parallel)
		}
	}
	walk(snap.Workflow.Steps)
	return out
}

// planStepJSON is one step of a run's workflow, in the order it runs (M5.1).
type planStepJSON struct {
	StepID string `json:"step_id"`
	Kind   string `json:"kind"` // agent, gate, action
	// When is the step's condition (D94), so a timeline can show a step the
	// run went past without running as skipped, and why (D98).
	When *config.When `json:"when,omitempty"`
	stepDetail
}

// runPlan lists the steps a run's snapshot executes, in order: a parallel
// group's branches, then the group's gate, under the IDs their step executions
// get. A snapshot it cannot read gives no steps.
func runPlan(raw json.RawMessage) []planStepJSON {
	var snap snapshot.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return []planStepJSON{}
	}
	details := stepDetails(raw)
	out := []planStepJSON{}
	add := func(id string, kind config.StepKind, when *config.When) {
		out = append(out, planStepJSON{StepID: id, Kind: string(kind), When: when, stepDetail: details[id]})
	}
	for _, st := range snap.Workflow.Steps {
		switch k := st.Kind(); k {
		case config.StepParallel:
			// A group's condition holds for its branches and its gate.
			for _, b := range st.Parallel {
				add(b.ID, b.Kind(), cmp.Or(b.When, st.When))
			}
			if st.Gate != nil {
				add(st.ID, config.StepGate, st.When)
			}
		case "":
			// not a valid step; a snapshot that passed validation has none
		default:
			add(st.ID, k, st.When)
		}
	}
	return out
}

// listPlan lists the steps a run executes, so a timeline can show the ones not
// reached yet: GET /api/runs/{id}/plan.
func (s *Server) listPlan(w http.ResponseWriter, r *http.Request) {
	run, ok := s.run(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runPlan(run.WorkflowSnapshot))
}

// listSteps lists a run's step executions in order, so the run page knows
// which agent step is running.
func (s *Server) listSteps(w http.ResponseWriter, r *http.Request) {
	run, ok := s.run(w, r)
	if !ok {
		return
	}
	steps, err := s.Store.ListStepRuns(r.Context(), run.ID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	waits, err := s.Store.ListRunWaits(r.Context(), run.ID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	details := stepDetails(run.WorkflowSnapshot)
	out := make([]stepJSON, 0, len(steps))
	for _, st := range steps {
		out = append(out, stepJSON{ID: st.ID, StepID: st.StepID, Attempt: st.Attempt, Kind: string(st.Kind), Status: string(st.Status),
			CommitSHA: st.CommitSHA, CostUSD: st.Usage.CostUSD, InputTokens: st.Usage.InputTokens, OutputTokens: st.Usage.OutputTokens,
			StartedAt: st.StartedAt, FinishedAt: st.FinishedAt, WaitsFor: string(waits[st.ID]), stepDetail: details[st.StepID]})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// interventionRequest is the body of a steer (text) or an abort (reason).
type interventionRequest struct {
	Text   string `json:"text"`
	Reason string `json:"reason"`
	By     string `json:"by"`
}

func (s *Server) steerStep(w http.ResponseWriter, r *http.Request) {
	s.intervene(w, r, workflow.SteerSignal)
}

func (s *Server) abortStep(w http.ResponseWriter, r *http.Request) {
	s.intervene(w, r, workflow.AbortSignal)
}

// intervene sends a steer or an abort to the workflow running the agent
// step, which relays it to the step's runner and records it (docs/spec/04,
// "Human intervention"). Only a running agent step takes one. Without
// authentication (open question 7) the sender is self-reported.
func (s *Server) intervene(w http.ResponseWriter, r *http.Request, signal string) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid step id", http.StatusBadRequest)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	var req interventionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInterventionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	text, field := strings.TrimSpace(req.Text), "text"
	if signal == workflow.AbortSignal {
		text, field = strings.TrimSpace(req.Reason), "reason"
	}
	switch {
	case text == "":
		http.Error(w, field+" is required", http.StatusBadRequest)
		return
	case len(text) > maxInterventionText:
		http.Error(w, field+" is too long", http.StatusBadRequest)
		return
	}
	by := actorName(r)
	if s.Signaler == nil {
		http.Error(w, "workflows are not running", http.StatusServiceUnavailable)
		return
	}

	step, err := s.Store.GetStepRun(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	case step.Kind != domain.StepAgent:
		http.Error(w, "only agent steps can be steered or aborted", http.StatusConflict)
		return
	case step.Status != domain.ExecRunning:
		http.Error(w, "the step is not running", http.StatusConflict)
		return
	}
	run, err := s.Store.GetRun(r.Context(), step.RunID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	in := workflow.StepIntervention{StepRunID: id.String(), By: by, Text: text}
	if err := s.Signaler.SignalWorkflow(r.Context(), run.TemporalWorkflowID, run.TemporalRunID, signal, in); err != nil {
		s.Log.Warn("signal intervention", "step", id, "signal", signal, "error", err)
		http.Error(w, "could not reach the run's workflow", http.StatusBadGateway)
		return
	}
	s.Log.Info("intervention sent", "step", id, "signal", signal, "by", by)
	w.WriteHeader(http.StatusAccepted)
}

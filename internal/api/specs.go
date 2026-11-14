package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// specJSON is a refined spec waiting for approval in the inbox (D64, D66):
// Spec, AcceptanceCriteria and FileHints are what approving makes the issue's.
type specJSON struct {
	Key                string   `json:"key"`
	Type               string   `json:"type"`
	Title              string   `json:"title"`
	Project            string   `json:"project"`
	Spec               string   `json:"spec"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	FileHints          []string `json:"file_hints"`
	// Summary is the refiner's note to the approver: what it changed and assumed.
	Summary string `json:"summary,omitempty"`
	// OriginalSpec is the issue's text before refinement.
	OriginalSpec string `json:"original_spec"`
	// RunID is the refinement's run, for its transcript.
	RunID     string   `json:"run_id,omitempty"`
	Labels    []string `json:"labels"`
	SourceRef string   `json:"source_ref,omitempty"`
	// RequestedBy asked to start the issue or a parent of it.
	RequestedBy string `json:"requested_by,omitempty"`
}

// listSpecs lists the refined specs waiting for a person: requested issues whose
// project or labels require approval, refined and not yet decided (D64, D66).
// GET /api/specs.
func (s *Server) listSpecs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	issues, deps, err := s.Store.SchedulingState(ctx)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	plan := domain.PlanSchedule(issues, deps, max(s.MaxConcurrentIssues, 1))
	projects := map[string]string{}
	out := make([]specJSON, 0, len(plan.AwaitingApproval))
	for _, a := range plan.AwaitingApproval {
		is, err := s.Store.GetIssue(ctx, a.ID)
		if err != nil {
			http.Error(w, "store error", http.StatusInternalServerError)
			return
		}
		name, ok := projects[is.ProjectID.String()]
		if !ok {
			p, err := s.Store.GetProject(ctx, is.ProjectID)
			if err != nil {
				http.Error(w, "store error", http.StatusInternalServerError)
				return
			}
			name = p.Name
			projects[is.ProjectID.String()] = name
		}
		if is.SpecProposal == nil {
			continue // decided since the plan was made
		}
		p := is.SpecProposal
		sp := specJSON{Key: is.Key, Type: string(is.Type), Title: is.Title, Project: name, Spec: p.Spec,
			AcceptanceCriteria: nonNilStrings(p.AcceptanceCriteria), FileHints: nonNilStrings(p.FileHints), Summary: p.Summary,
			OriginalSpec: is.Spec, Labels: nonNilStrings(is.Labels), SourceRef: is.SourceRef, RequestedBy: s.requester(r, is)}
		if runs, err := s.Store.ListIssueRuns(ctx, is.ID); err == nil && len(runs) > 0 {
			sp.RunID = runs[0].ID.String()
		}
		out = append(out, sp)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// requester is who asked to start the issue, or the nearest parent someone asked to start.
func (s *Server) requester(r *http.Request, is domain.Issue) string {
	seen := map[string]bool{}
	for cur := is; !seen[cur.Key]; {
		seen[cur.Key] = true
		if cur.StartRequestedBy != "" {
			return cur.StartRequestedBy
		}
		if cur.ParentID == nil {
			break
		}
		parent, err := s.Store.GetIssue(r.Context(), *cur.ParentID)
		if err != nil {
			break
		}
		cur = parent
	}
	return ""
}

// specDecisionRequest is the body of POST /api/issues/{key}/spec.
type specDecisionRequest struct {
	Decision string `json:"decision"` // approve or reject
	Comment  string `json:"comment"`  // required to reject: what to change
	By       string `json:"by"`
}

// decideSpec approves or rejects an issue's spec (D64, D66): the refiner's result
// for a refining issue, or a draft's text as written. Approving makes the issue
// ready (with the refined spec, if any), and the scheduler starts it once
// requested and its dependencies are done; rejecting returns it to draft and
// holds it until its text changes or someone asks to start it again, when the
// refiner works again with the feedback.
func (s *Server) decideSpec(w http.ResponseWriter, r *http.Request) {
	var req specDecisionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	by, ok := cleanBy(req.By)
	comment := strings.TrimSpace(req.Comment)
	switch {
	case !ok || len(comment) > maxComment:
		http.Error(w, "comment or by is too long", http.StatusBadRequest)
		return
	case req.Decision != "approve" && req.Decision != "reject":
		http.Error(w, `decision must be "approve" or "reject"`, http.StatusBadRequest)
		return
	case req.Decision == "reject" && comment == "":
		http.Error(w, "say what to change in the comment", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	is, err := s.Store.GetIssueByKey(ctx, r.PathValue("key"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such issue", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	var got domain.Issue
	if req.Decision == "approve" {
		got, err = s.Store.ApproveSpec(ctx, is.ID, by)
	} else {
		got, err = s.Store.RejectSpec(ctx, is.ID, fmt.Sprintf("%s (%s)", comment, by))
	}
	if errors.Is(err, store.ErrConflict) {
		http.Error(w, strings.TrimPrefix(err.Error(), "store: "), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.Log.Info("spec decided", "issue", got.Key, "decision", req.Decision, "by", by)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(issueRef(got))
}

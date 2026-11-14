package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

const maxStartKeys = 100

// startRequest asks to start issues; a parent covers everything under it (D62).
type startRequest struct {
	Keys []string `json:"keys"`
	By   string   `json:"by"` // who asks; until per-person identity (open question 7) the client says
}

type waitingJSON struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// startResponse is the scheduler's current plan for the requested issues and
// everything under them: what starts on its next pass, and what waits and why.
type startResponse struct {
	Requested []string      `json:"requested"`
	Starting  []string      `json:"starting"`
	Waiting   []waitingJSON `json:"waiting"`
}

// cleanBy checks the "by" field of the scheduling writes; empty means the web UI.
func cleanBy(by string) (string, bool) {
	by = strings.TrimSpace(by)
	if len(by) > maxBy {
		return "", false
	}
	if by == "" {
		by = "web"
	}
	return by, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInterventionBody)).Decode(v); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return false
	}
	return true
}

// startIssues asks the scheduler to start issues: POST /api/issues/start.
func (s *Server) startIssues(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	by, ok := cleanBy(req.By)
	switch {
	case !ok:
		http.Error(w, "by is too long", http.StatusBadRequest)
		return
	case len(req.Keys) == 0 || len(req.Keys) > maxStartKeys:
		http.Error(w, fmt.Sprintf("keys must name 1 to %d issues", maxStartKeys), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	ids := make([]uuid.UUID, 0, len(req.Keys))
	for _, key := range req.Keys {
		is, err := s.Store.GetIssueByKey(ctx, key)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "no such issue: "+key, http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "store error", http.StatusInternalServerError)
			return
		}
		ids = append(ids, is.ID)
	}
	requested, err := s.Store.RequestIssueStart(ctx, ids, by)
	if errors.Is(err, store.ErrConflict) {
		http.Error(w, strings.TrimPrefix(err.Error(), "store: "), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out, err := s.planFor(r, ids)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	for _, is := range requested {
		out.Requested = append(out.Requested, is.Key)
	}
	s.Log.Info("start requested", "issues", out.Requested, "by", by)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(out)
}

// planFor is the scheduler's plan restricted to roots and everything under them.
func (s *Server) planFor(r *http.Request, roots []uuid.UUID) (startResponse, error) {
	issues, deps, err := s.Store.SchedulingState(r.Context())
	if err != nil {
		return startResponse{}, err
	}
	plan := domain.PlanSchedule(issues, deps, max(s.MaxConcurrentIssues, 1))
	inScope := map[string]bool{}
	byID := make(map[uuid.UUID]domain.SchedIssue, len(issues))
	for _, is := range issues {
		byID[is.ID] = is
	}
	isRoot := map[uuid.UUID]bool{}
	for _, id := range roots {
		isRoot[id] = true
	}
	for _, is := range issues {
		seen := map[uuid.UUID]bool{}
		for cur, ok := is, true; ok && !seen[cur.ID]; {
			seen[cur.ID] = true
			if isRoot[cur.ID] {
				inScope[is.Key] = true
				break
			}
			if cur.ParentID == nil {
				break
			}
			cur, ok = byID[*cur.ParentID]
		}
	}
	out := startResponse{Starting: []string{}, Waiting: []waitingJSON{}}
	for _, is := range plan.Start {
		if inScope[is.Key] {
			out.Starting = append(out.Starting, is.Key)
		}
	}
	for _, w := range plan.Waiting {
		if inScope[w.Key] {
			out.Waiting = append(out.Waiting, waitingJSON(w))
		}
	}
	return out, nil
}

type retryRequest struct {
	By string `json:"by"`
}

// retryIssue re-arms a failed issue and asks to start it: POST /api/issues/{key}/retry.
func (s *Server) retryIssue(w http.ResponseWriter, r *http.Request) {
	var req retryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	by, ok := cleanBy(req.By)
	if !ok {
		http.Error(w, "by is too long", http.StatusBadRequest)
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
	if is.Status != domain.IssueFailed {
		http.Error(w, fmt.Sprintf("%s is %s; only a failed issue can be retried", is.Key, is.Status), http.StatusConflict)
		return
	}
	got, err := s.Store.RetryIssue(ctx, is.ID, by)
	if errors.Is(err, store.ErrConflict) || errors.Is(err, domain.ErrInvalidTransition) {
		http.Error(w, "the issue changed; reload it", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.Log.Info("retry requested", "issue", got.Key, "by", by)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(issueRef(got))
}

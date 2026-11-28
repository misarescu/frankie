package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

type closeRequest struct {
	By string `json:"by"`
	// Reason says why an issue is canceled; completing needs none.
	Reason string `json:"reason"`
}

// closeReply lists the issues a close moved, the one asked about first.
type closeReply struct {
	Closed []issueRefJSON `json:"closed"`
}

// completeIssue moves a parent whose sub-issues are all finished to merged
// (D70): POST /api/issues/{key}/complete.
func (s *Server) completeIssue(w http.ResponseWriter, r *http.Request) {
	s.closeIssue(w, r, func(is domain.Issue, by, _ string) ([]domain.Issue, error) {
		got, err := s.Store.CompleteIssue(r.Context(), is.ID, by)
		return []domain.Issue{got}, err
	})
}

// cancelIssue cancels an unfinished issue and everything unfinished under it,
// with a reason (D70): POST /api/issues/{key}/cancel.
func (s *Server) cancelIssue(w http.ResponseWriter, r *http.Request) {
	s.closeIssue(w, r, func(is domain.Issue, by, reason string) ([]domain.Issue, error) {
		return s.Store.CancelIssue(r.Context(), is.ID, by, reason)
	})
}

// closeIssue is what completing and canceling share: the request, the issue,
// and the answer. A close the domain refuses is a 409 saying why.
func (s *Server) closeIssue(w http.ResponseWriter, r *http.Request, close func(is domain.Issue, by, reason string) ([]domain.Issue, error)) {
	var req closeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	by := actorName(r)
	is, err := s.Store.GetIssueByKey(r.Context(), r.PathValue("key"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such issue", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	closed, err := close(is, by, req.Reason)
	switch {
	case errors.Is(err, domain.ErrCannotClose):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, store.ErrConflict):
		http.Error(w, "the issue changed; reload it", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := closeReply{Closed: make([]issueRefJSON, len(closed))}
	keys := make([]string, len(closed))
	for i, c := range closed {
		out.Closed[i], keys[i] = issueRef(c), c.Key
	}
	s.Log.Info("issues closed", "issue", is.Key, "closed", keys, "by", by, "reason", req.Reason)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

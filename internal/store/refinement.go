package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// RecordRefinement stores the refiner's result on a refining issue, where it
// waits for a person's approval, and ends the refinement's run as succeeded
// (D66). Repeating it is harmless, so a retried Activity is safe.
func (s *Store) RecordRefinement(ctx context.Context, issueID, runID uuid.UUID, spec domain.RefinedSpec) error {
	raw, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("store: encode refined spec: %w", err)
	}
	if _, err := s.q.SetSpecProposal(ctx, db.SetSpecProposalParams{ID: issueID, SpecProposal: raw}); err != nil {
		if err = mapErr("set spec proposal", err); errors.Is(err, ErrNotFound) {
			return fmt.Errorf("store: record refinement of %s: %w: the issue is no longer refining", issueID, ErrConflict)
		}
		return err
	}
	_, err = s.TransitionRun(ctx, runID, domain.ExecRunning, domain.ExecSucceeded)
	return err
}

// FailRefinement ends a refinement that produced no usable spec: its run fails,
// and the issue returns to draft, held with the reason as feedback like a
// rejection, so a failing refiner does not run again until someone asks (D66).
func (s *Store) FailRefinement(ctx context.Context, issueID, runID uuid.UUID, reason string) error {
	if _, err := s.TransitionRun(ctx, runID, domain.ExecRunning, domain.ExecFailed); err != nil {
		return err
	}
	return s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetIssue(ctx, issueID)
		if err != nil {
			return mapErr("get issue", err)
		}
		if domain.IssueStatus(cur.Status) == domain.IssueRefining {
			if err := transitionIssueTx(ctx, q, issueID, domain.IssueRefining, domain.IssueDraft); err != nil {
				return err
			}
		} else if domain.IssueStatus(cur.Status) != domain.IssueDraft {
			return nil // a person moved it on; nothing to hold
		}
		_, err = q.RejectSpec(ctx, db.RejectSpecParams{ID: issueID, Feedback: "refinement failed: " + reason})
		return mapErr("hold issue", err)
	})
}

func proposalFromRow(raw []byte) *domain.RefinedSpec {
	if len(raw) == 0 {
		return nil
	}
	var p domain.RefinedSpec
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	return &p
}

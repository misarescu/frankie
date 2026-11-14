package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// SetProjectSpecApproval records the project's specApproval setting, as intake
// read it from the repo (D64).
func (s *Store) SetProjectSpecApproval(ctx context.Context, id uuid.UUID, required bool) error {
	return mapErr("set project spec approval", s.q.SetProjectSpecApproval(ctx, db.SetProjectSpecApprovalParams{ID: id, SpecApproval: required}))
}

// ApproveSpec records a person's approval of a draft's spec and makes the issue
// ready (draft -> ready, "approved without refinement" in docs/spec/02), in one
// transaction with its state_change event (D64).
func (s *Store) ApproveSpec(ctx context.Context, id uuid.UUID, by string) (domain.Issue, error) {
	if strings.TrimSpace(by) == "" {
		return domain.Issue{}, errors.New("store: approve spec: empty approver")
	}
	var out domain.Issue
	err := s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.TransitionIssue(ctx, db.TransitionIssueParams{ID: id, FromStatus: string(domain.IssueDraft), ToStatus: string(domain.IssueReady)}); err != nil {
			if err = mapErr("transition issue", err); errors.Is(err, ErrNotFound) {
				return fmt.Errorf("store: approve spec of %s: %w: not a draft", id, ErrConflict)
			}
			return err
		}
		if err := appendStateChange(ctx, q, nil, nil, domain.StateChange{Entity: "issue", ID: id,
			From: string(domain.IssueDraft), To: string(domain.IssueReady)}); err != nil {
			return err
		}
		row, err := q.SetSpecApproved(ctx, db.SetSpecApprovedParams{ID: id, ApprovedBy: &by})
		if err != nil {
			return mapErr("set spec approved", err)
		}
		out = issueFromRow(row)
		return nil
	})
	return out, err
}

// RejectSpec records why a person rejected a draft's spec. The draft stays a draft
// and is held: the scheduler leaves it until its text changes on the source or
// someone asks to start it again (D64).
func (s *Store) RejectSpec(ctx context.Context, id uuid.UUID, feedback string) (domain.Issue, error) {
	if strings.TrimSpace(feedback) == "" {
		return domain.Issue{}, errors.New("store: reject spec: empty feedback")
	}
	row, err := s.q.RejectSpec(ctx, db.RejectSpecParams{ID: id, Feedback: feedback})
	if err = mapErr("reject spec", err); errors.Is(err, ErrNotFound) {
		return domain.Issue{}, fmt.Errorf("store: reject spec of %s: %w: not a draft", id, ErrConflict)
	}
	if err != nil {
		return domain.Issue{}, err
	}
	return issueFromRow(row), nil
}

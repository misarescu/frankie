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

// ApproveSpec records a person's approval of an issue's spec and makes the issue
// ready, in one transaction with its state_change event (D64, D66). A draft is
// approved as written ("approved without refinement" in docs/spec/02); a refining
// issue must carry the refiner's result, which becomes its spec, acceptance
// criteria and file hints.
func (s *Store) ApproveSpec(ctx context.Context, id uuid.UUID, by string) (domain.Issue, error) {
	if strings.TrimSpace(by) == "" {
		return domain.Issue{}, errors.New("store: approve spec: empty approver")
	}
	var out domain.Issue
	err := s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetIssue(ctx, id)
		if err != nil {
			return mapErr("get issue", err)
		}
		from := domain.IssueStatus(cur.Status)
		switch {
		case from == domain.IssueRefining && cur.SpecProposal == nil:
			return fmt.Errorf("store: approve spec of %s: %w: the refiner has not returned a spec yet", cur.Key, ErrConflict)
		case from == domain.IssueRefining:
			p := proposalFromRow(cur.SpecProposal)
			if _, err := q.ApplySpecProposal(ctx, db.ApplySpecProposalParams{ID: id, Spec: p.Spec,
				AcceptanceCriteria: nonNil(p.AcceptanceCriteria), FileHints: nonNil(p.FileHints)}); err != nil {
				return mapErr("apply spec proposal", err)
			}
		case from != domain.IssueDraft:
			return fmt.Errorf("store: approve spec of %s: %w: it is %s", cur.Key, ErrConflict, from)
		}
		if err := transitionIssueTx(ctx, q, id, from, domain.IssueReady); err != nil {
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

// RejectSpec records why a person rejected an issue's spec: a draft's as written,
// or the refiner's result (the issue returns from refining to draft and the result
// is dropped). The draft is held: the scheduler leaves it until its text changes
// on the source or someone asks to start it again, and the next refinement gets
// the feedback (D64, D66).
func (s *Store) RejectSpec(ctx context.Context, id uuid.UUID, feedback string) (domain.Issue, error) {
	if strings.TrimSpace(feedback) == "" {
		return domain.Issue{}, errors.New("store: reject spec: empty feedback")
	}
	var out domain.Issue
	err := s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetIssue(ctx, id)
		if err != nil {
			return mapErr("get issue", err)
		}
		switch from := domain.IssueStatus(cur.Status); {
		case from == domain.IssueRefining && cur.SpecProposal == nil:
			return fmt.Errorf("store: reject spec of %s: %w: the refiner has not returned a spec yet", cur.Key, ErrConflict)
		case from == domain.IssueRefining:
			if err := transitionIssueTx(ctx, q, id, from, domain.IssueDraft); err != nil {
				return err
			}
		case from != domain.IssueDraft:
			return fmt.Errorf("store: reject spec of %s: %w: it is %s", cur.Key, ErrConflict, from)
		}
		row, err := q.RejectSpec(ctx, db.RejectSpecParams{ID: id, Feedback: feedback})
		if err != nil {
			return mapErr("reject spec", err)
		}
		out = issueFromRow(row)
		return nil
	})
	return out, err
}

// transitionIssueTx moves an issue inside the caller's transaction, with its
// state_change event.
func transitionIssueTx(ctx context.Context, q *db.Queries, id uuid.UUID, from, to domain.IssueStatus) error {
	if !domain.CanTransitionIssue(from, to) {
		return fmt.Errorf("store: issue %s: %w: %s -> %s", id, domain.ErrInvalidTransition, from, to)
	}
	if _, err := q.TransitionIssue(ctx, db.TransitionIssueParams{ID: id, FromStatus: string(from), ToStatus: string(to)}); err != nil {
		if err = mapErr("transition issue", err); errors.Is(err, ErrNotFound) {
			return fmt.Errorf("store: issue %s: %w: it is no longer %s", id, ErrConflict, from)
		}
		return err
	}
	return appendStateChange(ctx, q, nil, nil, domain.StateChange{Entity: "issue", ID: id, From: string(from), To: string(to)})
}

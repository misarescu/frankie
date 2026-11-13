package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// NewApproval is the input for OpenApproval.
type NewApproval struct {
	StepRunID        uuid.UUID
	Reason           string
	CommitSHA        string
	ChangeRequestURL string
	ExpiresAt        time.Time
}

// PendingApproval is an open approval with what the inbox shows and what the
// API needs to signal the run's workflow.
type PendingApproval struct {
	domain.Approval
	StepID             string
	IssueKey           string
	IssueTitle         string
	TemporalWorkflowID string
	TemporalRunID      string
}

// OpenApproval records that a human gate waits for a decision and appends a
// state_change event to the run's stream. Opening it again (a retried
// Activity) returns the existing approval without a second event.
func (s *Store) OpenApproval(ctx context.Context, in NewApproval) (domain.Approval, error) {
	if in.Reason == "" || in.ExpiresAt.IsZero() {
		return domain.Approval{}, errors.New("store: approval: reason and expiry are required")
	}
	var out domain.Approval
	err := s.inTx(ctx, func(q *db.Queries) error {
		step, err := q.GetStepRun(ctx, in.StepRunID)
		if err != nil {
			return mapErr("approval step run", err)
		}
		id := domain.NewID()
		row, err := q.OpenApproval(ctx, db.OpenApprovalParams{ID: id, StepRunID: in.StepRunID, RunID: step.RunID, Reason: in.Reason,
			CommitSha: nonEmpty(in.CommitSHA), ChangeRequestUrl: nonEmpty(in.ChangeRequestURL), ExpiresAt: in.ExpiresAt})
		if err != nil {
			return mapErr("open approval", err)
		}
		out = approvalFromRow(row)
		if row.ID != id {
			return nil // opened by an earlier attempt, with its event
		}
		return appendStateChange(ctx, q, &step.RunID, &step.ID, domain.StateChange{Entity: "approval", ID: row.ID, From: "none", To: string(domain.ApprovalPending)})
	})
	return out, err
}

// DecideApproval closes a pending approval. Only the first decision counts: a
// later one returns ErrConflict, and repeating the same decision returns it
// unchanged, so a retried Activity is idempotent.
func (s *Store) DecideApproval(ctx context.Context, stepRunID uuid.UUID, status domain.ApprovalStatus, by, comment string) (domain.Approval, error) {
	if !status.Valid() || status == domain.ApprovalPending {
		return domain.Approval{}, fmt.Errorf("store: approval: %q is not a decision", status)
	}
	var out domain.Approval
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.DecideApproval(ctx, db.DecideApprovalParams{StepRunID: stepRunID, Status: string(status),
			DecidedBy: nonEmpty(by), Comment: nonEmpty(comment)})
		if err != nil {
			return mapErr("decide approval", err)
		}
		out = approvalFromRow(row)
		return appendStateChange(ctx, q, &row.RunID, &row.StepRunID, domain.StateChange{Entity: "approval", ID: row.ID,
			From: string(domain.ApprovalPending), To: string(status)})
	})
	if errors.Is(err, ErrNotFound) {
		current, getErr := s.GetApproval(ctx, stepRunID)
		if getErr != nil {
			return domain.Approval{}, getErr
		}
		if current.Status == status {
			return current, nil
		}
		return domain.Approval{}, fmt.Errorf("store: approval for step run %s is already %s: %w", stepRunID, current.Status, ErrConflict)
	}
	return out, err
}

// ExtendApproval moves a pending approval's expiry (a human gate's onTimeout: extend).
func (s *Store) ExtendApproval(ctx context.Context, stepRunID uuid.UUID, expiresAt time.Time) (domain.Approval, error) {
	row, err := s.q.ExtendApproval(ctx, db.ExtendApprovalParams{StepRunID: stepRunID, ExpiresAt: expiresAt})
	if err != nil {
		return domain.Approval{}, mapErr("extend approval", err)
	}
	return approvalFromRow(row), nil
}

// GetApproval returns the approval of a human gate's StepRun.
func (s *Store) GetApproval(ctx context.Context, stepRunID uuid.UUID) (domain.Approval, error) {
	row, err := s.q.GetApprovalByStepRun(ctx, stepRunID)
	if err != nil {
		return domain.Approval{}, mapErr("get approval", err)
	}
	return approvalFromRow(row), nil
}

// ListPendingApprovals is the approvals inbox, oldest first.
func (s *Store) ListPendingApprovals(ctx context.Context) ([]PendingApproval, error) {
	rows, err := s.q.ListPendingApprovals(ctx)
	if err != nil {
		return nil, mapErr("list pending approvals", err)
	}
	out := make([]PendingApproval, 0, len(rows))
	for _, r := range rows {
		out = append(out, PendingApproval{Approval: approvalFromRow(db.Approval{ID: r.ID, StepRunID: r.StepRunID, RunID: r.RunID,
			Reason: r.Reason, CommitSha: r.CommitSha, ChangeRequestUrl: r.ChangeRequestUrl, Status: r.Status, DecidedBy: r.DecidedBy,
			Comment: r.Comment, ExpiresAt: r.ExpiresAt, DecidedAt: r.DecidedAt, CreatedAt: r.CreatedAt}),
			StepID: r.StepID, IssueKey: r.IssueKey, IssueTitle: r.IssueTitle, TemporalWorkflowID: r.TemporalWorkflowID,
			TemporalRunID: deref(r.TemporalRunID)})
	}
	return out, nil
}

// GetPendingApproval returns the pending approval of a step run, or ErrNotFound
// when there is none or it was already decided.
func (s *Store) GetPendingApproval(ctx context.Context, stepRunID uuid.UUID) (PendingApproval, error) {
	r, err := s.q.GetPendingApproval(ctx, stepRunID)
	if err != nil {
		return PendingApproval{}, mapErr("get pending approval", err)
	}
	return PendingApproval{Approval: approvalFromRow(db.Approval{ID: r.ID, StepRunID: r.StepRunID, RunID: r.RunID,
		Reason: r.Reason, CommitSha: r.CommitSha, ChangeRequestUrl: r.ChangeRequestUrl, Status: r.Status, DecidedBy: r.DecidedBy,
		Comment: r.Comment, ExpiresAt: r.ExpiresAt, DecidedAt: r.DecidedAt, CreatedAt: r.CreatedAt}),
		StepID: r.StepID, IssueKey: r.IssueKey, IssueTitle: r.IssueTitle, TemporalWorkflowID: r.TemporalWorkflowID,
		TemporalRunID: deref(r.TemporalRunID)}, nil
}

func approvalFromRow(r db.Approval) domain.Approval {
	return domain.Approval{ID: r.ID, StepRunID: r.StepRunID, RunID: r.RunID, Reason: r.Reason, CommitSHA: deref(r.CommitSha),
		ChangeRequestURL: deref(r.ChangeRequestUrl), Status: domain.ApprovalStatus(r.Status), DecidedBy: deref(r.DecidedBy),
		Comment: deref(r.Comment), ExpiresAt: r.ExpiresAt, DecidedAt: r.DecidedAt, CreatedAt: r.CreatedAt}
}

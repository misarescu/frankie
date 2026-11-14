package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// NewChangeRequest is the input for UpsertChangeRequest.
type NewChangeRequest struct {
	IssueID     uuid.UUID
	RunID       uuid.UUID
	Repo        string
	Number      int
	ProviderRef string
	Branch      string
	HeadSHA     string
}

// UpsertChangeRequest records a change request; recording the same (repo, number)
// again updates its head and run, so retried Activities stay idempotent.
func (s *Store) UpsertChangeRequest(ctx context.Context, in NewChangeRequest) (domain.ChangeRequest, error) {
	if in.Repo == "" || in.Number < 1 || in.ProviderRef == "" || in.Branch == "" || in.HeadSHA == "" {
		return domain.ChangeRequest{}, errors.New("store: change request needs repo, number, provider ref, branch and head")
	}
	row, err := s.q.UpsertChangeRequest(ctx, db.UpsertChangeRequestParams{
		ID: domain.NewID(), IssueID: in.IssueID, RunID: in.RunID, Repo: in.Repo,
		Number: int32(in.Number), ProviderRef: in.ProviderRef, Branch: in.Branch, HeadSha: in.HeadSHA,
	})
	if err != nil {
		return domain.ChangeRequest{}, mapErr("upsert change request", err)
	}
	return changeRequestFromRow(row), nil
}

// ListChangeRequestsForIssue returns an issue's change requests, oldest first.
func (s *Store) ListChangeRequestsForIssue(ctx context.Context, issueID uuid.UUID) ([]domain.ChangeRequest, error) {
	rows, err := s.q.ListChangeRequestsForIssue(ctx, issueID)
	if err != nil {
		return nil, mapErr("list change requests", err)
	}
	out := make([]domain.ChangeRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, changeRequestFromRow(r))
	}
	return out, nil
}

func changeRequestFromRow(r db.ChangeRequest) domain.ChangeRequest {
	return domain.ChangeRequest{
		ID: r.ID, IssueID: r.IssueID, RunID: r.RunID, Repo: r.Repo, Number: int(r.Number),
		ProviderRef: r.ProviderRef, Branch: r.Branch, HeadSHA: r.HeadSha, Status: r.Status,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// ListChangeRequestsAwaitingMerge returns the open change requests of issues in
// awaiting_human, oldest first: the ones a person may merge on the provider (D61).
func (s *Store) ListChangeRequestsAwaitingMerge(ctx context.Context) ([]domain.ChangeRequest, error) {
	rows, err := s.q.ListChangeRequestsAwaitingMerge(ctx)
	if err != nil {
		return nil, mapErr("list change requests awaiting merge", err)
	}
	out := make([]domain.ChangeRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, changeRequestFromRow(r))
	}
	return out, nil
}

// RecordChangeRequestMerged marks an open change request merged and moves its issue
// from awaiting_human to merged in one transaction, each with a state_change event
// (D61). An issue in any other status keeps it: the change request is still recorded
// as merged. Recording a merge again is a no-op.
func (s *Store) RecordChangeRequestMerged(ctx context.Context, id uuid.UUID) (domain.Issue, error) {
	var issueID uuid.UUID
	err := s.inTx(ctx, func(q *db.Queries) error {
		cr, changed, err := finishChangeRequest(ctx, q, id, domain.ChangeRequestMerged)
		if err != nil {
			return err
		}
		issueID = cr.IssueID
		if !changed {
			return nil
		}
		_, err = q.TransitionIssue(ctx, db.TransitionIssueParams{ID: cr.IssueID,
			FromStatus: string(domain.IssueAwaitingHuman), ToStatus: string(domain.IssueMerged)})
		if errors.Is(mapErr("", err), ErrNotFound) {
			return nil // the issue moved on (e.g. a new run); only the change request is merged
		}
		if err != nil {
			return mapErr("transition issue", err)
		}
		return appendStateChange(ctx, q, nil, nil, domain.StateChange{Entity: "issue", ID: cr.IssueID,
			From: string(domain.IssueAwaitingHuman), To: string(domain.IssueMerged)})
	})
	if err != nil {
		return domain.Issue{}, err
	}
	return s.GetIssue(ctx, issueID)
}

// RecordChangeRequestClosed marks an open change request closed without a merge.
// Its issue keeps its status: what a rejected change request means for the issue is
// open question 11. Recording it again is a no-op.
func (s *Store) RecordChangeRequestClosed(ctx context.Context, id uuid.UUID) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		_, _, err := finishChangeRequest(ctx, q, id, domain.ChangeRequestClosed)
		return err
	})
}

// finishChangeRequest moves a change request from open to status with a
// state_change event on its run. changed is false when it already had status.
func finishChangeRequest(ctx context.Context, q *db.Queries, id uuid.UUID, status domain.ChangeRequestStatus) (cr db.ChangeRequest, changed bool, err error) {
	cr, err = q.FinishChangeRequest(ctx, db.FinishChangeRequestParams{ID: id, Status: string(status)})
	if err = mapErr("finish change request", err); errors.Is(err, ErrNotFound) {
		cur, err := q.GetChangeRequest(ctx, id)
		if err != nil {
			return cur, false, mapErr("get change request", err)
		}
		if cur.Status == string(status) {
			return cur, false, nil
		}
		return cur, false, fmt.Errorf("store: change request %s: %w: expected open, found %s", id, ErrConflict, cur.Status)
	}
	if err != nil {
		return cr, false, err
	}
	return cr, true, appendStateChange(ctx, q, &cr.RunID, nil, domain.StateChange{Entity: "change_request", ID: id,
		From: string(domain.ChangeRequestOpen), To: string(status)})
}

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// SchedulingState returns every issue and dependency as the scheduler plans with
// them (D62).
func (s *Store) SchedulingState(ctx context.Context) ([]domain.SchedIssue, []domain.Dependency, error) {
	rows, err := s.q.ListSchedIssues(ctx)
	if err != nil {
		return nil, nil, mapErr("list scheduling issues", err)
	}
	issues := make([]domain.SchedIssue, len(rows))
	for i, r := range rows {
		issues[i] = domain.SchedIssue{ID: r.ID, Key: r.Key, Type: domain.IssueType(r.Type), ParentID: r.ParentID,
			Status: domain.IssueStatus(r.Status), Priority: int(r.Priority), Number: int(r.Number), Requested: r.Requested, Busy: r.Busy}
	}
	depRows, err := s.q.ListAllDependencies(ctx)
	if err != nil {
		return nil, nil, mapErr("list dependencies", err)
	}
	deps := make([]domain.Dependency, len(depRows))
	for i, r := range depRows {
		deps[i] = domain.Dependency{IssueID: r.IssueID, DependsOnID: r.DependsOnID}
	}
	return issues, deps, nil
}

// RequestIssueStart asks to start each issue, and so everything under it (D62).
// Asking again keeps the first request. A terminal issue (released or canceled)
// cannot be started.
func (s *Store) RequestIssueStart(ctx context.Context, ids []uuid.UUID, by string) ([]domain.Issue, error) {
	if by == "" {
		return nil, errors.New("store: request start: empty requester")
	}
	out := make([]domain.Issue, 0, len(ids))
	err := s.inTx(ctx, func(q *db.Queries) error {
		for _, id := range ids {
			is, err := q.GetIssue(ctx, id)
			if err != nil {
				return mapErr("get issue", err)
			}
			if st := domain.IssueStatus(is.Status); st == domain.IssueReleased || st == domain.IssueCanceled {
				return fmt.Errorf("store: start %s: %w: it is %s", is.Key, ErrConflict, st)
			}
			row, err := q.RequestIssueStart(ctx, db.RequestIssueStartParams{ID: id, RequestedBy: by})
			if err != nil {
				return mapErr("request issue start", err)
			}
			out = append(out, issueFromRow(row))
		}
		return nil
	})
	return out, err
}

// RetryIssue re-arms a failed issue (failed -> ready, with its state_change event)
// and asks to start it, replacing the manual SQL of M2-M4.
func (s *Store) RetryIssue(ctx context.Context, id uuid.UUID, by string) (domain.Issue, error) {
	if _, err := s.TransitionIssue(ctx, id, domain.IssueFailed, domain.IssueReady); err != nil {
		return domain.Issue{}, err
	}
	issues, err := s.RequestIssueStart(ctx, []uuid.UUID{id}, by)
	if err != nil {
		return domain.Issue{}, err
	}
	return issues[0], nil
}

// MarkIssueScheduled records that the scheduler started the issue's run just now,
// so it holds a slot until the run exists.
func (s *Store) MarkIssueScheduled(ctx context.Context, id uuid.UUID) error {
	return mapErr("mark issue scheduled", s.q.MarkIssueScheduled(ctx, id))
}

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

// CompleteIssue moves a parent whose sub-issues are all finished to merged,
// recording who completed it (D70). It fails with domain.ErrCannotClose when
// domain.CheckComplete refuses.
func (s *Store) CompleteIssue(ctx context.Context, id uuid.UUID, by string) (domain.Issue, error) {
	if by == "" {
		return domain.Issue{}, errors.New("store: complete issue: empty closer")
	}
	var out domain.Issue
	err := s.inTx(ctx, func(q *db.Queries) error {
		// The project lock keeps a sync from adding a sub-issue meanwhile.
		row, err := s.lockIssueProject(ctx, q, id)
		if err != nil {
			return err
		}
		parent := issueFromRow(row)
		rows, err := q.ListIssueChildren(ctx, &id)
		children, err := issuesFromRows("list children", rows, err)
		if err != nil {
			return err
		}
		if err := domain.CheckComplete(parent, children); err != nil {
			return err
		}
		out, err = closeIssue(ctx, q, parent, domain.IssueMerged, by, "")
		return err
	})
	return out, err
}

// CancelIssue cancels an unfinished issue and every unfinished issue under it,
// recording who canceled them and why (D70); finished ones keep their status.
// It refuses, with domain.ErrCannotClose, while any of them has a run in
// progress: a person ends that run first (abort its agent step, or reject its
// gate), so no workflow keeps working on a canceled issue.
func (s *Store) CancelIssue(ctx context.Context, id uuid.UUID, by, reason string) ([]domain.Issue, error) {
	if by == "" {
		return nil, errors.New("store: cancel issue: empty closer")
	}
	reason = strings.TrimSpace(reason)
	var out []domain.Issue
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := s.lockIssueProject(ctx, q, id)
		if err != nil {
			return err
		}
		root := issueFromRow(row)
		if err := domain.CheckCancel(root, reason); err != nil {
			return err
		}
		live, err := q.ListActiveRunsUnder(ctx, id)
		if err != nil {
			return mapErr("list active runs", err)
		}
		if len(live) > 0 {
			return fmt.Errorf("%w: %s has a run in progress (run %s); abort its step or decide its gate first",
				domain.ErrCannotClose, live[0].IssueKey, live[0].ID)
		}
		rows, err := q.ListIssueSubtree(ctx, id)
		subtree, err := issuesFromRows("list subtree", rows, err)
		if err != nil {
			return err
		}
		for _, is := range domain.ToCancel(subtree) {
			closed, err := closeIssue(ctx, q, is, domain.IssueCanceled, by, reason)
			if err != nil {
				return err
			}
			out = append(out, closed)
		}
		return nil
	})
	return out, err
}

// closeIssue moves one issue from its current status, recording the closer, with
// its state change event.
func closeIssue(ctx context.Context, q *db.Queries, is domain.Issue, to domain.IssueStatus, by, reason string) (domain.Issue, error) {
	row, err := q.CloseIssue(ctx, db.CloseIssueParams{ID: is.ID, FromStatus: string(is.Status), ToStatus: string(to), ClosedBy: &by, CloseReason: reason})
	if err != nil {
		if err = mapErr("close issue", err); errors.Is(err, ErrNotFound) {
			return domain.Issue{}, fmt.Errorf("store: close %s: %w: its status changed", is.Key, ErrConflict)
		}
		return domain.Issue{}, err
	}
	if err := appendStateChange(ctx, q, nil, nil, domain.StateChange{Entity: "issue", ID: is.ID, From: string(is.Status), To: string(to)}); err != nil {
		return domain.Issue{}, err
	}
	return issueFromRow(row), nil
}

// ListIssuesToCloseAtSource returns up to limit issues a person completed or
// canceled whose provider issue is still to close (D71), oldest close first.
func (s *Store) ListIssuesToCloseAtSource(ctx context.Context, limit int) ([]domain.Issue, error) {
	rows, err := s.q.ListIssuesToCloseAtSource(ctx, int32(limit))
	return issuesFromRows("list issues to close at the source", rows, err)
}

// MarkSourceClosed records that the issue's provider issue is closed (D71).
func (s *Store) MarkSourceClosed(ctx context.Context, id uuid.UUID) error {
	return mapErr("mark source closed", s.q.MarkSourceClosed(ctx, id))
}

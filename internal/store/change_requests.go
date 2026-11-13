package store

import (
	"context"
	"errors"

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

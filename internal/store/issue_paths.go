package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// SetIssueChangedPaths records the files an issue's latest change touched (D63).
func (s *Store) SetIssueChangedPaths(ctx context.Context, id uuid.UUID, paths []string) error {
	return mapErr("set changed paths", s.q.SetIssueChangedPaths(ctx, db.SetIssueChangedPathsParams{ID: id, ChangedPaths: nonNil(paths)}))
}

// ListPathsIssues returns every unfinished issue with the paths it touches: its
// file hints and the files its latest change touched (D63).
func (s *Store) ListPathsIssues(ctx context.Context) ([]domain.PathsIssue, error) {
	rows, err := s.q.ListPathsIssues(ctx)
	if err != nil {
		return nil, mapErr("list issue paths", err)
	}
	out := make([]domain.PathsIssue, len(rows))
	for i, r := range rows {
		paths := append(append([]string{}, r.FileHints...), r.ChangedPaths...)
		out[i] = domain.PathsIssue{ID: r.ID, Key: r.Key, ProjectID: r.ProjectID, Status: domain.IssueStatus(r.Status), Paths: paths}
	}
	return out, nil
}

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// CreateProject inserts a project with a unique name and issue key prefix (D22).
func (s *Store) CreateProject(ctx context.Context, name, keyPrefix string) (domain.Project, error) {
	if name == "" {
		return domain.Project{}, errors.New("store: create project: empty name")
	}
	if !domain.ValidKeyPrefix(keyPrefix) {
		return domain.Project{}, fmt.Errorf("store: create project: invalid key prefix %q", keyPrefix)
	}
	row, err := s.q.CreateProject(ctx, db.CreateProjectParams{ID: domain.NewID(), Name: name, KeyPrefix: keyPrefix})
	if err != nil {
		return domain.Project{}, mapErr("create project", err)
	}
	return projectFromRow(row), nil
}

// GetProjectByName looks a project up by its unique name.
func (s *Store) GetProjectByName(ctx context.Context, name string) (domain.Project, error) {
	row, err := s.q.GetProjectByName(ctx, name)
	if err != nil {
		return domain.Project{}, mapErr("get project", err)
	}
	return projectFromRow(row), nil
}

// GetProject returns a project by ID.
func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (domain.Project, error) {
	row, err := s.q.GetProject(ctx, id)
	if err != nil {
		return domain.Project{}, mapErr("get project", err)
	}
	return projectFromRow(row), nil
}

// NewIssue is the input for CreateIssue. Status defaults to draft.
type NewIssue struct {
	ProjectID          uuid.UUID
	ParentID           *uuid.UUID // optional; must be in the same project and fit domain.ValidParent
	Type               domain.IssueType
	Title              string
	Spec               string
	AcceptanceCriteria []string
	Status             domain.IssueStatus
	Priority           int
	SourceRef          string
	Labels             []string
	CreatedBy          string
}

// CreateIssue inserts an issue with the project's next number and key. A SourceRef
// already synced into the project returns ErrConflict (and consumes no number).
func (s *Store) CreateIssue(ctx context.Context, in NewIssue) (domain.Issue, error) {
	var out domain.Issue
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := createIssue(ctx, q, in)
		out = issueFromRow(row)
		return err
	})
	if err != nil {
		return domain.Issue{}, err
	}
	return out, nil
}

// createIssue validates and inserts an issue inside the caller's transaction.
func createIssue(ctx context.Context, q *db.Queries, in NewIssue) (db.Issue, error) {
	if in.Status == "" {
		in.Status = domain.IssueDraft
	}
	switch {
	case !in.Type.Valid():
		return db.Issue{}, fmt.Errorf("store: create issue: unknown type %q", in.Type)
	case !in.Status.Valid():
		return db.Issue{}, fmt.Errorf("store: create issue: unknown status %q", in.Status)
	case in.Title == "":
		return db.Issue{}, errors.New("store: create issue: empty title")
	case in.CreatedBy == "":
		return db.Issue{}, errors.New("store: create issue: empty created_by")
	}
	if in.ParentID != nil {
		if err := checkParent(ctx, q, in.ProjectID, in.Type, *in.ParentID); err != nil {
			return db.Issue{}, fmt.Errorf("store: create issue: %w", err)
		}
	}
	alloc, err := q.AllocateIssueNumber(ctx, in.ProjectID)
	if err != nil {
		return db.Issue{}, mapErr("allocate issue number", err)
	}
	row, err := q.CreateIssue(ctx, db.CreateIssueParams{
		ID:                 domain.NewID(),
		ProjectID:          in.ProjectID,
		Number:             alloc.Number,
		Key:                domain.IssueKey(alloc.KeyPrefix, int(alloc.Number)),
		ParentID:           in.ParentID,
		Type:               string(in.Type),
		Title:              in.Title,
		Spec:               in.Spec,
		AcceptanceCriteria: nonNil(in.AcceptanceCriteria),
		Status:             string(in.Status),
		Priority:           int32(in.Priority),
		SourceRef:          nonEmpty(in.SourceRef),
		Labels:             nonNil(in.Labels),
		CreatedBy:          in.CreatedBy,
	})
	if err != nil {
		return db.Issue{}, mapErr("create issue", err)
	}
	return row, nil
}

// GetIssueByKey returns an issue by its key, e.g. "frk-42".
func (s *Store) GetIssueByKey(ctx context.Context, key string) (domain.Issue, error) {
	row, err := s.q.GetIssueByKey(ctx, key)
	if err != nil {
		return domain.Issue{}, mapErr("get issue by key", err)
	}
	return issueFromRow(row), nil
}

// GetIssue returns an issue by ID.
func (s *Store) GetIssue(ctx context.Context, id uuid.UUID) (domain.Issue, error) {
	row, err := s.q.GetIssue(ctx, id)
	if err != nil {
		return domain.Issue{}, mapErr("get issue", err)
	}
	return issueFromRow(row), nil
}

// GetIssueBySourceRef returns the issue synced from an external reference.
func (s *Store) GetIssueBySourceRef(ctx context.Context, projectID uuid.UUID, sourceRef string) (domain.Issue, error) {
	row, err := s.q.GetIssueBySourceRef(ctx, db.GetIssueBySourceRefParams{ProjectID: projectID, SourceRef: &sourceRef})
	if err != nil {
		return domain.Issue{}, mapErr("get issue by source ref", err)
	}
	return issueFromRow(row), nil
}

// ListIssuesByStatus returns a project's issues in one status, highest priority first.
func (s *Store) ListIssuesByStatus(ctx context.Context, projectID uuid.UUID, status domain.IssueStatus) ([]domain.Issue, error) {
	rows, err := s.q.ListIssuesByStatus(ctx, db.ListIssuesByStatusParams{ProjectID: projectID, Status: string(status)})
	if err != nil {
		return nil, mapErr("list issues", err)
	}
	out := make([]domain.Issue, 0, len(rows))
	for _, r := range rows {
		out = append(out, issueFromRow(r))
	}
	return out, nil
}

// TransitionIssue moves an issue from one status to another and records a state_change event.
// It fails with domain.ErrInvalidTransition if the state machine forbids the move, and with
// ErrConflict if the issue is no longer in from. Repeating a transition that already happened
// returns the issue unchanged and no error, so retried Activities are idempotent.
func (s *Store) TransitionIssue(ctx context.Context, id uuid.UUID, from, to domain.IssueStatus) (domain.Issue, error) {
	if !domain.CanTransitionIssue(from, to) {
		return domain.Issue{}, fmt.Errorf("store: issue %s: %w: %s -> %s", id, domain.ErrInvalidTransition, from, to)
	}
	var out domain.Issue
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.TransitionIssue(ctx, db.TransitionIssueParams{ID: id, FromStatus: string(from), ToStatus: string(to)})
		if err != nil {
			return mapErr("transition issue", err)
		}
		out = issueFromRow(row)
		return appendStateChange(ctx, q, nil, nil, domain.StateChange{Entity: "issue", ID: id, From: string(from), To: string(to)})
	})
	if errors.Is(err, ErrNotFound) {
		return s.resolveMissedIssueTransition(ctx, id, from, to)
	}
	return out, err
}

func (s *Store) resolveMissedIssueTransition(ctx context.Context, id uuid.UUID, from, to domain.IssueStatus) (domain.Issue, error) {
	current, err := s.GetIssue(ctx, id)
	if err != nil {
		return domain.Issue{}, err
	}
	if current.Status == to {
		return current, nil
	}
	return domain.Issue{}, fmt.Errorf("store: issue %s: %w: expected %s, found %s", id, ErrConflict, from, current.Status)
}

func projectFromRow(r db.Project) domain.Project {
	return domain.Project{ID: r.ID, Name: r.Name, KeyPrefix: r.KeyPrefix, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func issueFromRow(r db.Issue) domain.Issue {
	return domain.Issue{
		ID:                 r.ID,
		ProjectID:          r.ProjectID,
		Number:             int(r.Number),
		Key:                r.Key,
		ParentID:           r.ParentID,
		Type:               domain.IssueType(r.Type),
		Title:              r.Title,
		Spec:               r.Spec,
		AcceptanceCriteria: r.AcceptanceCriteria,
		Status:             domain.IssueStatus(r.Status),
		Priority:           int(r.Priority),
		SourceRef:          deref(r.SourceRef),
		Labels:             r.Labels,
		CreatedBy:          r.CreatedBy,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

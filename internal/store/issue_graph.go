package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// SetIssueParent moves an issue under parentID, or makes it a root when parentID is nil
// (D57). The parent must be in the same project and fit domain.ValidParent, and the move
// must not make any issue wait for itself through inherited dependencies.
func (s *Store) SetIssueParent(ctx context.Context, id uuid.UUID, parentID *uuid.UUID) (domain.Issue, error) {
	var out domain.Issue
	err := s.inTx(ctx, func(q *db.Queries) error {
		is, err := s.lockIssueProject(ctx, q, id)
		if err != nil {
			return err
		}
		if parentID != nil {
			if err := checkParent(ctx, q, is.ProjectID, domain.IssueType(is.Type), *parentID); err != nil {
				return fmt.Errorf("store: set parent of %s: %w", is.Key, err)
			}
		}
		nodes, deps, err := projectGraph(ctx, q, is.ProjectID)
		if err != nil {
			return err
		}
		for i := range nodes {
			if nodes[i].ID == id {
				nodes[i].ParentID = parentID
			}
		}
		if err := domain.CheckIssueGraph(nodes, deps); err != nil {
			return fmt.Errorf("store: set parent of %s: %w", is.Key, err)
		}
		row, err := q.SetIssueParent(ctx, db.SetIssueParentParams{ID: id, ParentID: parentID})
		if err != nil {
			return mapErr("set issue parent", err)
		}
		out = issueFromRow(row)
		return nil
	})
	return out, err
}

// AddIssueDependency records that issueID waits for dependsOnID (D57). Both issues must be
// in the same project, and the edge must not close a cycle (domain.CheckIssueGraph).
// Adding an edge that already exists is a no-op.
func (s *Store) AddIssueDependency(ctx context.Context, issueID, dependsOnID uuid.UUID) error {
	if issueID == dependsOnID {
		return fmt.Errorf("store: add dependency: %w: an issue cannot depend on itself", domain.ErrInvalidDependency)
	}
	return s.inTx(ctx, func(q *db.Queries) error {
		is, err := s.lockIssueProject(ctx, q, issueID)
		if err != nil {
			return err
		}
		dep, err := q.GetIssue(ctx, dependsOnID)
		if err != nil {
			return mapErr("get dependency", err)
		}
		if dep.ProjectID != is.ProjectID {
			return fmt.Errorf("store: add dependency %s -> %s: %w: different projects", is.Key, dep.Key, domain.ErrInvalidDependency)
		}
		nodes, deps, err := projectGraph(ctx, q, is.ProjectID)
		if err != nil {
			return err
		}
		deps = append(deps, domain.Dependency{IssueID: issueID, DependsOnID: dependsOnID})
		if err := domain.CheckIssueGraph(nodes, deps); err != nil {
			return fmt.Errorf("store: add dependency %s -> %s: %w", is.Key, dep.Key, err)
		}
		return mapErr("add dependency", q.AddIssueDependency(ctx, db.AddIssueDependencyParams{IssueID: issueID, DependsOnID: dependsOnID}))
	})
}

// RemoveIssueDependency deletes the edge issueID -> dependsOnID; removing an edge that
// does not exist is a no-op.
func (s *Store) RemoveIssueDependency(ctx context.Context, issueID, dependsOnID uuid.UUID) error {
	return mapErr("remove dependency", s.q.RemoveIssueDependency(ctx, db.RemoveIssueDependencyParams{IssueID: issueID, DependsOnID: dependsOnID}))
}

// ListIssueDependencies returns the issues that id waits for, by number.
func (s *Store) ListIssueDependencies(ctx context.Context, id uuid.UUID) ([]domain.Issue, error) {
	rows, err := s.q.ListIssueDependencies(ctx, id)
	return issuesFromRows("list dependencies", rows, err)
}

// ListIssueDependents returns the issues that wait for id, by number.
func (s *Store) ListIssueDependents(ctx context.Context, id uuid.UUID) ([]domain.Issue, error) {
	rows, err := s.q.ListIssueDependents(ctx, id)
	return issuesFromRows("list dependents", rows, err)
}

// ListIssueChildren returns the issues directly under id, highest priority first.
func (s *Store) ListIssueChildren(ctx context.Context, id uuid.UUID) ([]domain.Issue, error) {
	rows, err := s.q.ListIssueChildren(ctx, &id)
	return issuesFromRows("list children", rows, err)
}

// lockIssueProject loads an issue and locks its project row, so graph changes in one
// project are checked and applied one at a time.
func (s *Store) lockIssueProject(ctx context.Context, q *db.Queries, id uuid.UUID) (db.Issue, error) {
	is, err := q.GetIssue(ctx, id)
	if err != nil {
		return db.Issue{}, mapErr("get issue", err)
	}
	if err := q.LockProject(ctx, is.ProjectID); err != nil {
		return db.Issue{}, mapErr("lock project", err)
	}
	return is, nil
}

// checkParent validates parentID as the parent of a new or moved issue of type child.
func checkParent(ctx context.Context, q *db.Queries, projectID uuid.UUID, child domain.IssueType, parentID uuid.UUID) error {
	parent, err := q.GetIssue(ctx, parentID)
	if err = mapErr("get parent", err); errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: parent %s not found", domain.ErrInvalidParent, parentID)
	} else if err != nil {
		return err
	}
	if parent.ProjectID != projectID {
		return fmt.Errorf("%w: parent %s is in another project", domain.ErrInvalidParent, parent.Key)
	}
	if !domain.ValidParent(child, domain.IssueType(parent.Type)) {
		return fmt.Errorf("%w: a %s cannot sit under a %s (%s)", domain.ErrInvalidParent, child, parent.Type, parent.Key)
	}
	return nil
}

func projectGraph(ctx context.Context, q *db.Queries, projectID uuid.UUID) ([]domain.IssueNode, []domain.Dependency, error) {
	nodeRows, err := q.ListIssueNodes(ctx, projectID)
	if err != nil {
		return nil, nil, mapErr("list issue nodes", err)
	}
	depRows, err := q.ListProjectDependencies(ctx, projectID)
	if err != nil {
		return nil, nil, mapErr("list dependencies", err)
	}
	nodes := make([]domain.IssueNode, len(nodeRows))
	for i, r := range nodeRows {
		nodes[i] = domain.IssueNode{ID: r.ID, Key: r.Key, ParentID: r.ParentID}
	}
	deps := make([]domain.Dependency, len(depRows))
	for i, r := range depRows {
		deps[i] = domain.Dependency{IssueID: r.IssueID, DependsOnID: r.DependsOnID}
	}
	return nodes, deps, nil
}

func issuesFromRows(op string, rows []db.Issue, err error) ([]domain.Issue, error) {
	if err != nil {
		return nil, mapErr(op, err)
	}
	out := make([]domain.Issue, len(rows))
	for i, r := range rows {
		out[i] = issueFromRow(r)
	}
	return out, nil
}

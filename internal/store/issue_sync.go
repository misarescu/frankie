package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// SourceIssue is one issue of the tree an issue source reported, as intake hands
// it to SyncIssues (D60).
type SourceIssue struct {
	Ref       string // source_ref, e.g. the GitHub issue URL
	Type      domain.IssueType
	Title     string
	Spec      string
	Labels    []string
	CreatedBy string
	// ParentRef is the source's parent; empty, or a ref outside the tree, means a root.
	ParentRef string
	// BlockedBy are the refs this issue waits for; refs outside the tree are ignored.
	BlockedBy []string
	// Status is the status of a new issue (draft or ready); existing issues keep theirs.
	Status domain.IssueStatus
}

// SyncResult reports what SyncIssues did.
type SyncResult struct {
	Issues   map[string]domain.Issue // every synced issue by ref, as stored afterwards
	Created  []string                // keys of new issues
	Warnings []string                // links the sync could not apply
}

// SyncIssues applies a source's issue tree to a project in one transaction under
// the project lock (D60). New refs become issues. An issue that never ran (a
// draft, or ready without runs, D62) takes the source's title, spec, labels and
// type; a started one keeps the factory's copy. The
// source is authoritative for the parent and the dependencies of the issues it
// reports: each is set to what the tree says. A link that breaks the hierarchy
// rules or would make an issue wait for itself is skipped with a warning, so one
// bad link on the source does not stop the rest.
func (s *Store) SyncIssues(ctx context.Context, projectID uuid.UUID, tree []SourceIssue) (SyncResult, error) {
	res := SyncResult{Issues: make(map[string]domain.Issue, len(tree))}
	err := s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockProject(ctx, projectID); err != nil {
			return mapErr("lock project", err)
		}
		rows := make(map[string]db.Issue, len(tree))
		inTree := make(map[uuid.UUID]bool, len(tree))
		for _, in := range tree {
			row, created, warn, err := upsertSourceIssue(ctx, q, projectID, in)
			if err != nil {
				return err
			}
			if created {
				res.Created = append(res.Created, row.Key)
			}
			if warn != "" {
				res.Warnings = append(res.Warnings, warn)
			}
			rows[in.Ref] = row
			inTree[row.ID] = true
		}

		nodes, deps, err := projectGraph(ctx, q, projectID)
		if err != nil {
			return err
		}
		g := &syncGraph{nodes: nodes, deps: deps}

		for _, in := range tree {
			row := rows[in.Ref]
			var want *uuid.UUID
			if p, ok := rows[in.ParentRef]; ok && in.ParentRef != "" && p.ID != row.ID {
				if domain.ValidParent(domain.IssueType(row.Type), domain.IssueType(p.Type)) {
					want = &p.ID
				} else {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: a %s cannot sit under the %s %s; left without a parent", row.Key, row.Type, p.Type, p.Key))
				}
			}
			if equalID(row.ParentID, want) {
				continue
			}
			if err := g.setParent(row.ID, want); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: parent not changed: %v", row.Key, err))
				continue
			}
			updated, err := q.SetIssueParent(ctx, db.SetIssueParentParams{ID: row.ID, ParentID: want})
			if err != nil {
				return mapErr("set issue parent", err)
			}
			rows[in.Ref] = updated
		}

		// Remove stale edges first, so an edge that moved does not look like a cycle.
		want := make(map[uuid.UUID][]uuid.UUID, len(tree))
		for _, in := range tree {
			id := rows[in.Ref].ID
			for _, ref := range in.BlockedBy {
				if b, ok := rows[ref]; ok && b.ID != id && !slices.Contains(want[id], b.ID) {
					want[id] = append(want[id], b.ID)
				}
			}
		}
		for _, d := range slices.Clone(g.deps) {
			if inTree[d.IssueID] && !slices.Contains(want[d.IssueID], d.DependsOnID) {
				if err := q.RemoveIssueDependency(ctx, db.RemoveIssueDependencyParams{IssueID: d.IssueID, DependsOnID: d.DependsOnID}); err != nil {
					return mapErr("remove dependency", err)
				}
				g.removeDep(d)
			}
		}
		for _, in := range tree {
			id := rows[in.Ref].ID
			for _, b := range want[id] {
				d := domain.Dependency{IssueID: id, DependsOnID: b}
				if slices.Contains(g.deps, d) {
					continue
				}
				if err := g.addDep(d); err != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: dependency skipped: %v", rows[in.Ref].Key, err))
					continue
				}
				if err := q.AddIssueDependency(ctx, db.AddIssueDependencyParams{IssueID: id, DependsOnID: b}); err != nil {
					return mapErr("add dependency", err)
				}
			}
		}
		for ref, row := range rows {
			res.Issues[ref] = issueFromRow(row)
		}
		return nil
	})
	if err != nil {
		return SyncResult{}, err
	}
	return res, nil
}

// upsertSourceIssue creates the issue for a ref, or refreshes an issue that never
// ran from the source. A type change that its existing children would not fit is
// not applied.
func upsertSourceIssue(ctx context.Context, q *db.Queries, projectID uuid.UUID, in SourceIssue) (row db.Issue, created bool, warn string, err error) {
	ref := in.Ref
	row, err = q.GetIssueBySourceRef(ctx, db.GetIssueBySourceRefParams{ProjectID: projectID, SourceRef: &ref})
	if err = mapErr("get issue by source ref", err); errors.Is(err, ErrNotFound) {
		row, err = createIssue(ctx, q, NewIssue{ProjectID: projectID, Type: in.Type, Title: in.Title, Spec: in.Spec,
			Status: in.Status, SourceRef: in.Ref, Labels: in.Labels, CreatedBy: in.CreatedBy})
		return row, err == nil, "", err
	}
	if err != nil || (row.Status != string(domain.IssueDraft) && row.Status != string(domain.IssueReady)) {
		return row, false, "", err
	}
	typ := in.Type
	if typ != domain.IssueType(row.Type) {
		children, err := q.ListIssueChildren(ctx, &row.ID)
		if err != nil {
			return row, false, "", mapErr("list children", err)
		}
		for _, c := range children {
			if !domain.ValidParent(domain.IssueType(c.Type), typ) {
				warn = fmt.Sprintf("%s: stays a %s: its child %s (a %s) cannot sit under a %s", row.Key, row.Type, c.Key, c.Type, typ)
				typ = domain.IssueType(row.Type)
				break
			}
		}
	}
	if row.Title == in.Title && row.Spec == in.Spec && slices.Equal(row.Labels, nonNil(in.Labels)) && row.Type == string(typ) {
		return row, false, warn, nil
	}
	updated, err := q.UpdateUnstartedIssueFromSource(ctx, db.UpdateUnstartedIssueFromSourceParams{ID: row.ID, Title: in.Title, Spec: in.Spec,
		Labels: nonNil(in.Labels), Type: string(typ)})
	if err = mapErr("update issue from source", err); errors.Is(err, ErrNotFound) {
		return row, false, warn, nil // a ready issue that ran before keeps its copy
	}
	return updated, false, warn, err
}

// syncGraph is the project's graph in memory, so each change is checked before
// it is written.
type syncGraph struct {
	nodes []domain.IssueNode
	deps  []domain.Dependency
}

func (g *syncGraph) setParent(id uuid.UUID, parent *uuid.UUID) error {
	for i := range g.nodes {
		if g.nodes[i].ID != id {
			continue
		}
		old := g.nodes[i].ParentID
		g.nodes[i].ParentID = parent
		if err := domain.CheckIssueGraph(g.nodes, g.deps); err != nil {
			g.nodes[i].ParentID = old
			return err
		}
	}
	return nil
}

func (g *syncGraph) addDep(d domain.Dependency) error {
	g.deps = append(g.deps, d)
	if err := domain.CheckIssueGraph(g.nodes, g.deps); err != nil {
		g.deps = g.deps[:len(g.deps)-1]
		return err
	}
	return nil
}

func (g *syncGraph) removeDep(d domain.Dependency) {
	g.deps = slices.DeleteFunc(g.deps, func(x domain.Dependency) bool { return x == d })
}

func equalID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

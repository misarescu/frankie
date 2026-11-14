package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

func mustTyped(t *testing.T, s *Store, projectID uuid.UUID, typ domain.IssueType, parent *domain.Issue) domain.Issue {
	t.Helper()
	in := NewIssue{ProjectID: projectID, Type: typ, Title: string(typ), CreatedBy: "test"}
	if parent != nil {
		in.ParentID = &parent.ID
	}
	is, err := s.CreateIssue(t.Context(), in)
	if err != nil {
		t.Fatalf("CreateIssue(%s): %v", typ, err)
	}
	return is
}

// D57: epic > story > task, checked when an issue is created under a parent.
func TestCreateIssueWithParent(t *testing.T) {
	s := newTestStore(t)
	p, other := mustProject(t, s), mustProject(t, s)
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	story := mustTyped(t, s, p.ID, domain.IssueStory, &epic)
	task := mustTyped(t, s, p.ID, domain.IssueTask, &story)

	got, err := s.GetIssue(t.Context(), task.ID)
	if err != nil || got.ParentID == nil || *got.ParentID != story.ID {
		t.Fatalf("task parent = %v, %v; want %s", got.ParentID, err, story.Key)
	}
	children, err := s.ListIssueChildren(t.Context(), epic.ID)
	if err != nil || len(children) != 1 || children[0].ID != story.ID {
		t.Fatalf("epic children = %v, %v; want [%s]", children, err, story.Key)
	}

	foreign := mustTyped(t, s, other.ID, domain.IssueEpic, nil)
	missing := uuid.New()
	bad := []struct {
		name   string
		typ    domain.IssueType
		parent uuid.UUID
	}{
		{"epic under epic", domain.IssueEpic, epic.ID},
		{"story under story", domain.IssueStory, story.ID},
		{"task under task", domain.IssueTask, task.ID},
		{"parent in another project", domain.IssueTask, foreign.ID},
		{"missing parent", domain.IssueTask, missing},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateIssue(t.Context(), NewIssue{ProjectID: p.ID, Type: tt.typ, Title: "x", CreatedBy: "test", ParentID: &tt.parent})
			if !errors.Is(err, domain.ErrInvalidParent) {
				t.Fatalf("CreateIssue = %v, want ErrInvalidParent", err)
			}
		})
	}
}

func TestSetIssueParent(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	story := mustTyped(t, s, p.ID, domain.IssueStory, nil)
	task := mustTyped(t, s, p.ID, domain.IssueTask, nil)

	if got, err := s.SetIssueParent(ctx, story.ID, &epic.ID); err != nil || got.ParentID == nil || *got.ParentID != epic.ID {
		t.Fatalf("story under epic = %v, %v", got.ParentID, err)
	}
	if _, err := s.SetIssueParent(ctx, task.ID, &story.ID); err != nil {
		t.Fatalf("task under story: %v", err)
	}
	if _, err := s.SetIssueParent(ctx, epic.ID, &story.ID); !errors.Is(err, domain.ErrInvalidParent) {
		t.Fatalf("epic under story = %v, want ErrInvalidParent", err)
	}
	if got, err := s.SetIssueParent(ctx, task.ID, nil); err != nil || got.ParentID != nil {
		t.Fatalf("clear parent = %v, %v", got.ParentID, err)
	}

	t.Run("a move that makes an issue wait for itself", func(t *testing.T) {
		// The epic waits for the task; moving the task under the epic would make it
		// inherit that dependency on itself.
		if err := s.AddIssueDependency(ctx, epic.ID, task.ID); err != nil {
			t.Fatalf("AddIssueDependency: %v", err)
		}
		if _, err := s.SetIssueParent(ctx, task.ID, &story.ID); !errors.Is(err, domain.ErrDependencyCycle) {
			t.Fatalf("SetIssueParent = %v, want ErrDependencyCycle", err)
		}
		got, err := s.GetIssue(ctx, task.ID)
		if err != nil || got.ParentID != nil {
			t.Fatalf("rejected move changed the parent: %v, %v", got.ParentID, err)
		}
	})
}

func TestIssueDependencies(t *testing.T) {
	s := newTestStore(t)
	p, other := mustProject(t, s), mustProject(t, s)
	ctx := t.Context()
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	a := mustTyped(t, s, p.ID, domain.IssueTask, &epic)
	b := mustTyped(t, s, p.ID, domain.IssueTask, &epic)
	c := mustTyped(t, s, p.ID, domain.IssueTask, &epic)

	for _, e := range [][2]domain.Issue{{b, a}, {c, a}, {c, b}, {c, b}} { // the repeat is a no-op
		if err := s.AddIssueDependency(ctx, e[0].ID, e[1].ID); err != nil {
			t.Fatalf("AddIssueDependency(%s -> %s): %v", e[0].Key, e[1].Key, err)
		}
	}
	deps, err := s.ListIssueDependencies(ctx, c.ID)
	if err != nil || keys(deps) != a.Key+","+b.Key {
		t.Fatalf("dependencies of c = %s, %v; want %s,%s", keys(deps), err, a.Key, b.Key)
	}
	dependents, err := s.ListIssueDependents(ctx, a.ID)
	if err != nil || keys(dependents) != b.Key+","+c.Key {
		t.Fatalf("dependents of a = %s, %v; want %s,%s", keys(dependents), err, b.Key, c.Key)
	}

	foreign := mustTyped(t, s, other.ID, domain.IssueTask, nil)
	rejected := []struct {
		name     string
		from, to domain.Issue
		want     error
	}{
		{"self", a, a, domain.ErrInvalidDependency},
		{"another project", a, foreign, domain.ErrInvalidDependency},
		{"closes a cycle", a, c, domain.ErrDependencyCycle},
		{"on its own epic", a, epic, domain.ErrDependencyCycle},
		{"epic on its own task", epic, a, domain.ErrDependencyCycle},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.AddIssueDependency(ctx, tt.from.ID, tt.to.ID); !errors.Is(err, tt.want) {
				t.Fatalf("AddIssueDependency = %v, want %v", err, tt.want)
			}
		})
	}

	if err := s.RemoveIssueDependency(ctx, c.ID, b.ID); err != nil {
		t.Fatalf("RemoveIssueDependency: %v", err)
	}
	if err := s.RemoveIssueDependency(ctx, c.ID, b.ID); err != nil {
		t.Fatalf("RemoveIssueDependency again: %v", err)
	}
	deps, err = s.ListIssueDependencies(ctx, c.ID)
	if err != nil || keys(deps) != a.Key {
		t.Fatalf("dependencies of c after removal = %s, %v; want %s", keys(deps), err, a.Key)
	}
}

// Two edges that each are fine but together close a cycle: the project lock makes the
// second one see the first, so at most one is stored.
func TestIssueDependenciesConcurrentCycle(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	a := mustTyped(t, s, p.ID, domain.IssueTask, nil)
	b := mustTyped(t, s, p.ID, domain.IssueTask, nil)

	for range 20 {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, e := range [][2]domain.Issue{{a, b}, {b, a}} {
			wg.Go(func() { errs[i] = s.AddIssueDependency(t.Context(), e[0].ID, e[1].ID) })
		}
		wg.Wait()
		ab, _ := s.ListIssueDependencies(t.Context(), a.ID)
		ba, _ := s.ListIssueDependencies(t.Context(), b.ID)
		if len(ab)+len(ba) != 1 {
			t.Fatalf("stored %d edges (errors %v), want exactly 1", len(ab)+len(ba), errs)
		}
		_ = s.RemoveIssueDependency(t.Context(), a.ID, b.ID)
		_ = s.RemoveIssueDependency(t.Context(), b.ID, a.ID)
	}
}

func keys(issues []domain.Issue) string {
	out := ""
	for i, is := range issues {
		if i > 0 {
			out += ","
		}
		out += is.Key
	}
	return out
}

package store

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// D63: an issue's paths are its file hints plus the files its latest change touched;
// finished issues are not compared.
func TestIssuePaths(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	hinted, err := s.CreateIssue(ctx, NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", CreatedBy: "t",
		Status: domain.IssueRunning, FileHints: []string{"internal/api/issues.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetIssueChangedPaths(ctx, hinted.ID, []string{"internal/api/schedule.go", "web/src/App.tsx"}); err != nil {
		t.Fatal(err)
	}
	merged := mustIssue(t, s, p.ID, domain.IssueReady)
	if _, err := s.TransitionIssue(ctx, merged.ID, domain.IssueReady, domain.IssueCanceled); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetIssue(ctx, hinted.ID)
	if err != nil || !slices.Equal(got.FileHints, []string{"internal/api/issues.go"}) || len(got.ChangedPaths) != 2 {
		t.Fatalf("issue = %+v, %v", got, err)
	}
	all, err := s.ListPathsIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[uuid.UUID]domain.PathsIssue{}
	for _, x := range all {
		by[x.ID] = x
	}
	if h := by[hinted.ID]; !slices.Equal(h.Paths, []string{"internal/api/issues.go", "internal/api/schedule.go", "web/src/App.tsx"}) ||
		h.Status != domain.IssueRunning || h.ProjectID != p.ID {
		t.Errorf("paths issue = %+v", h)
	}
	if _, ok := by[merged.ID]; ok {
		t.Error("a canceled issue is listed")
	}
}

// File hints follow the source's text like the rest of it.
func TestSyncIssuesFileHints(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	in := src("1", domain.IssueTask, "")
	in.Status, in.FileHints = domain.IssueReady, []string{"a/b.go"}
	res := mustSync(t, s, p, in)
	if got := res.Issues[in.Ref].FileHints; !slices.Equal(got, []string{"a/b.go"}) {
		t.Fatalf("hints = %q", got)
	}
	in.FileHints = []string{"c/d.go"}
	if got := mustSync(t, s, p, in).Issues[in.Ref].FileHints; !slices.Equal(got, []string{"c/d.go"}) {
		t.Fatalf("hints after the source changed = %q", got)
	}
}

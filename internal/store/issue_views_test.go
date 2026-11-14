package store

import (
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

func TestListProjects(t *testing.T) {
	s := newTestStore(t)
	a, b := mustProject(t, s), mustProject(t, s)
	got, err := s.ListProjects(t.Context())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	seen := map[uuid.UUID]bool{}
	for i, p := range got {
		seen[p.ID] = true
		if i > 0 && got[i-1].Name > p.Name {
			t.Errorf("not ordered by name: %s before %s", got[i-1].Name, p.Name)
		}
	}
	if !seen[a.ID] || !seen[b.ID] {
		t.Fatalf("ListProjects = %v, want both projects", got)
	}
}

// The board lists a project's issues with what they spent and their latest run (M5).
func TestListProjectIssues(t *testing.T) {
	s := newTestStore(t)
	p, other := mustProject(t, s), mustProject(t, s)
	ctx := t.Context()
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	ran := mustTyped(t, s, p.ID, domain.IssueTask, &epic)
	idle := mustTyped(t, s, p.ID, domain.IssueTask, &epic)
	mustTyped(t, s, other.ID, domain.IssueTask, nil)

	// Two runs; the second is the latest. Each spends on one agent step.
	first := mustRun(t, s, ran.ID)
	spend(t, s, first, 0.25)
	if _, err := s.TransitionRun(ctx, first.ID, domain.ExecPending, domain.ExecFailed); err != nil {
		t.Fatal(err)
	}
	second := mustRun(t, s, ran.ID)
	spend(t, s, second, 0.5)

	got, err := s.ListProjectIssues(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListProjectIssues: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListProjectIssues returned %d issues, want 3 (other projects excluded)", len(got))
	}
	by := map[uuid.UUID]IssueSummary{}
	for _, is := range got {
		by[is.ID] = is
	}
	if r := by[ran.ID]; r.CostUSD != 0.75 || r.LatestRunID != second.ID || r.LatestRunStatus != domain.ExecPending {
		t.Errorf("ran = cost %v, latest %v %q; want 0.75, %v pending", r.CostUSD, r.LatestRunID, r.LatestRunStatus, second.ID)
	}
	if r := by[idle.ID]; r.CostUSD != 0 || r.LatestRunID != uuid.Nil || r.LatestRunStatus != "" {
		t.Errorf("idle = cost %v, latest %v %q; want no runs", r.CostUSD, r.LatestRunID, r.LatestRunStatus)
	}
	if r := by[ran.ID]; r.ParentID == nil || *r.ParentID != epic.ID {
		t.Errorf("ran parent = %v, want %s", r.ParentID, epic.Key)
	}
}

func TestProjectDependencies(t *testing.T) {
	s := newTestStore(t)
	p, other := mustProject(t, s), mustProject(t, s)
	a := mustTyped(t, s, p.ID, domain.IssueTask, nil)
	b := mustTyped(t, s, p.ID, domain.IssueTask, nil)
	x := mustTyped(t, s, other.ID, domain.IssueTask, nil)
	y := mustTyped(t, s, other.ID, domain.IssueTask, nil)
	for _, e := range [][2]domain.Issue{{b, a}, {y, x}} {
		if err := s.AddIssueDependency(t.Context(), e[0].ID, e[1].ID); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ProjectDependencies(t.Context(), p.ID)
	if err != nil || len(got) != 1 || got[0] != (domain.Dependency{IssueID: b.ID, DependsOnID: a.ID}) {
		t.Fatalf("ProjectDependencies = %v, %v; want only %s -> %s", got, err, b.Key, a.Key)
	}
}

func TestListIssueRunsAndUsage(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	is := mustTyped(t, s, p.ID, domain.IssueTask, nil)
	ctx := t.Context()

	if runs, err := s.ListIssueRuns(ctx, is.ID); err != nil || len(runs) != 0 {
		t.Fatalf("ListIssueRuns before any run = %v, %v", runs, err)
	}
	if u, err := s.IssueUsage(ctx, is.ID); err != nil || u != (domain.Usage{}) {
		t.Fatalf("IssueUsage before any run = %+v, %v", u, err)
	}

	first := mustRun(t, s, is.ID)
	spend(t, s, first, 0.25)
	if _, err := s.TransitionRun(ctx, first.ID, domain.ExecPending, domain.ExecFailed); err != nil {
		t.Fatal(err)
	}
	second := mustRun(t, s, is.ID)
	spend(t, s, second, 1)

	runs, err := s.ListIssueRuns(ctx, is.ID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("ListIssueRuns = %v, %v; want 2 runs", runs, err)
	}
	if runs[0].ID != second.ID || runs[0].CostUSD != 1 || runs[1].ID != first.ID || runs[1].Status != domain.ExecFailed || runs[1].CostUSD != 0.25 {
		t.Errorf("ListIssueRuns = %+v; want the second run first, with each run's cost", runs)
	}
	u, err := s.IssueUsage(ctx, is.ID)
	if err != nil || u != (domain.Usage{CostUSD: 1.25, InputTokens: 200, OutputTokens: 20}) {
		t.Fatalf("IssueUsage = %+v, %v; want 1.25 USD, 200 in, 20 out", u, err)
	}
}

// spend records one agent step on run that used cost USD, 100 input and 10 output tokens.
func spend(t *testing.T, s *Store, run domain.Run, cost float64) {
	t.Helper()
	sr, err := s.CreateStepRun(t.Context(), NewStepRun{RunID: run.ID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddStepRunUsage(t.Context(), sr.ID, domain.Usage{CostUSD: cost, InputTokens: 100, OutputTokens: 10}); err != nil {
		t.Fatal(err)
	}
}

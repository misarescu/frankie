package store

import (
	"slices"
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
	if runs[0].ID != second.ID || runs[0].CostUSD != 1 || runs[1].ID != first.ID || runs[1].Status != domain.ExecFailed || runs[1].CostUSD != 0.25 ||
		runs[0].WorkflowVersion != second.WorkflowVersion || runs[0].WorkflowVersion == "" {
		t.Errorf("ListIssueRuns = %+v; want the second run first, with each run's workflow and cost", runs)
	}
	u, err := s.IssueUsage(ctx, is.ID)
	if err != nil || u != (domain.Usage{CostUSD: 1.25, InputTokens: 200, OutputTokens: 20}) {
		t.Fatalf("IssueUsage = %+v, %v; want 1.25 USD, 200 in, 20 out", u, err)
	}
}

// D142: an issue's total adds what every issue under it spent, canceled ones
// included; a leaf's total is its own cost, and other trees stay out.
func TestIssueTotalCost(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	story := mustTyped(t, s, p.ID, domain.IssueStory, &epic)
	task := mustTyped(t, s, p.ID, domain.IssueTask, &story)
	canceled, err := s.CreateIssue(ctx, NewIssue{ProjectID: p.ID, ParentID: &story.ID, Type: domain.IssueTask, Title: "dropped",
		Status: domain.IssueCanceled, CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	bug := mustTyped(t, s, p.ID, domain.IssueBug, &epic)
	idle := mustTyped(t, s, p.ID, domain.IssueTask, &story)
	other := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	for _, c := range []struct {
		issue domain.Issue
		cost  float64
	}{{epic, 0.5}, {story, 0.25}, {task, 1}, {canceled, 2}, {bug, 0.125}, {other, 4}} {
		spend(t, s, mustRun(t, s, c.issue.ID), c.cost)
	}

	want := map[uuid.UUID]float64{
		epic.ID: 3.875, story.ID: 3.25, task.ID: 1, canceled.ID: 2, bug.ID: 0.125, idle.ID: 0, other.ID: 4,
	}
	list, err := s.ListProjectIssues(ctx, p.ID)
	if err != nil || len(list) != len(want) {
		t.Fatalf("ListProjectIssues = %d issues, %v; want %d", len(list), err, len(want))
	}
	for _, is := range list {
		if is.TotalCostUSD != want[is.ID] {
			t.Errorf("%s (%s): listed total %v, want %v", is.Key, is.Type, is.TotalCostUSD, want[is.ID])
		}
	}
	for _, is := range []domain.Issue{epic, story, task, canceled, bug, idle, other} {
		if got, err := s.IssueTotalCost(ctx, is.ID); err != nil || got != want[is.ID] {
			t.Errorf("IssueTotalCost(%s %s) = %v, %v; want %v", is.Key, is.Type, got, err, want[is.ID])
		}
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

// M5.1: the live view follows the active runs of an issue and of everything
// under it, oldest first; finished runs and other trees stay out.
func TestListActiveRunsUnder(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	story := mustTyped(t, s, p.ID, domain.IssueStory, &epic)
	task := mustTyped(t, s, p.ID, domain.IssueTask, &story)
	bug := mustTyped(t, s, p.ID, domain.IssueBug, &epic)
	other := mustTyped(t, s, p.ID, domain.IssueTask, nil)

	done := mustRun(t, s, bug.ID)
	if _, err := s.TransitionRun(ctx, done.ID, domain.ExecPending, domain.ExecFailed); err != nil {
		t.Fatal(err)
	}
	first := mustRun(t, s, task.ID)
	if _, err := s.TransitionRun(ctx, first.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	second := mustRun(t, s, bug.ID)
	mustRun(t, s, other.ID)

	keys := func(id uuid.UUID) []string {
		t.Helper()
		runs, err := s.ListActiveRunsUnder(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range runs {
			out = append(out, r.IssueKey+":"+string(r.Status))
		}
		return out
	}
	for _, c := range []struct {
		issue domain.Issue
		want  []string
	}{
		{epic, []string{task.Key + ":running", bug.Key + ":pending"}}, // a grandchild and a child
		{story, []string{task.Key + ":running"}},
		{task, []string{task.Key + ":running"}},
		{other, []string{other.Key + ":pending"}},
	} {
		if got := keys(c.issue.ID); !slices.Equal(got, c.want) {
			t.Errorf("%s: live runs = %v, want %v", c.issue.Key, got, c.want)
		}
	}
	runs, err := s.ListActiveRunsUnder(ctx, epic.ID)
	if err != nil || runs[0].ID != first.ID || runs[1].ID != second.ID || runs[0].IssueTitle != task.Title || runs[0].WorkflowVersion != first.WorkflowVersion {
		t.Errorf("live runs = %+v, %v", runs, err)
	}
}

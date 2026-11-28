package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// IssueSummary is an issue as the board lists it (M5): the issue, what its runs
// spent, and its latest run, if any.
type IssueSummary struct {
	domain.Issue
	CostUSD float64
	// TotalCostUSD is CostUSD plus what every issue under it spent (D142).
	TotalCostUSD float64
	// LatestRunID is uuid.Nil when the issue has never run.
	LatestRunID     uuid.UUID
	LatestRunStatus domain.ExecStatus
	// LatestChangeRequest is the issue's newest change request; Number is 0
	// when it has none.
	LatestChangeRequest ChangeRequestRef
}

// ChangeRequestRef names a change request on its provider.
type ChangeRequestRef struct {
	Number int
	URL    string
	Status string // open, merged or closed
}

// ListProjects returns every project by name.
func (s *Store) ListProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.q.ListProjects(ctx)
	if err != nil {
		return nil, mapErr("list projects", err)
	}
	out := make([]domain.Project, len(rows))
	for i, r := range rows {
		out[i] = projectFromRow(r)
	}
	return out, nil
}

// ListProjectIssues returns all of a project's issues, highest priority first,
// then by number.
func (s *Store) ListProjectIssues(ctx context.Context, projectID uuid.UUID) ([]IssueSummary, error) {
	rows, err := s.q.ListProjectIssues(ctx, projectID)
	if err != nil {
		return nil, mapErr("list project issues", err)
	}
	out := make([]IssueSummary, len(rows))
	for i, r := range rows {
		out[i] = IssueSummary{Issue: issueFromRow(r.Issue), CostUSD: r.CostUsd, TotalCostUSD: r.TotalCostUsd,
			LatestRunID: r.LatestRunID, LatestRunStatus: domain.ExecStatus(r.LatestRunStatus),
			LatestChangeRequest: ChangeRequestRef{Number: int(r.LatestCrNumber), URL: r.LatestCrUrl, Status: r.LatestCrStatus}}
	}
	return out, nil
}

// ProjectDependencies returns every dependency edge between a project's issues (D57).
func (s *Store) ProjectDependencies(ctx context.Context, projectID uuid.UUID) ([]domain.Dependency, error) {
	rows, err := s.q.ListProjectDependencies(ctx, projectID)
	if err != nil {
		return nil, mapErr("list project dependencies", err)
	}
	out := make([]domain.Dependency, len(rows))
	for i, r := range rows {
		out[i] = domain.Dependency{IssueID: r.IssueID, DependsOnID: r.DependsOnID}
	}
	return out, nil
}

// IssueRun is one of an issue's runs as the issue page lists it.
type IssueRun struct {
	ID     uuid.UUID
	Status domain.ExecStatus
	// WorkflowVersion is what the run executes, as name@version.
	WorkflowVersion string
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CostUSD         float64
}

// ListIssueRuns returns an issue's runs, newest first.
func (s *Store) ListIssueRuns(ctx context.Context, issueID uuid.UUID) ([]IssueRun, error) {
	rows, err := s.q.ListIssueRuns(ctx, issueID)
	if err != nil {
		return nil, mapErr("list issue runs", err)
	}
	out := make([]IssueRun, len(rows))
	for i, r := range rows {
		out[i] = IssueRun{ID: r.ID, Status: domain.ExecStatus(r.Status), WorkflowVersion: r.WorkflowVersion, CreatedAt: r.CreatedAt,
			StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, CostUSD: r.CostUsd}
	}
	return out, nil
}

// LiveRun is an active run of an issue or of an issue under it.
type LiveRun struct {
	ID              uuid.UUID
	Status          domain.ExecStatus
	WorkflowVersion string
	CreatedAt       time.Time
	IssueKey        string
	IssueTitle      string
}

// ListActiveRunsUnder returns the pending and running runs of the issue and of
// every issue under it, oldest first (M5.1).
func (s *Store) ListActiveRunsUnder(ctx context.Context, issueID uuid.UUID) ([]LiveRun, error) {
	rows, err := s.q.ListActiveRunsUnder(ctx, issueID)
	if err != nil {
		return nil, mapErr("list active runs under issue", err)
	}
	out := make([]LiveRun, len(rows))
	for i, r := range rows {
		out[i] = LiveRun{ID: r.ID, Status: domain.ExecStatus(r.Status), WorkflowVersion: r.WorkflowVersion, CreatedAt: r.CreatedAt,
			IssueKey: r.IssueKey, IssueTitle: r.IssueTitle}
	}
	return out, nil
}

// IssueUsage sums the model usage of all an issue's runs (D53).
func (s *Store) IssueUsage(ctx context.Context, issueID uuid.UUID) (domain.Usage, error) {
	r, err := s.q.IssueUsage(ctx, issueID)
	if err != nil {
		return domain.Usage{}, mapErr("issue usage", err)
	}
	return domain.Usage{CostUSD: r.CostUsd, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}, nil
}

// IssueTotalCost is what an issue and every issue under it spent, canceled
// ones included (D142).
func (s *Store) IssueTotalCost(ctx context.Context, issueID uuid.UUID) (float64, error) {
	cost, err := s.q.IssueTotalCost(ctx, issueID)
	if err != nil {
		return 0, mapErr("issue total cost", err)
	}
	return cost, nil
}

// Package scheduler decides which requested issues start (D62, docs/spec/03
// "Scheduling"): it honors the dependency DAG, the hierarchy and the deployment's
// maxConcurrentIssues, and starts each chosen issue's run.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// Starter starts an issue's run; starting one that is already running is a no-op.
type Starter interface {
	StartIssueRun(ctx context.Context, in activities.IssueRunInput) error
}

// Project is where a project's runs work: its repo and base branch.
type Project struct {
	Repo       string
	BaseBranch string
}

// Scheduler starts requested issues as their dependencies finish and slots free up.
type Scheduler struct {
	Store    *store.Store
	Starter  Starter
	Projects map[uuid.UUID]Project // by project ID
	// Limit is maxConcurrentIssues: how many issues may hold a run at once.
	Limit    int
	Interval time.Duration
	Log      *slog.Logger
}

// Run plans and starts until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		if err := s.Pass(ctx); err != nil {
			s.Log.Warn("scheduler pass", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.Interval):
		}
	}
}

// Pass plans once and starts what the plan says. An issue whose start fails is
// tried again on the next pass.
func (s *Scheduler) Pass(ctx context.Context) error {
	issues, deps, err := s.Store.SchedulingState(ctx)
	if err != nil {
		return err
	}
	plan := domain.PlanSchedule(issues, deps, s.Limit)
	if len(plan.Start) > 0 {
		s.warnOverlaps(ctx, plan.Start)
	}
	var errs []error
	for _, c := range plan.Start {
		if err := s.start(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", c.Key, err))
		}
	}
	return errors.Join(errs...)
}

// warnOverlaps logs what the issues about to start likely share with issues in
// flight (D63, FR-MQ-4). It warns and does not hold anything back.
func (s *Scheduler) warnOverlaps(ctx context.Context, starting []domain.SchedIssue) {
	all, err := s.Store.ListPathsIssues(ctx)
	if err != nil {
		s.Log.Warn("overlap check", "error", err)
		return
	}
	keys := map[string]bool{}
	for _, c := range starting {
		keys[c.Key] = true
	}
	var candidates []domain.PathsIssue
	for _, is := range all {
		if keys[is.Key] {
			candidates = append(candidates, is)
		}
	}
	for _, o := range domain.FindOverlaps(candidates, all) {
		s.Log.Warn("starting an issue that likely overlaps one in flight", "issue", o.Key, "with", o.With, "paths", o.Paths)
	}
}

func (s *Scheduler) start(ctx context.Context, id uuid.UUID) error {
	is, err := s.Store.GetIssue(ctx, id)
	if err != nil {
		return err
	}
	p, ok := s.Projects[is.ProjectID]
	if !ok {
		return errors.New("its project is not configured on this deployment")
	}
	if err := s.Starter.StartIssueRun(ctx, activities.IssueRunInput{
		IssueID: is.ID.String(), IssueKey: is.Key, Title: is.Title, Spec: is.Spec,
		AcceptanceCriteria: is.AcceptanceCriteria, SourceURL: is.SourceRef,
		Repo: p.Repo, BaseBranch: p.BaseBranch,
	}); err != nil {
		return err
	}
	// Recorded only once the run is on its way, so a failed start holds no slot.
	if err := s.Store.MarkIssueScheduled(ctx, is.ID); err != nil {
		return err
	}
	s.Log.Info("issue started", "issue", is.Key, "requested_by", is.StartRequestedBy)
	return nil
}

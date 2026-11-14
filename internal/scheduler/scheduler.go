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

// Starter starts an issue's run, or its refinement (D66); starting one that is
// already under way is a no-op.
type Starter interface {
	StartIssueRun(ctx context.Context, in activities.IssueRunInput) error
	StartRefinement(ctx context.Context, in activities.IssueRunInput) error
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
	var errs []error
	// Drafts whose spec needs no approval become ready first (D64), so the ones
	// planned to start can.
	notReady := map[uuid.UUID]bool{}
	for _, d := range plan.MakeReady {
		if _, err := s.Store.TransitionIssue(ctx, d.ID, domain.IssueDraft, domain.IssueReady); err != nil {
			errs = append(errs, fmt.Errorf("make %s ready: %w", d.Key, err))
			notReady[d.ID] = true
		}
	}
	if len(plan.Start) > 0 {
		s.warnOverlaps(ctx, plan.Start)
	}
	for _, c := range plan.Start {
		if notReady[c.ID] {
			continue
		}
		if err := s.start(ctx, c.ID, false); err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", c.Key, err))
		}
	}
	for _, c := range plan.Refine {
		if err := s.start(ctx, c.ID, true); err != nil {
			errs = append(errs, fmt.Errorf("refine %s: %w", c.Key, err))
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

// start starts the issue's run, or its refinement.
func (s *Scheduler) start(ctx context.Context, id uuid.UUID, refine bool) error {
	is, err := s.Store.GetIssue(ctx, id)
	if err != nil {
		return err
	}
	p, ok := s.Projects[is.ProjectID]
	if !ok {
		return errors.New("its project is not configured on this deployment")
	}
	in := activities.IssueRunInput{
		IssueID: is.ID.String(), IssueKey: is.Key, Title: is.Title, Spec: is.Spec,
		AcceptanceCriteria: is.AcceptanceCriteria, SourceURL: is.SourceRef,
		Repo: p.Repo, BaseBranch: p.BaseBranch,
	}
	startFn := s.Starter.StartIssueRun
	if refine {
		in.SpecFeedback, startFn = is.SpecFeedback, s.Starter.StartRefinement
		if in.Dependencies, err = s.dependencies(ctx, is); err != nil {
			return err
		}
	}
	if err := startFn(ctx, in); err != nil {
		return err
	}
	// Recorded only once the run is on its way, so a failed start holds no slot.
	if err := s.Store.MarkIssueScheduled(ctx, is.ID); err != nil {
		return err
	}
	// The requester is for the log: a failed lookup does not fail a start that
	// already happened.
	by, via, err := s.Store.StartRequester(ctx, is)
	if err != nil {
		s.Log.Warn("find who requested a start", "issue", is.Key, "error", err)
	}
	attrs := []any{"issue", is.Key, "refinement", refine, "requested_by", by}
	if via != "" && via != is.Key {
		attrs = append(attrs, "requested_via", via)
	}
	s.Log.Info("issue started", attrs...)
	return nil
}

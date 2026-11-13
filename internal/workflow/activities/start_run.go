package activities

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// StartRun creates the Run and its implement StepRun and marks the issue running.
func (a *Activities) StartRun(ctx context.Context, in IssueRunInput) (StartRunResult, error) {
	issueID, err := uuid.Parse(in.IssueID)
	if err != nil {
		return StartRunResult{}, temporal.NewNonRetryableApplicationError("bad issue id", "input", err)
	}
	// One Run per Temporal execution: a retried attempt finds the Run it created.
	// The workflow ID is reused by reruns of the same issue, so it cannot be the key.
	exec := activity.GetInfo(ctx).WorkflowExecution
	run, err := a.Store.GetRunByTemporalRunID(ctx, exec.RunID)
	if errors.Is(err, store.ErrNotFound) {
		run, err = a.Store.CreateRun(ctx, store.NewRun{IssueID: issueID, WorkflowSnapshot: hardcodedWorkflow,
			WorkflowVersion: "m1-1", TemporalWorkflowID: exec.ID, TemporalRunID: exec.RunID})
	}
	if err != nil {
		return StartRunResult{}, err
	}
	if _, err := a.Store.TransitionIssue(ctx, issueID, domain.IssueReady, domain.IssueRunning); err != nil {
		return StartRunResult{}, err
	}
	if _, err := a.Store.TransitionRun(ctx, run.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		return StartRunResult{}, err
	}
	step, err := a.Store.CreateStepRun(ctx, store.NewStepRun{RunID: run.ID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if errors.Is(err, store.ErrConflict) {
		var steps []domain.StepRun
		if steps, err = a.Store.ListStepRuns(ctx, run.ID); err == nil && len(steps) > 0 {
			step = steps[0]
		}
	}
	if err != nil {
		return StartRunResult{}, err
	}
	return StartRunResult{RunID: run.ID.String(), StepRunID: step.ID.String(), Branch: BranchName(in.IssueKey, in.Title)}, nil
}

package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/store"
)

// StartWorkflowRun creates the Interpret run with its snapshot (invariant 4)
// and marks the issue running. Steps get their StepRuns as they execute
// (StartStep). Idempotent per Temporal execution, like StartRun.
func (a *Activities) StartWorkflowRun(ctx context.Context, in IssueRunInput, snap snapshot.Snapshot) (StartRunResult, error) {
	issueID, err := uuid.Parse(in.IssueID)
	if err != nil {
		return StartRunResult{}, temporal.NewNonRetryableApplicationError("bad issue id", "input", err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return StartRunResult{}, temporal.NewNonRetryableApplicationError("encode snapshot", "input", err)
	}
	exec := activity.GetInfo(ctx).WorkflowExecution
	run, err := a.Store.GetRunByTemporalRunID(ctx, exec.RunID)
	if errors.Is(err, store.ErrNotFound) {
		run, err = a.Store.CreateRun(ctx, store.NewRun{IssueID: issueID, WorkflowSnapshot: raw,
			WorkflowVersion: snap.Version(), TemporalWorkflowID: exec.ID, TemporalRunID: exec.RunID})
	}
	if err != nil {
		return StartRunResult{}, fmt.Errorf("start run: %w", err)
	}
	if _, err := a.Store.TransitionIssue(ctx, issueID, domain.IssueReady, domain.IssueRunning); err != nil {
		return StartRunResult{}, err
	}
	if _, err := a.Store.TransitionRun(ctx, run.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		return StartRunResult{}, err
	}
	return StartRunResult{RunID: run.ID.String(), Branch: BranchName(in.IssueKey, in.Title)}, nil
}

package activities

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// FinishRun records the outcome on the run and the issue, and on IssueRun's
// single agent step (Interpret ends its steps itself and passes no StepRunID).
// Without a merge queue (M6) a successful issue waits for a human on its PR.
func (a *Activities) FinishRun(ctx context.Context, in FinishInput) error {
	issueID, _ := uuid.Parse(in.IssueID)
	runID, _ := uuid.Parse(in.RunID)
	var stepRef *uuid.UUID
	stepID, err := uuid.Parse(in.StepRunID)
	if err == nil {
		stepRef = &stepID
	}
	stepStatus, runStatus, issueStatus := domain.ExecFailed, domain.ExecFailed, domain.IssueFailed
	if in.Succeeded {
		stepStatus, runStatus, issueStatus = domain.ExecSucceeded, domain.ExecSucceeded, domain.IssueAwaitingHuman
	}
	if in.AgentSucceeded {
		stepStatus = domain.ExecSucceeded
	}
	if stepRef != nil {
		if step, err := a.Store.GetStepRun(ctx, stepID); err == nil && !step.Status.Terminal() {
			if _, err := a.Store.TransitionStepRun(ctx, stepID, step.Status, stepStatus); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
				return err
			}
		}
	}
	if _, err := a.Store.TransitionRun(ctx, runID, domain.ExecRunning, runStatus); err != nil {
		return err
	}
	if in.Merged {
		issueStatus = domain.IssueMerged
	}
	// A run that enqueued its change request ends with the issue queued
	// (D88); one canceled while it waited for a person, awaiting_human (D91).
	from := domain.IssueRunning
	is, err := a.Store.GetIssue(ctx, issueID)
	if err != nil {
		return err
	}
	if is.Status == domain.IssueQueued || is.Status == domain.IssueAwaitingHuman {
		from = is.Status
	}
	// The queue's merge already moved the issue to merged (D121), and its
	// release may even have finished.
	done := is.Status == issueStatus || (in.Merged && is.Status == domain.IssueReleased)
	if !done { // a retry finds it there already
		if _, err := a.Store.TransitionIssue(ctx, issueID, from, issueStatus); err != nil {
			return err
		}
	}
	if !in.Succeeded {
		a.Log.Warn("run failed", "run", in.RunID, "reason", in.Reason)
		// The transcript must say why the run ended, not only that it did.
		payload, _ := json.Marshal(map[string]string{"message": "run failed", "detail": in.Reason})
		if _, err := a.Store.AppendEvent(ctx, store.NewEvent{RunID: &runID, StepRunID: stepRef, Type: domain.EventError, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
}

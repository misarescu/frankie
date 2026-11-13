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
	if _, err := a.Store.TransitionIssue(ctx, issueID, domain.IssueRunning, issueStatus); err != nil {
		return err
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

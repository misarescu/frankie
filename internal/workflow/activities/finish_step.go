package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// FinishStep ends a step's execution, from whichever status it reached. A step
// that already ended (a gate records its own verdict) is left as it is.
func (a *Activities) FinishStep(ctx context.Context, in StepEnd) error {
	id, err := uuid.Parse(in.StepRunID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil {
		return err
	}
	if step.Status.Terminal() {
		return nil
	}
	to := domain.ExecFailed
	if in.Succeeded {
		to = domain.ExecSucceeded
		if step.Status == domain.ExecPending {
			// Steps without a pod (actions) never entered running.
			if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecPending, domain.ExecRunning); err != nil {
				return err
			}
			step.Status = domain.ExecRunning
		}
	}
	_, err = a.Store.TransitionStepRun(ctx, id, step.Status, to)
	return err
}

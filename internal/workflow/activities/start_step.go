package activities

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// StartStep creates the pending StepRun of one execution of a step, or finds
// it on a retry: (run, step, attempt) is unique.
func (a *Activities) StartStep(ctx context.Context, in StepStart) (string, error) {
	runID, err := uuid.Parse(in.RunID)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError("bad run id", "input", err)
	}
	step, err := a.stepRun(ctx, store.NewStepRun{RunID: runID, StepID: in.StepID, Attempt: in.Attempt, Kind: in.Kind})
	if err != nil {
		return "", err
	}
	return step.ID.String(), nil
}

// stepRun creates a StepRun, or returns the existing one after a conflict.
func (a *Activities) stepRun(ctx context.Context, in store.NewStepRun) (domain.StepRun, error) {
	step, err := a.Store.CreateStepRun(ctx, in)
	if !errors.Is(err, store.ErrConflict) {
		return step, err
	}
	steps, err := a.Store.ListStepRuns(ctx, in.RunID)
	if err != nil {
		return domain.StepRun{}, err
	}
	for _, s := range steps {
		if s.StepID == in.StepID && s.Attempt == in.Attempt {
			return s, nil
		}
	}
	return domain.StepRun{}, fmt.Errorf("step run %s/%d of run %s not found after a conflict", in.StepID, in.Attempt, in.RunID)
}

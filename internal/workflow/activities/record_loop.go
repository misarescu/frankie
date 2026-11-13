package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
)

// RecordLoop records that a gate looped back for the loops-th time, on the Run
// (docs/spec/03: loop counts are recorded per issue and surface in metrics).
// The workflow keeps its own count; this is the queryable copy.
func (a *Activities) RecordLoop(ctx context.Context, runID, gateStepID string, loops int) error {
	id, err := uuid.Parse(runID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad run id", "input", err)
	}
	_, err = a.Store.SetRunLoopCount(ctx, id, gateStepID, loops)
	return err
}

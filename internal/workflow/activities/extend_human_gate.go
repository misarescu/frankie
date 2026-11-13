package activities

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
)

// ExtendHumanGate moves a pending human gate's expiry when it timed out with
// onTimeout: extend.
func (a *Activities) ExtendHumanGate(ctx context.Context, stepRunID string, expiresAt time.Time) error {
	id, err := uuid.Parse(stepRunID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	_, err = a.Store.ExtendApproval(ctx, id, expiresAt)
	return err
}

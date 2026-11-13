package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// OpenHumanGate records the decision a human gate waits for, where the
// approvals inbox and the API find it, and marks the gate's StepRun running.
// Idempotent: a retry finds the approval it opened.
func (a *Activities) OpenHumanGate(ctx context.Context, req HumanGateRequest) error {
	id, err := uuid.Parse(req.StepRunID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	if _, err := a.Store.OpenApproval(ctx, store.NewApproval{StepRunID: id, Reason: req.Reason, CommitSHA: req.CommitSHA,
		ChangeRequestURL: req.ChangeRequestURL, ExpiresAt: req.ExpiresAt}); err != nil {
		return err
	}
	if req.CommitSHA != "" {
		if _, err := a.Store.SetStepRunCommit(ctx, id, req.CommitSHA); err != nil {
			return err
		}
	}
	_, err = a.Store.TransitionStepRun(ctx, id, domain.ExecPending, domain.ExecRunning)
	return err
}

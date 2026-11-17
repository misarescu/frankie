package activities

import (
	"context"

	"github.com/misarescu/frankie/internal/domain"
)

// RollbackStage asks the stage's driver to put the stage back to what it ran
// before the failed deployment (e.g. revert its GitOps commit), records the
// state the driver returns and marks the deployment rolled back (FR-RL-2,
// D114). A retry rolls back again, which drivers must take as the same change.
func (a *Activities) RollbackStage(ctx context.Context, req StageRequest) error {
	c, err := a.resolve(ctx, req)
	if err != nil {
		return err
	}
	if c.deployment.Status == domain.ReleaseRolledBack {
		return nil
	}
	state, err := c.driver.Rollback(ctx, c.request)
	if err != nil {
		return driverError("roll back "+req.Stage.Name, err)
	}
	if _, err := a.Store.SetDeploymentDriverState(ctx, c.deployment.ID, state); err != nil {
		return err
	}
	_, err = a.Store.TransitionDeployment(ctx, c.deployment.ID, domain.ReleaseFailed, domain.ReleaseRolledBack, "")
	return err
}

package activities

import (
	"context"
)

// PublishStage asks the stage's driver to deploy the release (e.g. commit its
// image to the GitOps repository) and records the state the driver returns
// (D112, D114). It returns once the change is made; AwaitStage waits for the
// stage to run it. A retry publishes again, which drivers must take as the same
// change.
func (a *Activities) PublishStage(ctx context.Context, req StageRequest) error {
	c, err := a.resolve(ctx, req)
	if err != nil {
		return err
	}
	state, err := c.driver.Publish(ctx, c.request)
	if err != nil {
		return driverError("publish to "+req.Stage.Name, err)
	}
	_, err = a.Store.SetDeploymentDriverState(ctx, c.deployment.ID, state)
	return err
}

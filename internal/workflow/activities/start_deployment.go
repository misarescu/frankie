package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// StartDeployment records the release's deployment to the stage and starts it
// (D114); it returns the deployment's ID. Idempotent: the deployment of a
// release to a stage is created and started once.
func (a *Activities) StartDeployment(ctx context.Context, req StageRequest) (string, error) {
	releaseID, err := uuid.Parse(req.ReleaseID)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError("bad release id", "input", err)
	}
	dep, err := a.Store.CreateDeployment(ctx, store.NewDeployment{ReleaseID: releaseID, Stage: req.Stage.Name, Driver: req.Stage.Driver})
	if err != nil {
		return "", err
	}
	if dep.Status == domain.ReleasePending {
		if _, err := a.Store.TransitionDeployment(ctx, dep.ID, domain.ReleasePending, domain.ReleaseInProgress, ""); err != nil {
			return "", err
		}
	}
	return dep.ID.String(), nil
}

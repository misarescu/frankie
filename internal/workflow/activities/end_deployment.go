package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// EndDeploymentRequest ends a running deployment: succeeded, or failed with
// the reason.
type EndDeploymentRequest struct {
	DeploymentID string
	Succeeded    bool
	Reason       string `json:",omitempty"`
}

// EndDeployment records how a deployment ended (D114). Idempotent.
func (a *Activities) EndDeployment(ctx context.Context, req EndDeploymentRequest) error {
	id, err := uuid.Parse(req.DeploymentID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad deployment id", "input", err)
	}
	to := domain.ReleaseFailed
	if req.Succeeded {
		to = domain.ReleaseSucceeded
	}
	_, err = a.Store.TransitionDeployment(ctx, id, domain.ReleaseInProgress, to, req.Reason)
	return err
}

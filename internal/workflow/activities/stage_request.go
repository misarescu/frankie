package activities

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
)

// StageRequest names one release at one stage, for the Activities that deploy
// it (D114). DeploymentID is set once StartDeployment recorded it.
type StageRequest struct {
	ReleaseID string
	Repo      string
	SHA       string
	// BaseBranch is the branch the commit is on (D116).
	BaseBranch   string `json:",omitempty"`
	Stage        ReleaseStage
	DeploymentID string `json:",omitempty"`
}

// stageCall is a StageRequest resolved: its driver, its deployment and the
// request the driver gets.
type stageCall struct {
	driver     drivers.Driver
	deployment domain.Deployment
	request    drivers.Request
}

// resolve finds the stage's driver and its recorded deployment.
func (a *Activities) resolve(ctx context.Context, req StageRequest) (stageCall, error) {
	releaseID, err1 := uuid.Parse(req.ReleaseID)
	depID, err2 := uuid.Parse(req.DeploymentID)
	if err := errors.Join(err1, err2); err != nil {
		return stageCall{}, temporal.NewNonRetryableApplicationError("bad id", "input", err)
	}
	d, err := a.driverRegistry().Get(req.Stage.Driver)
	if err != nil {
		return stageCall{}, temporal.NewNonRetryableApplicationError(err.Error(), "config", err)
	}
	dep, err := a.Store.GetDeployment(ctx, depID)
	if err != nil {
		return stageCall{}, err
	}
	if dep.ReleaseID != releaseID || dep.Stage != req.Stage.Name {
		return stageCall{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("deployment %s is not release %s at %s", depID, releaseID, req.Stage.Name), "input", nil)
	}
	return stageCall{driver: d, deployment: dep, request: drivers.Request{
		Stage:      drivers.Stage{Name: req.Stage.Name, Config: req.Stage.Config},
		Subject:    drivers.Subject{Repo: req.Repo, SHA: req.SHA, ReleaseID: releaseID},
		Deployment: drivers.Deployment{ID: dep.ID, State: dep.DriverState},
	}}, nil
}

// driverError makes a driver's refusal (unsupported, invalid) final; any other
// error is retried.
func driverError(op string, err error) error {
	if errors.Is(err, drivers.ErrUnsupported) || errors.Is(err, drivers.ErrInvalidRequest) || errors.Is(err, drivers.ErrInvalidConfig) {
		return temporal.NewNonRetryableApplicationError(op+": "+err.Error(), "driver", err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

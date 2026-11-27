package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// ReleaseGateRequest asks a person to approve a release before it deploys to
// a gated stage (D139).
type ReleaseGateRequest struct {
	ReleaseID string
	Repo      string
	SHA       string
	Stage     string
	ExpiresAt time.Time
}

// OpenReleaseGate records a stage's human gate as a step, named after the
// stage, of the release's run (D129), and opens its approval, which the inbox
// shows and whose decision the API signals to the release workflow. It
// returns the gate's step run ID. Idempotent: a retry finds the step and the
// approval it opened.
func (a *Activities) OpenReleaseGate(ctx context.Context, req ReleaseGateRequest) (string, error) {
	relID, err := uuid.Parse(req.ReleaseID)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError("bad release id", "input", err)
	}
	rel, err := a.Store.GetRelease(ctx, relID)
	if err != nil {
		return "", err
	}
	run, err := a.releaseRun(ctx, rel, req.Repo)
	if err != nil {
		return "", err
	}
	step, err := a.stepRun(ctx, store.NewStepRun{RunID: run.ID, StepID: req.Stage, Attempt: 1, Kind: domain.StepGate})
	if err != nil {
		return "", err
	}
	reason := fmt.Sprintf("Release %s of %s passed the stages before %s. Approve to deploy it to %s, or reject to stop the release before it.",
		short(req.SHA), req.Repo, req.Stage, req.Stage)
	if err := a.OpenHumanGate(ctx, HumanGateRequest{RunID: run.ID.String(), StepRunID: step.ID.String(), StepID: req.Stage,
		Reason: reason, CommitSHA: req.SHA, ExpiresAt: req.ExpiresAt}); err != nil {
		return "", err
	}
	return step.ID.String(), nil
}

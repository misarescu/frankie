package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// FinishReleaseRequest ends a running release with its outcome.
type FinishReleaseRequest struct {
	ReleaseID string
	Status    domain.ReleaseStatus // succeeded, failed or rolled_back
	Reason    string               `json:",omitempty"`
}

// FinishRelease records how a release ended (D114): one that succeeded
// releases its issues (D110). Idempotent.
func (a *Activities) FinishRelease(ctx context.Context, req FinishReleaseRequest) error {
	id, err := uuid.Parse(req.ReleaseID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad release id", "input", err)
	}
	if !req.Status.Finished() {
		return temporal.NewNonRetryableApplicationError("a release ends succeeded, failed or rolled back, not "+string(req.Status), "input", nil)
	}
	_, err = a.Store.TransitionRelease(ctx, id, domain.ReleaseInProgress, req.Status, req.Reason)
	return err
}

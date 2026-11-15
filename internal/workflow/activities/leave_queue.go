package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// LeaveQueue moves an issue the merge queue sent back from queued to running,
// as its run loops back to the coder (D89). An issue already running stays so.
func (a *Activities) LeaveQueue(ctx context.Context, issueID string) error {
	id, err := uuid.Parse(issueID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad issue id", "input", err)
	}
	_, err = a.Store.TransitionIssue(ctx, id, domain.IssueQueued, domain.IssueRunning)
	return err
}

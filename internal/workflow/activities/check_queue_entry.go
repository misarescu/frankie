package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// QueueEntryState is whether the queue should still process a change request.
type QueueEntryState struct {
	// Queued is false when the issue left the queue, e.g. it was canceled.
	Queued bool
	// ChangeRequest is the change request's state on the provider: a person
	// may have merged or closed it while it waited.
	ChangeRequest domain.ChangeRequestStatus
}

// CheckQueueEntry reads the issue's status and its change request's provider
// state before the queue works on it (D87).
func (a *Activities) CheckQueueEntry(ctx context.Context, issue IssueRunInput, number int) (QueueEntryState, error) {
	id, err := uuid.Parse(issue.IssueID)
	if err != nil {
		return QueueEntryState{}, temporal.NewNonRetryableApplicationError("bad issue id", "input", err)
	}
	is, err := a.Store.GetIssue(ctx, id)
	if err != nil {
		return QueueEntryState{}, err
	}
	status, err := a.GitHub.ChangeRequestStatus(ctx, issue.Repo, number)
	if err != nil {
		return QueueEntryState{}, err
	}
	return QueueEntryState{Queued: is.Status == domain.IssueQueued, ChangeRequest: status}, nil
}

package activities

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// CloseChangeRequest closes the issue's open change request for branch on the
// provider, with comment saying who rejected it and why, and records it closed
// (D92). Closing one that is closed already does nothing; a run without an
// open change request for the branch has nothing to close.
func (a *Activities) CloseChangeRequest(ctx context.Context, issue IssueRunInput, branch, comment string) error {
	issueID, err := uuid.Parse(issue.IssueID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad issue id", "input", err)
	}
	crs, err := a.Store.ListChangeRequestsForIssue(ctx, issueID)
	if err != nil {
		return err
	}
	for _, cr := range crs {
		if cr.Branch != branch || cr.Status != string(domain.ChangeRequestOpen) {
			continue
		}
		if err := a.GitHub.CloseChangeRequest(ctx, cr.Repo, cr.Number, comment); err != nil {
			return err
		}
		if err := a.Store.RecordChangeRequestClosed(ctx, cr.ID); err != nil {
			return err
		}
	}
	return nil
}

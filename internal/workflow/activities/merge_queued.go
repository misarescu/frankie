package activities

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// QueueMergeRequest merges a queued change request whose branch the queue
// brought up to date and checked (D87).
type QueueMergeRequest struct {
	Issue           IssueRunInput
	StepRunID       string
	ChangeRequestID string
	Number          int
	HeadSHA         string // the commit the queue checked
	BaseSHA         string // the base commit it contains
	Method          domain.MergeMethod
	Title           string
}

// Queue merge outcomes.
const (
	QueueMerged    = "merged"     // merged; MergeSHA is the commit on the base branch
	QueueBaseMoved = "base-moved" // the base branch moved since the update: update again
	QueueHeadMoved = "head-moved" // the change request moved since it was checked
	QueueRefused   = "refused"    // the provider refused the merge (Reason says why)
)

// QueueMergeResult is what the merge did.
type QueueMergeResult struct {
	Outcome  string
	MergeSHA string
	Reason   string
}

// MergeQueued merges the change request at HeadSHA, only while the base branch
// is still at BaseSHA, so the base branch receives exactly the tree the gates
// checked (D87), and records the merge. A change request already merged, e.g.
// before this Activity was retried, reports that merge.
func (a *Activities) MergeQueued(ctx context.Context, req QueueMergeRequest) (QueueMergeResult, error) {
	stepID, err1 := uuid.Parse(req.StepRunID)
	crID, err2 := uuid.Parse(req.ChangeRequestID)
	if err := errors.Join(err1, err2); err != nil {
		return QueueMergeResult{}, temporal.NewNonRetryableApplicationError("bad id", "input", err)
	}
	if err := (domain.MergeRequest{HeadSHA: req.HeadSHA, Method: req.Method}).Validate(); err != nil {
		return QueueMergeResult{}, temporal.NewNonRetryableApplicationError(err.Error(), "input", err)
	}
	repo := req.Issue.Repo
	status, err := a.GitHub.ChangeRequestStatus(ctx, repo, req.Number)
	if err != nil {
		return QueueMergeResult{}, err
	}
	if status != domain.ChangeRequestMerged {
		base, found, err := a.GitHub.BranchHead(ctx, repo, req.Issue.BaseBranch)
		if err != nil {
			return QueueMergeResult{}, err
		}
		if !found {
			return QueueMergeResult{}, temporal.NewNonRetryableApplicationError("base branch "+req.Issue.BaseBranch+" not found", "provider", nil)
		}
		if base != req.BaseSHA {
			return a.endMerge(ctx, stepID, QueueMergeResult{Outcome: QueueBaseMoved, Reason: fmt.Sprintf("%s moved to %s", req.Issue.BaseBranch, base)})
		}
	}
	sha, err := a.GitHub.MergeChangeRequest(ctx, repo, req.Number, domain.MergeRequest{HeadSHA: req.HeadSHA, Method: req.Method, Title: req.Title})
	switch {
	case errors.Is(err, domain.ErrHeadMoved):
		return a.endMerge(ctx, stepID, QueueMergeResult{Outcome: QueueHeadMoved, Reason: err.Error()})
	case errors.Is(err, domain.ErrNotMergeable):
		return a.endMerge(ctx, stepID, QueueMergeResult{Outcome: QueueRefused, Reason: err.Error()})
	case err != nil:
		return QueueMergeResult{}, err
	}
	if _, err := a.Store.RecordChangeRequestMerged(ctx, crID); err != nil {
		return QueueMergeResult{}, err
	}
	if _, err := a.Store.SetStepRunCommit(ctx, stepID, sha); err != nil {
		return QueueMergeResult{}, err
	}
	return a.endMerge(ctx, stepID, QueueMergeResult{Outcome: QueueMerged, MergeSHA: sha})
}

// endMerge ends the merge's StepRun: succeeded when merged.
func (a *Activities) endMerge(ctx context.Context, stepID uuid.UUID, r QueueMergeResult) (QueueMergeResult, error) {
	step, err := a.Store.GetStepRun(ctx, stepID)
	if err != nil {
		return QueueMergeResult{}, err
	}
	to := domain.ExecFailed
	if r.Outcome == QueueMerged {
		to = domain.ExecSucceeded
	}
	if !step.Status.Terminal() {
		from := step.Status
		if from == domain.ExecPending {
			if _, err := a.Store.TransitionStepRun(ctx, stepID, domain.ExecPending, domain.ExecRunning); err != nil {
				return QueueMergeResult{}, err
			}
			from = domain.ExecRunning
		}
		if _, err := a.Store.TransitionStepRun(ctx, stepID, from, to); err != nil {
			return QueueMergeResult{}, err
		}
	}
	return r, nil
}

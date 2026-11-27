package activities

import (
	"context"

	"github.com/google/uuid"
)

// syncWaiting moves the issue of a step between running and awaiting_human
// to match whether its run waits for a person (D91). It runs after anything
// that opens or ends a wait: a human gate, a question, a step's end.
func (a *Activities) syncWaiting(ctx context.Context, stepRunID uuid.UUID) error {
	step, err := a.Store.GetStepRun(ctx, stepRunID)
	if err != nil {
		return err
	}
	run, err := a.Store.GetRun(ctx, step.RunID)
	if err != nil || run.IssueID == uuid.Nil { // a release's run has no issue
		return err
	}
	_, err = a.Store.SyncIssueWaiting(ctx, run.IssueID)
	return err
}

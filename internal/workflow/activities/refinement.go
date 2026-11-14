package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/store"
)

// StartRefinement creates the refinement's run with its snapshot (invariant 4)
// and moves the issue from draft to refining (D66). Idempotent per Temporal
// execution, like StartWorkflowRun.
func (a *Activities) StartRefinement(ctx context.Context, in IssueRunInput, snap snapshot.Snapshot) (StartRunResult, error) {
	return a.startSnapshotRun(ctx, in, snap, domain.IssueDraft, domain.IssueRefining)
}

// RefinementOutcome is how a refinement's session ended.
type RefinementOutcome struct {
	IssueID   string
	RunID     string
	Succeeded bool   // the session ended normally
	Reason    string // why it did not
	Result    string // the refiner's final message: a refined spec
}

// FinishRefinement records a refinement (D66): a valid refined spec waits on the
// issue for a person's approval; anything else (a failed session or a result
// outside the contract) fails the run and returns the issue to draft, held with
// the reason. It returns that reason, or "" when the spec waits for approval.
func (a *Activities) FinishRefinement(ctx context.Context, out RefinementOutcome) (string, error) {
	issueID, err1 := uuid.Parse(out.IssueID)
	runID, err2 := uuid.Parse(out.RunID)
	if err := errors.Join(err1, err2); err != nil {
		return "", fmt.Errorf("finish refinement: %w", err)
	}
	reason := out.Reason
	if out.Succeeded {
		spec, err := domain.ParseRefinedSpec([]byte(out.Result))
		if err == nil {
			return "", a.Store.RecordRefinement(ctx, issueID, runID, spec)
		}
		reason = "the refiner's result is not a refined spec: " + err.Error()
	}
	if reason == "" {
		reason = "the refinement did not finish"
	}
	a.Log.Warn("refinement failed", "run", out.RunID, "reason", reason)
	// The transcript must say why the refinement ended, not only that it did.
	payload, _ := json.Marshal(map[string]string{"message": "refinement failed", "detail": reason})
	if _, err := a.Store.AppendEvent(ctx, store.NewEvent{RunID: &runID, Type: domain.EventError, Payload: payload}); err != nil {
		return "", err
	}
	return reason, a.Store.FailRefinement(ctx, issueID, runID, reason)
}

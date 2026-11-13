package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// HumanGateDetails is the Details of a human gate result.
type HumanGateDetails struct {
	Reason  string                `json:"reason"`
	Status  domain.ApprovalStatus `json:"status"`
	By      string                `json:"by,omitempty"`
	Comment string                `json:"comment,omitempty"`
}

// CloseHumanGate records a human gate's outcome: the approval's decision, a
// gate result (pass only when approved) and the end of its StepRun.
// Idempotent; if the approval was already decided otherwise, that decision
// stands and is what the gate records.
func (a *Activities) CloseHumanGate(ctx context.Context, in HumanGateClose) error {
	id, err := uuid.Parse(in.StepRunID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	approval, err := a.Store.DecideApproval(ctx, id, in.Status, in.By, in.Comment)
	if errors.Is(err, store.ErrConflict) {
		approval, err = a.Store.GetApproval(ctx, id)
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(HumanGateDetails{Reason: approval.Reason, Status: approval.Status, By: approval.DecidedBy, Comment: approval.Comment})
	if err != nil {
		return fmt.Errorf("encode human gate details: %w", err)
	}
	verdict, status := domain.GateFail, domain.ExecFailed
	if approval.Status == domain.ApprovalApproved {
		verdict, status = domain.GatePass, domain.ExecSucceeded
	}
	if _, err := a.Store.RecordGateResult(ctx, store.NewGateResult{StepRunID: id, Type: domain.GateHuman, Policy: domain.GateBlocking,
		Verdict: verdict, Details: raw}); err != nil {
		return err
	}
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil || step.Status.Terminal() {
		return err
	}
	_, err = a.Store.TransitionStepRun(ctx, id, step.Status, status)
	return err
}

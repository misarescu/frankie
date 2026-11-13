package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// HumanDecisionSignal is the Temporal signal a human gate waits for
// (FR-WF-3); the API sends it when someone decides in the approvals inbox.
const HumanDecisionSignal = "human-gate-decision"

// HumanDecision is a person's decision on one human gate.
type HumanDecision struct {
	// StepRunID is the gate's StepRun; a decision for any other gate (an
	// earlier one, decided twice) is ignored.
	StepRunID string
	Approved  bool
	By        string
	Comment   string
}

// escalationSuffix names the human gate a failed gate escalates to.
const escalationSuffix = "-escalation"

// humanGate is a definition's human gate: the run continues only once a person
// approves; a rejection or a timeout fails it.
func (it *interpreter) humanGate(ctx workflow.Context, pc int, s config.Step) (int, string) {
	g := s.Gate
	reason := fmt.Sprintf("The %s workflow asks for a human decision at its `%s` gate.", it.in.Snapshot.Workflow.Name, s.ID)
	if why := it.awaitHuman(ctx, s.ID, reason, time.Duration(g.Timeout), g.OnTimeout); why != "" {
		return 0, why
	}
	return pc + 1, ""
}

// escalate routes a failed blocking gate to a person (FR-WF-4): approving
// accepts the change despite the failure and the run continues after the
// gate; rejecting, or no decision in time, fails the run.
func (it *interpreter) escalate(ctx workflow.Context, pc int, s config.Step, failure string) (int, string) {
	reason := fmt.Sprintf("The `%s` gate failed: %s. Approve to accept the change anyway and continue, or reject to fail the run.", s.ID, failure)
	if why := it.awaitHuman(ctx, s.ID+escalationSuffix, reason, time.Duration(config.DefaultHumanTimeout), config.OnTimeoutFail); why != "" {
		return 0, fmt.Sprintf("gate failed: %s; escalated to a human, %s", failure, why)
	}
	return pc + 1, ""
}

// awaitHuman opens a human gate under the step ID id and waits for its
// decision (FR-WF-3). It returns "" when a person approved, otherwise why the
// gate did not pass. onTimeout extend waits another period each time.
func (it *interpreter) awaitHuman(ctx workflow.Context, id, reason string, timeout time.Duration, onTimeout config.OnTimeout) string {
	stepRunID, err := it.startStep(ctx, id, domain.StepGate)
	if err != nil {
		return err.Error()
	}
	short := workflow.WithActivityOptions(ctx, shortOptions)
	err = workflow.ExecuteActivity(short, it.a.OpenHumanGate, activities.HumanGateRequest{
		RunID: it.run.RunID, StepRunID: stepRunID, StepID: id, Reason: reason, CommitSHA: it.session.CommitSHA,
		ChangeRequestURL: it.changeRequest, ExpiresAt: workflow.Now(ctx).Add(timeout),
	}).Get(ctx, nil)
	if err != nil {
		it.finishStep(ctx, stepRunID, false)
		return "opening the human gate failed: " + err.Error()
	}

	decisions := workflow.GetSignalChannel(ctx, HumanDecisionSignal)
	for {
		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		timer := workflow.NewTimer(timerCtx, timeout)
		var decision *HumanDecision
		expired := false
		for decision == nil && !expired && ctx.Err() == nil {
			sel := workflow.NewSelector(ctx)
			sel.AddReceive(decisions, func(c workflow.ReceiveChannel, _ bool) {
				var d HumanDecision
				c.Receive(ctx, &d)
				if d.StepRunID == stepRunID {
					decision = &d
				}
			})
			sel.AddFuture(timer, func(workflow.Future) { expired = true })
			sel.Select(ctx)
		}
		cancelTimer()

		switch {
		case ctx.Err() != nil:
			cleanup, _ := workflow.NewDisconnectedContext(ctx)
			// Best effort: the run is ending anyway; the approval must not stay pending.
			_ = it.closeHuman(cleanup, activities.HumanGateClose{StepRunID: stepRunID, Status: domain.ApprovalRejected,
				By: "frankie", Comment: "the run was canceled"})
			return "canceled while waiting for a human decision"
		case decision != nil:
			status := domain.ApprovalRejected
			if decision.Approved {
				status = domain.ApprovalApproved
			}
			if err := it.closeHuman(ctx, activities.HumanGateClose{StepRunID: stepRunID, Status: status, By: decision.By, Comment: decision.Comment}); err != nil {
				return "recording the decision failed: " + err.Error()
			}
			if decision.Approved {
				return ""
			}
			why := "rejected by " + decision.By
			if decision.Comment != "" {
				why += ": " + decision.Comment
			}
			return why
		case onTimeout == config.OnTimeoutExtend:
			if err := workflow.ExecuteActivity(short, it.a.ExtendHumanGate, stepRunID, workflow.Now(ctx).Add(timeout)).Get(ctx, nil); err != nil {
				return "extending the human gate failed: " + err.Error()
			}
		default:
			if err := it.closeHuman(ctx, activities.HumanGateClose{StepRunID: stepRunID, Status: domain.ApprovalTimedOut}); err != nil {
				return "recording the timeout failed: " + err.Error()
			}
			return fmt.Sprintf("no human decision within %s", timeout)
		}
	}
}

func (it *interpreter) closeHuman(ctx workflow.Context, in activities.HumanGateClose) error {
	return workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.CloseHumanGate, in).Get(ctx, nil)
}

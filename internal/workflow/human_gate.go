package workflow

import (
	"cmp"
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/guard"
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

// protectedGateID names the human gate forced by protected changes.
const protectedGateID = "protected-changes"

// guardChanges forces a human gate when the run's change touches protected
// paths or deletes or skips tests (invariant 6), whatever the definition says:
// a workflow author cannot turn it off. It runs once the definition's steps
// have passed, on the last pushed commit, so after any change request opened
// and the person can read the diff there. Runs recorded before it existed end
// without it.
func (it *interpreter) guardChanges(ctx workflow.Context) string {
	if workflow.GetVersion(ctx, "protected-changes", workflow.DefaultVersion, 1) < 1 || it.session.CommitSHA == "" {
		return ""
	}
	patterns := it.in.Snapshot.ProtectedPaths
	if len(patterns) == 0 { // a snapshot taken before protected paths were
		patterns = config.DefaultProtectedPaths
	}
	var report guard.Report
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.DetectProtectedChanges, it.in.Issue, activities.GuardRequest{
		BaseSHA: cmp.Or(it.in.Snapshot.BaseSHA, it.in.Issue.BaseBranch), HeadSHA: it.session.CommitSHA, Patterns: patterns,
	}).Get(ctx, &report)
	switch {
	case err != nil:
		return fmt.Sprintf("step %s: inspecting the change failed: %s", protectedGateID, err)
	case report.Clean():
		return ""
	}
	if why := it.awaitHuman(ctx, protectedGateID, report.Reason(), time.Duration(config.DefaultHumanTimeout), config.OnTimeoutFail); why != "" {
		return fmt.Sprintf("step %s: %s", protectedGateID, why)
	}
	return ""
}

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

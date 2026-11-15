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
	// Action is DecisionRequestChanges to send the change back to the coder
	// rather than reject it, where the gate allows that (D92).
	Action string `json:",omitempty"`
}

// DecisionRequestChanges is the HumanDecision action that sends a change back
// to its coder with the comment as feedback (D92).
const DecisionRequestChanges = "request-changes"

// mergeApprovalGateID names the human gate a project's merge approval opens
// before the merge queue (D92).
const mergeApprovalGateID = "merge-approval"

// humanVerdict is how a person decided.
type humanVerdict int

const (
	verdictApproved humanVerdict = iota
	verdictRejected
	verdictChangesRequested
)

// humanOutcome is a decided human gate.
type humanOutcome struct {
	verdict humanVerdict
	by      string
	comment string
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
	report, why := it.inspectChanges(ctx)
	switch {
	case why != "":
		return why
	case report.Clean():
		return ""
	}
	if why := it.awaitHuman(ctx, protectedGateID, report.Reason(), time.Duration(config.DefaultHumanTimeout), config.OnTimeoutFail); why != "" {
		return fmt.Sprintf("step %s: %s", protectedGateID, why)
	}
	return ""
}

// inspectChanges asks the provider which of the run's changes touch protected
// paths or tests (invariant 6).
func (it *interpreter) inspectChanges(ctx workflow.Context) (guard.Report, string) {
	patterns := it.in.Snapshot.ProtectedPaths
	if len(patterns) == 0 { // a snapshot taken before protected paths were
		patterns = config.DefaultProtectedPaths
	}
	var report guard.Report
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.DetectProtectedChanges, it.in.Issue, activities.GuardRequest{
		BaseSHA: cmp.Or(it.in.Snapshot.BaseSHA, it.in.Issue.BaseBranch), HeadSHA: it.session.CommitSHA, Patterns: patterns,
	}).Get(ctx, &report)
	if err != nil {
		return guard.Report{}, fmt.Sprintf("step %s: inspecting the change failed: %s", protectedGateID, err)
	}
	return report, ""
}

// beforeMerge asks a person before the change request goes to the merge
// queue (D92): the forced protected-changes gate when the change needs it,
// else the project's merge approval when it asks for one. Approving lets the
// run enqueue (proceed); requesting changes loops back to the coder like the
// queue does (next); rejecting closes the change request and fails the run.
func (it *interpreter) beforeMerge(ctx workflow.Context, pc int, s config.Step) (next int, reason string, proceed bool) {
	if it.session.CommitSHA == "" {
		return pc, "", true
	}
	report, why := it.inspectChanges(ctx)
	if why != "" {
		return 0, why, false
	}
	var gate, ask string
	switch {
	case !report.Clean():
		gate, ask = protectedGateID, report.Reason()
	case it.in.Snapshot.MergeApproval:
		gate, ask = mergeApprovalGateID, "The project asks a person to approve each change before it merges. Approve to merge it through the merge queue, "+
			"request changes to send it back to the coder with your comment, or reject to close the change request."
	default:
		return pc, "", true
	}
	out, why := it.awaitDecision(ctx, gate, ask, time.Duration(config.DefaultHumanTimeout), config.OnTimeoutFail, true)
	if why != "" {
		return 0, fmt.Sprintf("step %s: %s", gate, why), false
	}
	decided := "by " + out.by
	if out.comment != "" {
		decided += ": " + out.comment
	}
	switch out.verdict {
	case verdictApproved:
		return pc, "", true
	case verdictChangesRequested:
		feedback := fmt.Sprintf("%s reviewed your change request before it merged and asked for changes:\n\n%s\n\n"+
			"Make the changes on your branch and push; the change is checked and reviewed again before it merges.", out.by, out.comment)
		next, reason := it.loopBack(ctx, s, fmt.Sprintf("step %s: changes requested %s", gate, decided), feedback, false)
		return next, reason, false
	}
	comment := "Rejected in Frankie " + decided
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.CloseChangeRequest, it.in.Issue, it.session.Branch, comment).Get(ctx, nil); err != nil {
		return 0, fmt.Sprintf("step %s: rejected %s (closing the change request failed: %s)", gate, decided, err), false
	}
	return 0, fmt.Sprintf("step %s: rejected %s", gate, decided), false
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
	out, why := it.awaitDecision(ctx, id, reason, timeout, onTimeout, false)
	if why != "" || out.verdict == verdictApproved {
		return why
	}
	why = "rejected by " + out.by
	if out.comment != "" {
		why += ": " + out.comment
	}
	return why
}

// awaitDecision opens a human gate and waits for a person's decision. why is
// set instead when the gate ended without one: canceled, timed out, or an
// Activity failed. A gate that allows it can send the change back to the
// coder (D92); elsewhere a request for changes counts as a rejection.
func (it *interpreter) awaitDecision(ctx workflow.Context, id, reason string, timeout time.Duration, onTimeout config.OnTimeout,
	canRequestChanges bool) (humanOutcome, string) {
	stepRunID, err := it.startStep(ctx, id, domain.StepGate)
	if err != nil {
		return humanOutcome{}, err.Error()
	}
	short := workflow.WithActivityOptions(ctx, shortOptions)
	err = workflow.ExecuteActivity(short, it.a.OpenHumanGate, activities.HumanGateRequest{
		RunID: it.run.RunID, StepRunID: stepRunID, StepID: id, Reason: reason, CommitSHA: it.session.CommitSHA,
		ChangeRequestURL: it.changeRequest, ExpiresAt: workflow.Now(ctx).Add(timeout), CanRequestChanges: canRequestChanges,
	}).Get(ctx, nil)
	if err != nil {
		it.finishStep(ctx, stepRunID, false)
		return humanOutcome{}, "opening the human gate failed: " + err.Error()
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
			return humanOutcome{}, "canceled while waiting for a human decision"
		case decision != nil:
			out := humanOutcome{verdict: verdictRejected, by: decision.By, comment: decision.Comment}
			status := domain.ApprovalRejected
			switch {
			case decision.Approved:
				out.verdict, status = verdictApproved, domain.ApprovalApproved
			case decision.Action == DecisionRequestChanges && canRequestChanges:
				out.verdict, status = verdictChangesRequested, domain.ApprovalChangesRequested
			}
			if err := it.closeHuman(ctx, activities.HumanGateClose{StepRunID: stepRunID, Status: status, By: decision.By, Comment: decision.Comment}); err != nil {
				return humanOutcome{}, "recording the decision failed: " + err.Error()
			}
			return out, ""
		case onTimeout == config.OnTimeoutExtend:
			if err := workflow.ExecuteActivity(short, it.a.ExtendHumanGate, stepRunID, workflow.Now(ctx).Add(timeout)).Get(ctx, nil); err != nil {
				return humanOutcome{}, "extending the human gate failed: " + err.Error()
			}
		default:
			if err := it.closeHuman(ctx, activities.HumanGateClose{StepRunID: stepRunID, Status: domain.ApprovalTimedOut}); err != nil {
				return humanOutcome{}, "recording the timeout failed: " + err.Error()
			}
			return humanOutcome{}, fmt.Sprintf("no human decision within %s", timeout)
		}
	}
}

func (it *interpreter) closeHuman(ctx workflow.Context, in activities.HumanGateClose) error {
	return workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.CloseHumanGate, in).Get(ctx, nil)
}

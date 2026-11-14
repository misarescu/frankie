package workflow

import (
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// RefineInput is the issue to refine and what the refinement executes, fixed
// when it starts (invariant 4): snapshot.ResolveRefinement's refine@1.
type RefineInput struct {
	Issue    activities.IssueRunInput
	Snapshot snapshot.Snapshot
}

// RefineResult is how a refinement ended. Succeeded means a refined spec waits
// on the issue for a person's approval.
type RefineResult struct {
	RunID     string
	Succeeded bool
	Reason    string
}

// Refine runs the refiner on an issue a person asked to start whose spec needs
// approval (D66): the issue moves from draft to refining, the refinement's one
// agent step runs like any agent step of Interpret (a read-only session that
// people can steer or abort, and in which the agent can ask them), and its
// result, a refined spec, is recorded for approval. Anything else returns the
// issue to draft, held with the reason.
func Refine(ctx workflow.Context, in RefineInput) (RefineResult, error) {
	var a *activities.Activities
	var run activities.StartRunResult
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.StartRefinement, in.Issue, in.Snapshot).Get(ctx, &run); err != nil {
		return RefineResult{}, err
	}
	outcome := activities.RefinementOutcome{IssueID: in.Issue.IssueID, RunID: run.RunID}
	switch steps := in.Snapshot.Workflow.Steps; {
	case in.Snapshot.Error != "":
		outcome.Reason = in.Snapshot.Error
	case len(steps) != 1 || steps[0].Agent == nil:
		outcome.Reason = fmt.Sprintf("refinement %s must be one agent step", in.Snapshot.Version())
	default:
		it := &interpreter{in: InterpretInput(in), run: run, attempts: map[string]int{}, loops: map[string]int{}}
		session, stepRunID, reason := it.agentSession(ctx, steps[0])
		if stepRunID != "" {
			it.finishStep(ctx, stepRunID, reason == "")
		}
		outcome.Succeeded, outcome.Reason, outcome.Result = reason == "", reason, session.Result
	}

	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
	var reason string
	if err := workflow.ExecuteActivity(cleanup, a.FinishRefinement, outcome).Get(cleanup, &reason); err != nil {
		return RefineResult{RunID: run.RunID, Reason: outcome.Reason}, err
	}
	return RefineResult{RunID: run.RunID, Succeeded: reason == "", Reason: reason}, nil
}

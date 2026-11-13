package workflow

import (
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/workflow/activities"
)

// IssueRunResult is the run's outcome.
type IssueRunResult struct {
	RunID            string
	Succeeded        bool
	Reason           string
	ChangeRequestURL string
}

// IssueRun is the M1 workflow: one agent step that implements the issue in a
// sandboxed pod, then a change request for the pushed branch.
func IssueRun(ctx workflow.Context, in activities.IssueRunInput) (IssueRunResult, error) {
	var a *activities.Activities
	var run activities.StartRunResult
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.StartRun, in).Get(ctx, &run); err != nil {
		return IssueRunResult{}, err
	}
	out := IssueRunResult{RunID: run.RunID}

	// The agent runs in the repo's toolchain image (M2, D30). Runs started before
	// this version existed replay without it.
	var err error
	if workflow.GetVersion(ctx, "toolchain-image", workflow.DefaultVersion, 1) >= 1 {
		if err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, toolchainOptions), a.EnsureToolchainImage, in).Get(ctx, &run.ToolchainImage); err != nil {
			err = fmt.Errorf("toolchain image: %w", err)
		}
	}

	var pod activities.AgentPod
	if err == nil {
		err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, launchOptions), a.LaunchAgentPod, in, run).Get(ctx, &pod)
	}
	var session activities.SessionResult
	if err == nil {
		err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, sessionOptions), a.RunAgentSession, in, run, pod).Get(ctx, &session)
	}

	// Cleanup and bookkeeping run even if the workflow was canceled.
	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
	if pod.Name != "" {
		_ = workflow.ExecuteActivity(cleanup, a.DeleteAgentPod, pod.Name).Get(cleanup, nil)
	}

	switch {
	case err != nil:
		out.Reason = "agent step failed: " + err.Error()
	case session.Status != "succeeded":
		out.Reason = "agent session " + session.Status + ": " + session.Reason
	case !session.Pushed:
		out.Reason = "agent session made no changes"
	default:
		var cr string
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.OpenChangeRequest, in, run, session).Get(ctx, &cr); err != nil {
			out.Reason = "opening the change request failed: " + err.Error()
		} else {
			out.Succeeded, out.ChangeRequestURL = true, cr
		}
	}

	finish := activities.FinishInput{IssueID: in.IssueID, RunID: run.RunID, StepRunID: run.StepRunID, Succeeded: out.Succeeded, Reason: out.Reason}
	if err := workflow.ExecuteActivity(cleanup, a.FinishRun, finish).Get(cleanup, nil); err != nil {
		return out, err
	}
	return out, nil
}

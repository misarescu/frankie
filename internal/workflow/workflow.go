// Package workflow holds the control plane's Temporal workflows and Activities
// (docs/spec/03-workflows-and-gates.md). M1 runs one hardcoded workflow per
// issue; the generic interpreter of workflow definitions replaces it in M3.
//
// Workflow code is deterministic (invariant 3): it only calls Activities.
package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TaskQueue is where the control plane's worker polls.
const TaskQueue = "frankie"

// IssueRunInput is everything the run needs, snapshotted at start (invariant 4).
type IssueRunInput struct {
	IssueID            string
	IssueKey           string // e.g. gc-1
	Title              string
	Spec               string
	AcceptanceCriteria []string
	SourceURL          string // the GitHub issue
	SourceNumber       int
	Repo               string // factory repo, also the GitHub owner/name in M1
	BaseBranch         string
}

// IssueRunResult is the run's outcome.
type IssueRunResult struct {
	RunID            string
	Succeeded        bool
	Reason           string
	ChangeRequestURL string
}

// StartRunResult identifies the run and its single M1 step.
type StartRunResult struct {
	RunID     string
	StepRunID string
	Branch    string
}

// AgentPod is a launched agent pod.
type AgentPod struct {
	Name string
	IP   string
}

// SessionResult is the runner's done event, as the workflow needs it.
type SessionResult struct {
	Status    string // succeeded | failed | aborted | timeout
	Reason    string
	Result    string // the agent's final message
	Branch    string
	CommitSHA string
	Pushed    bool
}

// FinishInput records a run's outcome.
type FinishInput struct {
	IssueID   string
	RunID     string
	StepRunID string
	Succeeded bool
	Reason    string
}

// Activity options. The agent session is long and heartbeats so a restarted
// control plane resumes the event stream instead of starting over.
var (
	shortOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	}
	// Launching heartbeats while it waits for the pod, so a worker that dies
	// mid-launch is noticed in seconds rather than at the 10-minute timeout.
	launchOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	sessionOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Hour,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
)

// IssueRun is the M1 workflow: one agent step that implements the issue in a
// sandboxed pod, then a change request for the pushed branch.
func IssueRun(ctx workflow.Context, in IssueRunInput) (IssueRunResult, error) {
	var a *Activities
	var run StartRunResult
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.StartRun, in).Get(ctx, &run); err != nil {
		return IssueRunResult{}, err
	}
	out := IssueRunResult{RunID: run.RunID}

	var pod AgentPod
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, launchOptions), a.LaunchAgentPod, in, run).Get(ctx, &pod)
	var session SessionResult
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

	finish := FinishInput{IssueID: in.IssueID, RunID: run.RunID, StepRunID: run.StepRunID, Succeeded: out.Succeeded, Reason: out.Reason}
	if err := workflow.ExecuteActivity(cleanup, a.FinishRun, finish).Get(cleanup, nil); err != nil {
		return out, err
	}
	return out, nil
}

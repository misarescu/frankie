package workflow

import (
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/workflow/activities"
)

// sessionGrace is how much longer than its own timeout a release agent
// session's Activity may run, as for agent steps.
const sessionGrace = 30 * time.Minute

// releaseAgentSession runs one session of a release's agent (D129): a step of
// the release's run, in a sandbox pod like a reviewer's. It returns the
// agent's final message, or why the session failed.
func releaseAgentSession(ctx workflow.Context, req activities.ReleaseAgentRequest) (result, failed string) {
	var a *activities.Activities
	var s activities.ReleaseSession
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.PrepareReleaseSession, req).Get(ctx, &s); err != nil {
		return "", "preparing the session failed: " + err.Error()
	}
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, toolchainOptions), a.EnsureStepImage, s.Issue, s.Run.RunID, s.Run.StepRunID).Get(ctx, &s.Run.ToolchainImage)
	var pod activities.AgentPod
	if err == nil {
		err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, launchOptions), a.LaunchAgentPod, s.Issue, s.Run).Get(ctx, &pod)
	}
	var session activities.SessionResult
	if err == nil {
		opts := sessionOptions
		if s.Run.Step != nil && s.Run.Step.Timeout > 0 {
			opts.StartToCloseTimeout = s.Run.Step.Timeout + sessionGrace
		}
		err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), a.RunAgentSession, s.Issue, s.Run, pod).Get(ctx, &session)
	}
	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
	if pod.Name != "" {
		_ = workflow.ExecuteActivity(cleanup, a.DeleteAgentPod, pod.Name).Get(cleanup, nil)
	}
	switch {
	case err != nil:
		failed = "agent step failed: " + err.Error()
	case session.Status != "succeeded":
		failed = "agent session " + session.Status + ": " + session.Reason
	}
	_ = workflow.ExecuteActivity(cleanup, a.FinishStep, activities.StepEnd{StepRunID: s.Run.StepRunID, Succeeded: failed == ""}).Get(cleanup, nil)
	return session.Result, failed
}

// prepareNotes runs the notes session and records its notes, or the fallback.
func prepareNotes(ctx workflow.Context, req activities.ReleaseAgentRequest) {
	var a *activities.Activities
	result, failed := releaseAgentSession(ctx, req)
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.RecordReleaseNotes,
		activities.ReleaseNotesRequest{ReleaseID: req.ReleaseID, Result: result, Failed: failed}).Get(ctx, nil)
	if err != nil {
		// PublishVersion falls back to notes of its own.
		workflow.GetLogger(ctx).Error("recording the release notes failed", "release", req.ReleaseID, "error", err)
	}
}

// publishVersion gives a succeeded release its version and publishes it; a
// failure is logged, the release has passed its stages.
func publishVersion(ctx workflow.Context, releaseID string) {
	var a *activities.Activities
	var out activities.PublishedVersion
	log := workflow.GetLogger(ctx)
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.PublishVersion, releaseID).Get(ctx, &out); err != nil {
		log.Error("publishing the release's version failed", "release", releaseID, "error", err)
		return
	}
	log.Info("published the release's version", "release", releaseID, "version", out.Version, "url", out.URL, "error", out.Error)
}

// finishReleaseRun ends the release's run, even while canceling.
func finishReleaseRun(ctx workflow.Context, releaseID string) {
	var a *activities.Activities
	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
	if err := workflow.ExecuteActivity(cleanup, a.FinishReleaseRun, releaseID).Get(cleanup, nil); err != nil {
		workflow.GetLogger(ctx).Error("ending the release's run failed", "release", releaseID, "error", err)
	}
}

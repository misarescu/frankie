package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// The release workflow's types live with the Activities (activities cannot
// import this package); these names keep the workflow's code short.
const ReleaseRequestSignal = activities.ReleaseRequestSignal

type (
	ReleaseInput   = activities.ReleaseInput
	ReleaseRequest = activities.ReleaseRequest
)

// ReleaseWorkflowID is the ID of repo's release workflow.
func ReleaseWorkflowID(repo string) string { return activities.ReleaseWorkflowID(repo) }

// maxReleasesPerRun bounds a release workflow's history: it continues as new,
// carrying what is pending, after this many releases.
const maxReleasesPerRun = 20

// awaitOptions cover waiting for a stage to run a release (Release.StageTimeout,
// 15 minutes by default, plus room), heartbeating at every poll.
var awaitOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 30 * time.Minute,
	HeartbeatTimeout:    2 * time.Minute,
	RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
}

// verifyOptions cover a stage's verification, e.g. an end-to-end suite.
var verifyOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 60 * time.Minute,
	RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
}

// Release is a repo's release workflow (D114): merges ask it to release their
// commit (signal-with-start from the merge queue), and it releases one commit
// at a time. Requests that arrive while a release runs are batched: the next
// release is of the newest commit asked for, which contains the others (D110).
// Each release moves through the project's release stages in order, as
// configured at its commit: publish, wait until the stage runs it, verify. A
// stage that fails stops the release, rolled back when the stage allows it
// (FR-RL-2); a release that passes every stage releases its issues. The
// workflow ends when nothing is pending; the next merge starts it again.
func Release(ctx workflow.Context, in ReleaseInput) error {
	pending := in.Pending
	ch := workflow.GetSignalChannel(ctx, ReleaseRequestSignal)
	for done := 0; ; done++ {
		for {
			var r ReleaseRequest
			if !ch.ReceiveAsync(&r) {
				break
			}
			if r.SHA != "" {
				pending = &r
			}
		}
		if pending == nil {
			return nil
		}
		if done == maxReleasesPerRun {
			return workflow.NewContinueAsNewError(ctx, Release, ReleaseInput{Repo: in.Repo, Pending: pending})
		}
		req := *pending
		pending = nil
		runRelease(ctx, in.Repo, req.SHA)
	}
}

// runRelease prepares and runs the release of one commit. Its outcome is in the
// store; the workflow goes on to the next request whatever happened.
func runRelease(ctx workflow.Context, repo, sha string) {
	var a *activities.Activities
	log := workflow.GetLogger(ctx)
	short := workflow.WithActivityOptions(ctx, shortOptions)
	var plan activities.ReleasePlan
	if err := workflow.ExecuteActivity(short, a.PrepareRelease, activities.PrepareReleaseRequest{Repo: repo, SHA: sha}).Get(ctx, &plan); err != nil {
		// Nothing was released; the next merge asks again.
		log.Error("preparing the release failed", "sha", sha, "error", err)
		return
	}
	switch {
	case plan.Skip != "":
		log.Info("nothing to release", "sha", sha, "reason", plan.Skip)
		return
	case plan.Failed != "":
		log.Info("release failed before its stages", "release", plan.ReleaseID, "reason", plan.Failed)
		return
	}
	for _, st := range plan.Stages {
		status, reason := deployStage(ctx, activities.StageRequest{ReleaseID: plan.ReleaseID, Repo: repo, SHA: sha, Stage: st})
		if status != domain.ReleaseSucceeded {
			finishRelease(ctx, plan.ReleaseID, status, reason)
			return
		}
	}
	finishRelease(ctx, plan.ReleaseID, domain.ReleaseSucceeded, "")
}

// deployStage deploys the release to one stage: succeeded, or failed or
// rolled back with the reason.
func deployStage(ctx workflow.Context, req activities.StageRequest) (domain.ReleaseStatus, string) {
	var a *activities.Activities
	short := workflow.WithActivityOptions(ctx, shortOptions)
	if err := workflow.ExecuteActivity(short, a.StartDeployment, req).Get(ctx, &req.DeploymentID); err != nil {
		return domain.ReleaseFailed, fmt.Sprintf("%s: recording the deployment failed: %v", req.Stage.Name, err)
	}
	reason := ""
	if err := workflow.ExecuteActivity(short, a.PublishStage, req).Get(ctx, nil); err != nil {
		reason = "publishing failed: " + err.Error()
	}
	if reason == "" {
		var st activities.StageStatus
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, awaitOptions), a.AwaitStage, req).Get(ctx, &st); err != nil {
			reason = "waiting for the stage failed: " + err.Error()
		} else if !st.Ready {
			reason = st.Reason
		}
	}
	if reason == "" {
		var v activities.Verification
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, verifyOptions), a.VerifyStage, req).Get(ctx, &v); err != nil {
			reason = "verification could not run: " + err.Error()
		} else if !v.Passed {
			reason = "verification failed: " + v.Summary
		}
	}
	if reason == "" {
		if err := workflow.ExecuteActivity(short, a.EndDeployment, activities.EndDeploymentRequest{DeploymentID: req.DeploymentID, Succeeded: true}).Get(ctx, nil); err != nil {
			return domain.ReleaseFailed, fmt.Sprintf("%s: recording the deployment failed: %v", req.Stage.Name, err)
		}
		return domain.ReleaseSucceeded, ""
	}

	reason = req.Stage.Name + ": " + reason
	_ = workflow.ExecuteActivity(short, a.EndDeployment, activities.EndDeploymentRequest{DeploymentID: req.DeploymentID, Reason: reason}).Get(ctx, nil)
	if !req.Stage.Rollback {
		return domain.ReleaseFailed, reason
	}
	if err := workflow.ExecuteActivity(short, a.RollbackStage, req).Get(ctx, nil); err != nil {
		return domain.ReleaseFailed, reason + "; rolling back failed: " + err.Error()
	}
	return domain.ReleaseRolledBack, reason
}

// finishRelease records the release's outcome, even while canceling.
func finishRelease(ctx workflow.Context, releaseID string, status domain.ReleaseStatus, reason string) {
	var a *activities.Activities
	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
	if err := workflow.ExecuteActivity(cleanup, a.FinishRelease, activities.FinishReleaseRequest{ReleaseID: releaseID, Status: status, Reason: reason}).Get(cleanup, nil); err != nil {
		workflow.GetLogger(ctx).Error("recording the release's outcome failed", "release", releaseID, "error", err)
	}
}

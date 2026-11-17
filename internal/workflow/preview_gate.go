package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// previewOptions cover publishing a preview, which waits up to 30 minutes
// for room (Release.RoomTimeout), heartbeating at every attempt.
var previewOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 40 * time.Minute,
	HeartbeatTimeout:    2 * time.Minute,
	RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
}

// removeOptions cover taking a preview down: one commit, retried for a while,
// since a preview left behind holds a slot until the driver drops it.
var removeOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 2 * time.Minute,
	RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 10},
}

// previewGate checks the run's change request in a preview of its head
// (D126): it waits for the head's provider checks as the merge queue does,
// since the provider's CI publishes the image the preview runs; then deploys
// the head to the project's preview stage, waits until it runs it, verifies
// it there and removes it. Failed CI or failing tests go back to the coder
// through the gate's onFail; a check that could not judge the head (no room,
// the preview never ran it, the suite could not run) escalates to a person
// (D127). Without a preview stage the gate does not apply.
func (it *interpreter) previewGate(ctx workflow.Context, pc int, s config.Step) (int, string) {
	stage := it.in.Snapshot.PreviewStage
	if stage == nil {
		return pc + 1, ""
	}
	if workflow.GetVersion(ctx, "preview-gate", workflow.DefaultVersion, 1) < 1 {
		return 0, "this run cannot check its change request in a preview"
	}
	if it.changeRequest == "" {
		return 0, "no change request was opened to check in a preview"
	}
	head := it.session.CommitSHA

	ciID, err := it.startStep(ctx, s.ID+"-ci", domain.StepGate)
	if err != nil {
		return 0, err.Error()
	}
	var ci activities.GateOutcome
	err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, checksOptions), it.a.AwaitProviderChecks, activities.ProviderChecksRequest{
		Issue: it.in.Issue, StepRunID: ciID, HeadSHA: head, Logs: it.in.Snapshot.CILogs,
	}).Get(ctx, &ci)
	if err != nil {
		it.finishStep(ctx, ciID, false)
		return 0, "waiting for the repo's CI failed: " + err.Error()
	}
	if !ci.Passed {
		return it.previewJudge(ctx, pc, s, ci)
	}

	stepRunID, err := it.startStep(ctx, s.ID, domain.StepGate)
	if err != nil {
		return 0, err.Error()
	}
	req := activities.PreviewRequest{Issue: it.in.Issue, RunID: it.run.RunID, StepRunID: stepRunID, Branch: it.session.Branch,
		HeadSHA: head, ChangeRequestURL: it.changeRequest, Stage: *stage}
	d := it.checkPreview(ctx, &req)
	var outcome activities.GateOutcome
	err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.FinishPreview,
		activities.PreviewResult{StepRunID: stepRunID, Details: d}).Get(ctx, &outcome)
	if err != nil {
		it.finishStep(ctx, stepRunID, false)
		return 0, "recording the preview check failed: " + err.Error()
	}
	return it.previewJudge(ctx, pc, s, outcome)
}

// previewJudge escalates a blocking check that could not judge the head,
// since sending the coder back would not help; anything else is judged as
// any gate is.
func (it *interpreter) previewJudge(ctx workflow.Context, pc int, s config.Step, outcome activities.GateOutcome) (int, string) {
	if !outcome.Passed && outcome.Inconclusive && s.Gate.Policy != domain.GateAdvisory {
		return it.escalate(ctx, pc, s, outcome.Summary)
	}
	return it.judge(ctx, pc, s, outcome)
}

// checkPreview deploys the head as a preview, waits until it runs it,
// verifies it and removes it, and says what it found. Any step that cannot
// complete makes the check inconclusive; the preview is removed whenever it
// was published, even when the run is canceled meanwhile.
func (it *interpreter) checkPreview(ctx workflow.Context, req *activities.PreviewRequest) domain.PreviewDetails {
	d := domain.PreviewDetails{Stage: req.Stage.Name, SHA: req.HeadSHA}
	var pub activities.PreviewPublished
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, previewOptions), it.a.PublishPreview, *req).Get(ctx, &pub); err != nil {
		d.Summary, d.Inconclusive = "deploying the preview failed: "+err.Error(), true
		return d
	}
	req.ChangeRequest, req.State = pub.ChangeRequest, pub.State
	d.ChangeRequest, d.URL = pub.ChangeRequest, req.URL()
	if pub.NoRoom != "" {
		d.Summary, d.Inconclusive = pub.NoRoom, true
		return d
	}

	var st activities.StageStatus
	switch err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, awaitOptions), it.a.AwaitPreview, *req).Get(ctx, &st); {
	case err != nil:
		d.Summary, d.Inconclusive = "waiting for the preview failed: "+err.Error(), true
	case !st.Ready:
		d.Summary, d.Inconclusive = st.Reason, true
	default:
		d.Ran = true
		var v activities.Verification
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, verifyOptions), it.a.VerifyPreview, *req).Get(ctx, &v); err != nil {
			d.Summary, d.Inconclusive = "verifying the preview failed: "+err.Error(), true
			break
		}
		d.Passed, d.Summary, d.Inconclusive = v.Passed, v.Summary, !v.Passed && v.Inconclusive
		d.Failures = activities.PreviewFailures(v.Failures)
	}

	remove, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(remove, removeOptions), it.a.RemovePreview, *req).Get(remove, nil); err != nil {
		d.RemoveError = err.Error()
	} else {
		d.Removed = true
	}
	return d
}

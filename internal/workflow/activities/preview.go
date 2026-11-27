package activities

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/store"
)

// PreviewRequest is one check of a change request's head in a preview of it
// (D126): one execution of a preview gate StepRun, whose ID is also the
// preview deployment's. The deployment's driver state stays in the workflow
// (invariant 8): State is what PublishPreview returned.
type PreviewRequest struct {
	Issue     IssueRunInput
	RunID     string
	StepRunID string
	Branch    string // the issue's agent branch, which holds HeadSHA
	HeadSHA   string
	// ChangeRequestURL is the run's change request; PublishPreview finds its
	// number, which the later calls get as ChangeRequest.
	ChangeRequestURL string
	ChangeRequest    int
	Stage            config.Stage
	State            json.RawMessage `json:",omitempty"`
}

// URL is where the preview serves the change request.
func (r PreviewRequest) URL() string { return r.Stage.URLFor(r.ChangeRequest) }

// PreviewPublished is what PublishPreview did: the change request's number,
// and the driver's state or why no preview could start in time.
type PreviewPublished struct {
	ChangeRequest int
	State         json.RawMessage `json:",omitempty"`
	NoRoom        string          `json:",omitempty"`
}

// changeRequestNumber finds the number of the issue's change request at url.
func (a *Activities) changeRequestNumber(ctx context.Context, in IssueRunInput, url string) (int, error) {
	issueID, err := uuid.Parse(in.IssueID)
	if err != nil {
		return 0, temporal.NewNonRetryableApplicationError("bad issue id", "input", err)
	}
	crs, err := a.Store.ListChangeRequestsForIssue(ctx, issueID)
	if err != nil {
		return 0, err
	}
	for _, cr := range crs {
		if cr.ProviderRef == url {
			return cr.Number, nil
		}
	}
	return 0, temporal.NewNonRetryableApplicationError("issue "+in.IssueKey+" has no change request "+url, "input", nil)
}

// previewCall resolves the preview stage's driver and the request it gets.
func (a *Activities) previewCall(req PreviewRequest) (drivers.Driver, drivers.Request, error) {
	depID, err := uuid.Parse(req.StepRunID)
	if err != nil {
		return nil, drivers.Request{}, temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	d, err := a.driverRegistry().Get(req.Stage.Driver)
	if err != nil {
		return nil, drivers.Request{}, temporal.NewNonRetryableApplicationError(err.Error(), "config", err)
	}
	state := req.State
	if len(state) == 0 {
		state = drivers.EmptyState
	}
	return d, drivers.Request{
		Stage:      drivers.Stage{Name: req.Stage.Name, Config: req.Stage.Config},
		Subject:    drivers.Subject{Repo: req.Issue.Repo, SHA: req.HeadSHA, ChangeRequest: req.ChangeRequest},
		Deployment: drivers.Deployment{ID: depID, State: state},
	}, nil
}

// PublishPreview starts the preview gate's StepRun, finds the change
// request's number, and asks the preview stage's driver to deploy the head. While the stage has no room (another
// change request's preview runs), it asks again every Release.PollInterval
// for up to Release.RoomTimeout, then reports NoRoom. A retry publishes again,
// which drivers take as the same change.
func (a *Activities) PublishPreview(ctx context.Context, req PreviewRequest) (PreviewPublished, error) {
	if req.ChangeRequest == 0 {
		n, err := a.changeRequestNumber(ctx, req.Issue, req.ChangeRequestURL)
		if err != nil {
			return PreviewPublished{}, err
		}
		req.ChangeRequest = n
	}
	d, r, err := a.previewCall(req)
	if err != nil {
		return PreviewPublished{}, err
	}
	id := r.Deployment.ID
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil {
		return PreviewPublished{}, err
	}
	if _, err := a.Store.SetStepRunCommit(ctx, id, req.HeadSHA); err != nil {
		return PreviewPublished{}, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecPending, domain.ExecRunning); err != nil && step.Status != domain.ExecRunning {
		return PreviewPublished{}, err
	}
	timeout := a.Release.roomTimeout()
	deadline := time.Now().Add(timeout)
	for {
		state, err := d.Publish(ctx, r)
		switch {
		case err == nil:
			return PreviewPublished{ChangeRequest: req.ChangeRequest, State: state}, nil
		case !errors.Is(err, drivers.ErrNoRoom):
			return PreviewPublished{}, driverError("preview on "+req.Stage.Name, err)
		case time.Now().After(deadline):
			return PreviewPublished{ChangeRequest: req.ChangeRequest, NoRoom: fmt.Sprintf("no room for a preview on %s within %s: %s", req.Stage.Name, timeout, err)}, nil
		}
		activity.RecordHeartbeat(ctx, err.Error())
		select {
		case <-ctx.Done():
			return PreviewPublished{}, ctx.Err()
		case <-time.After(a.Release.pollInterval()):
		}
	}
}

// AwaitPreview waits until the preview runs the head, its driver reports it
// failed, or the stage's readyTimeout passes (Release.PreviewTimeout when it
// sets none, D131).
func (a *Activities) AwaitPreview(ctx context.Context, req PreviewRequest) (StageStatus, error) {
	d, r, err := a.previewCall(req)
	if err != nil {
		return StageStatus{}, err
	}
	return a.awaitDriver(ctx, d, r, req.Stage.Name, req.HeadSHA, cmp.Or(req.Stage.ReadyWithin(), a.Release.previewTimeout()))
}

// VerifyPreview runs the preview stage's verification against the preview: the
// head checked out from its agent branch, the suite pointed at the
// preview's URL (D126). A stage without verify passes.
func (a *Activities) VerifyPreview(ctx context.Context, req PreviewRequest) (Verification, error) {
	if req.Stage.Verify == "" {
		return Verification{Passed: true, Summary: "the preview runs the head; the stage verifies nothing more"}, nil
	}
	if a.Verifier == nil {
		return Verification{}, temporal.NewNonRetryableApplicationError("no verifier for "+req.Stage.Verify, "config", nil)
	}
	v, err := a.Verifier.Verify(ctx, VerifyRequest{DeploymentID: req.StepRunID, Repo: req.Issue.Repo, BaseBranch: req.Issue.BaseBranch,
		SHA: req.HeadSHA, Stage: req.Stage.Name, Kind: req.Stage.Verify, URL: req.URL(),
		Branch: req.Branch, RunID: req.RunID, IssueKey: req.Issue.IssueKey})
	if err != nil {
		return Verification{}, fmt.Errorf("verify the preview on %s: %w", req.Stage.Name, err)
	}
	return v, nil
}

// RemovePreview takes the preview down by rolling its deployment back, which
// removes it (D126, D137).
// Nothing to do before PublishPreview returned a state.
func (a *Activities) RemovePreview(ctx context.Context, req PreviewRequest) error {
	if len(req.State) == 0 {
		return nil
	}
	d, r, err := a.previewCall(req)
	if err != nil {
		return err
	}
	if _, err := d.Rollback(ctx, r); err != nil {
		return driverError("remove the preview on "+req.Stage.Name, err)
	}
	return nil
}

// PreviewResult is what the preview gate found, for FinishPreview.
type PreviewResult struct {
	StepRunID string
	Details   domain.PreviewDetails
}

// maxPreviewFailures caps the failed tests a coder is told about.
const maxPreviewFailures = 20

// FinishPreview records the preview gate's result on its StepRun and tells
// the workflow, and the coder a loop sends back, what failed. A retry of a
// finished StepRun reports what was recorded.
func (a *Activities) FinishPreview(ctx context.Context, res PreviewResult) (GateOutcome, error) {
	id, err := uuid.Parse(res.StepRunID)
	if err != nil {
		return GateOutcome{}, temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil {
		return GateOutcome{}, err
	}
	if step.Status.Terminal() {
		return a.recordedPreview(ctx, step)
	}
	if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecPending, domain.ExecRunning); err != nil && step.Status != domain.ExecRunning {
		return GateOutcome{}, err
	}
	d := res.Details
	verdict, status := domain.GateFail, domain.ExecFailed
	if d.Passed {
		verdict, status = domain.GatePass, domain.ExecSucceeded
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.RecordGateResult(ctx, store.NewGateResult{StepRunID: id, Type: domain.GatePreview, Policy: domain.GateBlocking,
		Verdict: verdict, Details: raw}); err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecRunning, status); err != nil {
		return GateOutcome{}, err
	}
	return PreviewOutcome(id.String(), d), nil
}

func (a *Activities) recordedPreview(ctx context.Context, step domain.StepRun) (GateOutcome, error) {
	results, err := a.Store.ListGateResultsForRun(ctx, step.RunID)
	if err != nil {
		return GateOutcome{}, err
	}
	for _, r := range results {
		if r.StepRunID != step.ID {
			continue
		}
		var d domain.PreviewDetails
		if err := json.Unmarshal(r.Details, &d); err != nil {
			return GateOutcome{}, fmt.Errorf("decode preview details: %w", err)
		}
		return PreviewOutcome(step.ID.String(), d), nil
	}
	return GateOutcome{}, fmt.Errorf("preview gate %s is %s but has no result", step.ID, step.Status)
}

// PreviewOutcome says what the preview found. Failing tests go back to the
// coder; a check that could not judge the head is inconclusive, which no loop
// back to the coder would help (D126).
func PreviewOutcome(stepRunID string, d domain.PreviewDetails) GateOutcome {
	out := GateOutcome{StepRunID: stepRunID, Passed: d.Passed, Summary: d.Summary, Inconclusive: !d.Passed && d.Inconclusive}
	if d.Passed || d.Inconclusive {
		return out
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Your change request's head failed the end-to-end suite in a preview: the image the repo's CI built for it, "+
		"deployed at %s like the project's stages. %s\n", d.URL, d.Summary)
	for i, f := range d.Failures {
		if i == maxPreviewFailures {
			fmt.Fprintf(&b, "- and %d more\n", len(d.Failures)-i)
			break
		}
		fmt.Fprintf(&b, "- %s", f.Test)
		if f.Message != "" {
			fmt.Fprintf(&b, ": %s", f.Message)
		}
		b.WriteString("\n")
	}
	b.WriteString("Fix the code so these pass against the deployed image.\n")
	out.Feedback = b.String()
	return out
}

// PreviewFailures converts a verification's failures for PreviewDetails.
func PreviewFailures(fs []TestFailure) []domain.TestFailure {
	out := make([]domain.TestFailure, 0, len(fs))
	for _, f := range fs {
		out = append(out, domain.TestFailure{Test: f.Test, IssueKeys: f.IssueKeys, Message: f.Message})
	}
	return out
}

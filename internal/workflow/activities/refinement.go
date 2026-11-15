package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/store"
)

// StartRefinement creates the refinement's run with its snapshot (invariant 4)
// and moves the issue from draft to refining (D66). Idempotent per Temporal
// execution, like StartWorkflowRun.
func (a *Activities) StartRefinement(ctx context.Context, in IssueRunInput, snap snapshot.Snapshot) (StartRunResult, error) {
	return a.startSnapshotRun(ctx, in, snap, domain.IssueDraft, domain.IssueRefining)
}

// RefinementOutcome is how a refinement's session ended.
type RefinementOutcome struct {
	IssueID   string
	RunID     string
	Succeeded bool   // the session ended normally
	Reason    string // why it did not
	Result    string // the refiner's final message: a refined spec
}

// FinishRefinement records a refinement (D66): a valid refined spec waits on the
// issue for a person's approval; anything else (a failed session or a result
// outside the contract) fails the run and returns the issue to draft, held with
// the reason. A refinement that failed before its agent started (its image
// build or pod, D79) is not held, so the scheduler starts it again, until
// maxEarlyRefinementFailures in a row have. It returns the reason, or "" when
// the spec waits for approval.
func (a *Activities) FinishRefinement(ctx context.Context, out RefinementOutcome) (string, error) {
	issueID, err1 := uuid.Parse(out.IssueID)
	runID, err2 := uuid.Parse(out.RunID)
	if err := errors.Join(err1, err2); err != nil {
		return "", fmt.Errorf("finish refinement: %w", err)
	}
	reason := out.Reason
	if out.Succeeded {
		spec, err := domain.ParseRefinedSpec([]byte(out.Result))
		if err == nil {
			return "", a.Store.RecordRefinement(ctx, issueID, runID, spec)
		}
		reason = "the refiner's result is not a refined spec: " + err.Error()
	}
	if reason == "" {
		reason = "the refinement did not finish"
	}
	hold := true
	if !out.Succeeded {
		early, err := a.earlyRefinementFailures(ctx, issueID, runID)
		if err != nil {
			return "", err
		}
		switch {
		case early == 0:
		case early < maxEarlyRefinementFailures:
			hold = false
		default:
			reason = fmt.Sprintf("%s (%d refinements in a row failed before the refiner started)", reason, early)
		}
	}
	a.Log.Warn("refinement failed", "run", out.RunID, "reason", reason, "held", hold)
	// The transcript must say why the refinement ended, not only that it did.
	message := "refinement failed"
	if !hold {
		message = "refinement failed before the refiner started; it starts again"
	}
	payload, _ := json.Marshal(map[string]string{"message": message, "detail": reason})
	if _, err := a.Store.AppendEvent(ctx, store.NewEvent{RunID: &runID, Type: domain.EventError, Payload: payload}); err != nil {
		return "", err
	}
	return reason, a.Store.FailRefinement(ctx, issueID, runID, reason, hold)
}

// maxEarlyRefinementFailures is how many refinements of an issue in a row may
// fail before their agent starts until the draft is held (D79): an image that
// cannot build would otherwise rebuild on every scheduler pass.
const maxEarlyRefinementFailures = 3

// earlyRefinementFailures counts the refinements of the issue, from run
// backwards, that failed before their agent started: 0 when run's agent did.
func (a *Activities) earlyRefinementFailures(ctx context.Context, issueID, runID uuid.UUID) (int, error) {
	started, err := a.agentStarted(ctx, runID)
	if err != nil || started {
		return 0, err
	}
	runs, err := a.Store.ListIssueRuns(ctx, issueID)
	if err != nil {
		return 0, err
	}
	n := 1
	for _, r := range runs { // newest first
		if r.ID == runID {
			continue
		}
		if r.Status != domain.ExecFailed || !strings.HasPrefix(r.WorkflowVersion, snapshot.RefinementName+"@") {
			break
		}
		started, err := a.agentStarted(ctx, r.ID)
		if err != nil {
			return 0, err
		}
		if started {
			break
		}
		n++
	}
	return n, nil
}

// agentStarted reports whether any agent of the run got a running pod.
func (a *Activities) agentStarted(ctx context.Context, runID uuid.UUID) (bool, error) {
	steps, err := a.Store.ListStepRuns(ctx, runID)
	if err != nil {
		return false, err
	}
	for _, s := range steps {
		if s.Kind == domain.StepAgent && s.PodName != "" {
			return true, nil
		}
	}
	return false, nil
}

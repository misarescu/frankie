package activities

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// humanGate opens a human gate on a fresh Interpret run.
func humanGate(t *testing.T) (*Activities, HumanGateRequest) {
	t.Helper()
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)
	stepRunID := startStep(t, a, StepStart{RunID: run.RunID, StepID: "approve", Kind: domain.StepGate, Attempt: 1})
	req := HumanGateRequest{RunID: run.RunID, StepRunID: stepRunID, StepID: "approve", Reason: "protected paths changed",
		CommitSHA: reviewedSHA, ChangeRequestURL: "https://github.com/o/r/pull/3", ExpiresAt: time.Now().Add(time.Hour)}
	env := activityEnv(a)
	for range 2 { // retried
		if _, err := env.ExecuteActivity(a.OpenHumanGate, req); err != nil {
			t.Fatalf("OpenHumanGate: %v", err)
		}
	}
	return a, req
}

func TestOpenHumanGate(t *testing.T) {
	a, req := humanGate(t)
	id := uuid.MustParse(req.StepRunID)
	step, _ := a.Store.GetStepRun(t.Context(), id)
	if step.Status != domain.ExecRunning || step.CommitSHA != reviewedSHA {
		t.Errorf("step = %+v", step)
	}
	pending, err := a.Store.ListPendingApprovals(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Reason != req.Reason || pending[0].ChangeRequestURL != req.ChangeRequestURL {
		t.Fatalf("inbox = %+v, %v", pending, err)
	}
}

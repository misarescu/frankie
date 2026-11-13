package activities

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

func TestCloseHumanGate(t *testing.T) {
	tests := []struct {
		status  domain.ApprovalStatus
		verdict domain.GateVerdict
		step    domain.ExecStatus
	}{
		{domain.ApprovalApproved, domain.GatePass, domain.ExecSucceeded},
		{domain.ApprovalRejected, domain.GateFail, domain.ExecFailed},
		{domain.ApprovalTimedOut, domain.GateFail, domain.ExecFailed},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			a, req := humanGate(t)
			in := HumanGateClose{StepRunID: req.StepRunID, Status: tt.status, By: "web", Comment: "checked the diff"}
			env := activityEnv(a)
			for range 2 { // retried
				if _, err := env.ExecuteActivity(a.CloseHumanGate, in); err != nil {
					t.Fatalf("CloseHumanGate: %v", err)
				}
			}
			id := uuid.MustParse(req.StepRunID)
			step, _ := a.Store.GetStepRun(t.Context(), id)
			results, _ := a.Store.ListGateResultsForRun(t.Context(), step.RunID)
			if step.Status != tt.step || len(results) != 1 || results[0].Type != domain.GateHuman || results[0].Verdict != tt.verdict {
				t.Fatalf("step %s, results %+v", step.Status, results)
			}
			var d HumanGateDetails
			if err := json.Unmarshal(results[0].Details, &d); err != nil || d.Status != tt.status || d.By != "web" || d.Reason != req.Reason {
				t.Errorf("details = %s", results[0].Details)
			}
		})
	}
}

// The first decision stands: closing again with another one records the first.
func TestCloseHumanGateKeepsTheFirstDecision(t *testing.T) {
	a, req := humanGate(t)
	env := activityEnv(a)
	if _, err := env.ExecuteActivity(a.CloseHumanGate, HumanGateClose{StepRunID: req.StepRunID, Status: domain.ApprovalRejected, By: "web"}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ExecuteActivity(a.CloseHumanGate, HumanGateClose{StepRunID: req.StepRunID, Status: domain.ApprovalApproved, By: "web"}); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Store.GetApproval(t.Context(), uuid.MustParse(req.StepRunID))
	if got.Status != domain.ApprovalRejected {
		t.Errorf("approval = %s, want the first decision", got.Status)
	}
}

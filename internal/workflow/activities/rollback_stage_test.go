package activities

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

// M7, FR-RL-2, D114: a failed deployment is rolled back by its driver, once.
func TestRollbackStage(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 0)
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.EndDeployment, EndDeploymentRequest{DeploymentID: req.DeploymentID, Reason: "staging: e2e failed"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := activityEnv(f.a).ExecuteActivity(f.a.RollbackStage, req); err != nil {
			t.Fatal(err)
		}
	}
	var ops []string
	for _, c := range deployCalls(f.flux) {
		ops = append(ops, c.Op)
	}
	if !slices.Equal(ops, []string{driverstest.OpRollback}) {
		t.Errorf("driver ops = %v, want one rollback", ops)
	}
	dep := f.deployment(t, req)
	var state driverstest.FakeState
	if dep.Status != domain.ReleaseRolledBack || dep.Reason != "staging: e2e failed" || json.Unmarshal(dep.DriverState, &state) != nil || !state.RolledBack {
		t.Errorf("deployment = %+v (state %s)", dep, dep.DriverState)
	}
}

func TestRollbackStageUnsupported(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 1) // plain cannot roll back
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.EndDeployment, EndDeploymentRequest{DeploymentID: req.DeploymentID, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	_, err := activityEnv(f.a).ExecuteActivity(f.a.RollbackStage, req)
	var app *temporal.ApplicationError
	if !errors.As(err, &app) || !app.NonRetryable() {
		t.Errorf("rollback without the capability = %v, want non-retryable", err)
	}
	if dep := f.deployment(t, req); dep.Status != domain.ReleaseFailed {
		t.Errorf("deployment = %s, want failed", dep.Status)
	}
}

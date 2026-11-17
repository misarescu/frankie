package activities

import (
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// M7, D114.
func TestEndDeployment(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 0)
	for range 2 { // the repeat is a no-op
		if _, err := activityEnv(f.a).ExecuteActivity(f.a.EndDeployment, EndDeploymentRequest{DeploymentID: req.DeploymentID, Reason: "staging: e2e failed"}); err != nil {
			t.Fatal(err)
		}
	}
	if dep := f.deployment(t, req); dep.Status != domain.ReleaseFailed || dep.Reason != "staging: e2e failed" || dep.FinishedAt == nil {
		t.Errorf("deployment = %+v, want failed with the reason", dep)
	}

	ok := f.stageRequest(t, 1)
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.EndDeployment, EndDeploymentRequest{DeploymentID: ok.DeploymentID, Succeeded: true}); err != nil {
		t.Fatal(err)
	}
	if dep := f.deployment(t, ok); dep.Status != domain.ReleaseSucceeded {
		t.Errorf("deployment = %s, want succeeded", dep.Status)
	}
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.EndDeployment, EndDeploymentRequest{DeploymentID: "x"}); err == nil {
		t.Error("bad id accepted")
	}
}

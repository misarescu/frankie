package activities

import (
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// stageRequest prepares the release and starts its deployment to stage i.
func (f *releaseFixture) stageRequest(t *testing.T, i int) StageRequest {
	t.Helper()
	plan := f.prepare(t)
	req := StageRequest{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, Stage: plan.Stages[i]}
	v, err := activityEnv(f.a).ExecuteActivity(f.a.StartDeployment, req)
	if err != nil {
		t.Fatalf("StartDeployment: %v", err)
	}
	if err := v.Get(&req.DeploymentID); err != nil {
		t.Fatal(err)
	}
	return req
}

func (f *releaseFixture) deployment(t *testing.T, req StageRequest) domain.Deployment {
	t.Helper()
	dep, err := f.a.Store.GetDeployment(t.Context(), uuid.MustParse(req.DeploymentID))
	if err != nil {
		t.Fatal(err)
	}
	return dep
}

// M7, D114.
func TestStartDeployment(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 0)
	dep := f.deployment(t, req)
	if dep.Status != domain.ReleaseInProgress || dep.Stage != "staging" || dep.Driver != "flux" || dep.StartedAt == nil {
		t.Errorf("deployment = %+v, want staging with flux, started", dep)
	}
	v, err := activityEnv(f.a).ExecuteActivity(f.a.StartDeployment, req)
	var again string
	if err != nil || v.Get(&again) != nil || again != req.DeploymentID {
		t.Errorf("retry = %s, %v; want the same deployment", again, err)
	}
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.StartDeployment, StageRequest{ReleaseID: "x", Stage: req.Stage}); err == nil {
		t.Error("bad release id accepted")
	}
}

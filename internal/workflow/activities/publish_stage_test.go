package activities

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

// M7, D112, D114: the driver gets the stage, the release and the deployment,
// and its state is recorded.
func TestPublishStage(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 0)
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.PublishStage, req); err != nil {
		t.Fatal(err)
	}
	calls := deployCalls(f.flux)
	if len(calls) != 1 || calls[0].Op != driverstest.OpPublish {
		t.Fatalf("calls = %+v", calls)
	}
	got := calls[0].Request
	if got.Stage.Name != "staging" || string(got.Stage.Config) != `{"path":"apps/goodreads/staging"}` ||
		got.Subject != (drivers.Subject{Repo: relRepo, SHA: relSHA, ReleaseID: uuid.MustParse(req.ReleaseID)}) ||
		got.Deployment.ID.String() != req.DeploymentID || string(got.Deployment.State) != "{}" {
		t.Errorf("driver saw %+v", got)
	}
	var state driverstest.FakeState
	if err := json.Unmarshal(f.deployment(t, req).DriverState, &state); err != nil || state.Op != driverstest.OpPublish || state.SHA != relSHA {
		t.Errorf("recorded state = %s, %v", f.deployment(t, req).DriverState, err)
	}
}

func TestPublishStageErrors(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 0)
	f.flux.Errs = map[string]error{driverstest.OpPublish: errors.New("git push failed")}
	_, err := activityEnv(f.a).ExecuteActivity(f.a.PublishStage, req)
	var app *temporal.ApplicationError
	if err == nil || (errors.As(err, &app) && app.NonRetryable()) {
		t.Errorf("transient driver error = %v, want a retryable error", err)
	}
	f.flux.Errs = map[string]error{driverstest.OpPublish: drivers.ErrInvalidConfig}
	_, err = activityEnv(f.a).ExecuteActivity(f.a.PublishStage, req)
	if !errors.As(err, &app) || !app.NonRetryable() {
		t.Errorf("driver refusal = %v, want non-retryable", err)
	}
	bad := req
	bad.Stage.Name = "production"
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.PublishStage, bad); !errors.As(err, &app) || !app.NonRetryable() {
		t.Errorf("deployment of another stage = %v, want non-retryable", err)
	}
}

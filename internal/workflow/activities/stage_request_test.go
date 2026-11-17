package activities

import (
	"errors"
	"testing"

	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/drivers"
)

// M7, D114: a driver's refusal is final, any other error is retried.
func TestDriverError(t *testing.T) {
	for err, final := range map[error]bool{
		drivers.ErrUnsupported:    true,
		drivers.ErrInvalidRequest: true,
		drivers.ErrInvalidConfig:  true,
		errors.New("git push"):    false,
	} {
		got := driverError("publish", err)
		var app *temporal.ApplicationError
		if isFinal := errors.As(got, &app) && app.NonRetryable(); isFinal != final {
			t.Errorf("driverError(%v) final = %v, want %v", err, isFinal, final)
		}
	}
}

func TestResolveRejectsBadRequests(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 0)
	for name, bad := range map[string]func(*StageRequest){
		"bad deployment id": func(r *StageRequest) { r.DeploymentID = "x" },
		"unknown driver":    func(r *StageRequest) { r.Stage.Driver = "argocd" },
		"other stage":       func(r *StageRequest) { r.Stage.Name = "production" },
	} {
		r := req
		bad(&r)
		if _, err := f.a.resolve(t.Context(), r); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if c, err := f.a.resolve(t.Context(), req); err != nil || c.driver.Name() != "flux" {
		t.Errorf("resolve = %v, %v", c, err)
	}
}

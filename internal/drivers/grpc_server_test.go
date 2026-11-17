package drivers_test

import (
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	driverv1 "github.com/misarescu/frankie/gen/frankie/driver/v1"
	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

func protoRequest() (*driverv1.Stage, *driverv1.Subject, *driverv1.Deployment) {
	return &driverv1.Stage{Name: "staging", ConfigJson: []byte(`{}`)},
		&driverv1.Subject{Repo: "misarescu/toy", Sha: "abc123", ReleaseId: uuid.NewString()},
		&driverv1.Deployment{Id: uuid.NewString(), StateJson: []byte(`{}`)}
}

// M7, D112: the server checks what it is sent and answers with the contract's codes.
func TestServer(t *testing.T) {
	ctx := t.Context()
	fake := &driverstest.Fake{DriverName: "flux", Caps: drivers.Capabilities{Health: true}, Phase: drivers.PhaseFailed}
	s := drivers.NewServer(fake)

	desc, err := s.Describe(ctx, &driverv1.DescribeRequest{})
	if err != nil || desc.GetName() != "flux" || !desc.GetCapabilities().GetHealth() || desc.GetCapabilities().GetRollback() {
		t.Errorf("Describe = %v, %v", desc, err)
	}

	stage, subj, dep := protoRequest()
	st, err := s.Status(ctx, &driverv1.StatusRequest{Stage: stage, Subject: subj, Deployment: dep})
	if err != nil || st.GetPhase() != driverv1.Phase_PHASE_FAILED {
		t.Errorf("Status = %v, %v; want failed", st, err)
	}

	tests := []struct {
		name string
		call func() error
		want codes.Code
	}{
		{"bad deployment id", func() error {
			stage, subj, _ := protoRequest()
			_, err := s.Publish(ctx, &driverv1.PublishRequest{Stage: stage, Subject: subj, Deployment: &driverv1.Deployment{Id: "x"}})
			return err
		}, codes.InvalidArgument},
		{"bad release id", func() error {
			stage, subj, dep := protoRequest()
			subj.ReleaseId = "x"
			_, err := s.Publish(ctx, &driverv1.PublishRequest{Stage: stage, Subject: subj, Deployment: dep})
			return err
		}, codes.InvalidArgument},
		{"missing stage", func() error {
			_, subj, dep := protoRequest()
			_, err := s.Publish(ctx, &driverv1.PublishRequest{Subject: subj, Deployment: dep})
			return err
		}, codes.InvalidArgument},
		{"config not an object", func() error {
			_, err := s.ValidateConfig(ctx, &driverv1.ValidateConfigRequest{ConfigJson: []byte(`[]`)})
			return err
		}, codes.InvalidArgument},
		{"rollback unsupported", func() error {
			stage, subj, dep := protoRequest()
			_, err := s.Rollback(ctx, &driverv1.RollbackRequest{Stage: stage, Subject: subj, Deployment: dep})
			return err
		}, codes.Unimplemented},
		{"promote unsupported", func() error {
			stage, subj, dep := protoRequest()
			_, err := s.Promote(ctx, &driverv1.PromoteRequest{Stage: stage, Subject: subj, Deployment: dep, Step: &driverv1.RolloutStep{Name: "x"}})
			return err
		}, codes.Unimplemented},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(tt.call()); got != tt.want {
				t.Errorf("code = %s, want %s", got, tt.want)
			}
		})
	}
	if ops := fake.Ops(); len(ops) != 1 {
		t.Errorf("the driver was called for invalid requests: %v", ops)
	}
}

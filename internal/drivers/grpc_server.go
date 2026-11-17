package drivers

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	driverv1 "github.com/misarescu/frankie/gen/frankie/driver/v1"
)

// server serves a Driver as frankie.driver.v1.ReleaseDriverService: what an
// out-of-process driver runs (D109).
type server struct {
	driverv1.UnimplementedReleaseDriverServiceServer
	d Driver
}

// NewServer serves d, wrapped by Checked, over the driver contract.
func NewServer(d Driver) driverv1.ReleaseDriverServiceServer {
	return &server{d: Checked(d)}
}

func (s *server) Describe(context.Context, *driverv1.DescribeRequest) (*driverv1.DescribeResponse, error) {
	c := s.d.Capabilities()
	return &driverv1.DescribeResponse{Name: s.d.Name(), Capabilities: &driverv1.Capabilities{
		Rollback: c.Rollback, Progressive: c.Progressive, Health: c.Health, Preview: c.Preview,
	}}, nil
}

func (s *server) ValidateConfig(ctx context.Context, in *driverv1.ValidateConfigRequest) (*driverv1.ValidateConfigResponse, error) {
	if err := s.d.ValidateConfig(ctx, in.GetConfigJson()); err != nil {
		return nil, toStatus(err)
	}
	return &driverv1.ValidateConfigResponse{}, nil
}

func (s *server) Publish(ctx context.Context, in *driverv1.PublishRequest) (*driverv1.PublishResponse, error) {
	r, err := requestFromProto(in.GetStage(), in.GetSubject(), in.GetDeployment())
	if err != nil {
		return nil, err
	}
	st, err := s.d.Publish(ctx, r)
	if err != nil {
		return nil, toStatus(err)
	}
	return &driverv1.PublishResponse{StateJson: st}, nil
}

func (s *server) Status(ctx context.Context, in *driverv1.StatusRequest) (*driverv1.StatusResponse, error) {
	r, err := requestFromProto(in.GetStage(), in.GetSubject(), in.GetDeployment())
	if err != nil {
		return nil, err
	}
	st, err := s.d.Status(ctx, r)
	if err != nil {
		return nil, toStatus(err)
	}
	return &driverv1.StatusResponse{Phase: phaseToProto[st.Phase], Message: st.Message}, nil
}

func (s *server) Promote(ctx context.Context, in *driverv1.PromoteRequest) (*driverv1.PromoteResponse, error) {
	r, err := requestFromProto(in.GetStage(), in.GetSubject(), in.GetDeployment())
	if err != nil {
		return nil, err
	}
	step := RolloutStep{Name: in.GetStep().GetName(), Percentage: int(in.GetStep().GetPercentage())}
	st, err := s.d.Promote(ctx, r, step)
	if err != nil {
		return nil, toStatus(err)
	}
	return &driverv1.PromoteResponse{StateJson: st}, nil
}

func (s *server) Rollback(ctx context.Context, in *driverv1.RollbackRequest) (*driverv1.RollbackResponse, error) {
	r, err := requestFromProto(in.GetStage(), in.GetSubject(), in.GetDeployment())
	if err != nil {
		return nil, err
	}
	st, err := s.d.Rollback(ctx, r)
	if err != nil {
		return nil, toStatus(err)
	}
	return &driverv1.RollbackResponse{StateJson: st}, nil
}

var phaseToProto = map[Phase]driverv1.Phase{
	PhaseProgressing: driverv1.Phase_PHASE_PROGRESSING,
	PhaseReady:       driverv1.Phase_PHASE_READY,
	PhaseFailed:      driverv1.Phase_PHASE_FAILED,
}

func requestFromProto(stage *driverv1.Stage, subj *driverv1.Subject, dep *driverv1.Deployment) (Request, error) {
	depID, err := uuid.Parse(dep.GetId())
	if err != nil {
		return Request{}, status.Errorf(codes.InvalidArgument, "deployment id %q: %v", dep.GetId(), err)
	}
	var releaseID uuid.UUID
	if id := subj.GetReleaseId(); id != "" {
		if releaseID, err = uuid.Parse(id); err != nil {
			return Request{}, status.Errorf(codes.InvalidArgument, "release id %q: %v", id, err)
		}
	}
	return Request{
		Stage: Stage{Name: stage.GetName(), Config: stage.GetConfigJson()},
		Subject: Subject{Repo: subj.GetRepo(), SHA: subj.GetSha(), ReleaseID: releaseID,
			ChangeRequest: int(subj.GetChangeRequest())},
		Deployment: Deployment{ID: depID, State: dep.GetStateJson()},
	}, nil
}

// toStatus carries the contract's errors as status codes, which the client
// turns back into them.
func toStatus(err error) error {
	code := codes.Unknown
	switch {
	case errors.Is(err, ErrUnsupported):
		code = codes.Unimplemented
	case errors.Is(err, ErrInvalidConfig), errors.Is(err, ErrInvalidRequest):
		code = codes.InvalidArgument
	case errors.Is(err, ErrNoRoom):
		code = codes.ResourceExhausted
	case errors.Is(err, context.Canceled):
		code = codes.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		code = codes.DeadlineExceeded
	}
	return status.Error(code, fmt.Sprint(err))
}

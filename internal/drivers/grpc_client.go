package drivers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	driverv1 "github.com/misarescu/frankie/gen/frankie/driver/v1"
)

// client is a Driver served out of process over the driver contract.
type client struct {
	c    driverv1.ReleaseDriverServiceClient
	name string
	caps Capabilities
}

// NewClient asks the driver behind conn to describe itself and returns it as a
// Driver; register it like a built-in one.
func NewClient(ctx context.Context, conn grpc.ClientConnInterface) (Driver, error) {
	c := driverv1.NewReleaseDriverServiceClient(conn)
	desc, err := c.Describe(ctx, &driverv1.DescribeRequest{})
	if err != nil {
		return nil, fmt.Errorf("drivers: describe: %w", fromStatus(err, ErrInvalidRequest))
	}
	pc := desc.GetCapabilities()
	return &client{c: c, name: desc.GetName(), caps: Capabilities{
		Rollback: pc.GetRollback(), Progressive: pc.GetProgressive(), Health: pc.GetHealth(), Preview: pc.GetPreview(),
	}}, nil
}

func (c *client) Name() string               { return c.name }
func (c *client) Capabilities() Capabilities { return c.caps }

func (c *client) ValidateConfig(ctx context.Context, config json.RawMessage) error {
	_, err := c.c.ValidateConfig(ctx, &driverv1.ValidateConfigRequest{ConfigJson: config})
	return fromStatus(err, ErrInvalidConfig)
}

func (c *client) Publish(ctx context.Context, r Request) (State, error) {
	stage, subj, dep := requestToProto(r)
	out, err := c.c.Publish(ctx, &driverv1.PublishRequest{Stage: stage, Subject: subj, Deployment: dep})
	if err != nil {
		return nil, fromStatus(err, ErrInvalidRequest)
	}
	return out.GetStateJson(), nil
}

func (c *client) Status(ctx context.Context, r Request) (Status, error) {
	stage, subj, dep := requestToProto(r)
	out, err := c.c.Status(ctx, &driverv1.StatusRequest{Stage: stage, Subject: subj, Deployment: dep})
	if err != nil {
		return Status{}, fromStatus(err, ErrInvalidRequest)
	}
	return Status{Phase: phaseFromProto[out.GetPhase()], Message: out.GetMessage()}, nil
}

func (c *client) Promote(ctx context.Context, r Request, step RolloutStep) (State, error) {
	stage, subj, dep := requestToProto(r)
	out, err := c.c.Promote(ctx, &driverv1.PromoteRequest{Stage: stage, Subject: subj, Deployment: dep,
		Step: &driverv1.RolloutStep{Name: step.Name, Percentage: int32(step.Percentage)}})
	if err != nil {
		return nil, fromStatus(err, ErrInvalidRequest)
	}
	return out.GetStateJson(), nil
}

func (c *client) Rollback(ctx context.Context, r Request) (State, error) {
	stage, subj, dep := requestToProto(r)
	out, err := c.c.Rollback(ctx, &driverv1.RollbackRequest{Stage: stage, Subject: subj, Deployment: dep})
	if err != nil {
		return nil, fromStatus(err, ErrInvalidRequest)
	}
	return out.GetStateJson(), nil
}

var phaseFromProto = map[driverv1.Phase]Phase{
	driverv1.Phase_PHASE_PROGRESSING: PhaseProgressing,
	driverv1.Phase_PHASE_READY:       PhaseReady,
	driverv1.Phase_PHASE_FAILED:      PhaseFailed,
}

func requestToProto(r Request) (*driverv1.Stage, *driverv1.Subject, *driverv1.Deployment) {
	var releaseID string
	if r.Subject.ReleaseID != uuid.Nil {
		releaseID = r.Subject.ReleaseID.String()
	}
	return &driverv1.Stage{Name: r.Stage.Name, ConfigJson: r.Stage.Config},
		&driverv1.Subject{Repo: r.Subject.Repo, Sha: r.Subject.SHA, ReleaseId: releaseID, ChangeRequest: int64(r.Subject.ChangeRequest)},
		&driverv1.Deployment{Id: r.Deployment.ID.String(), StateJson: r.Deployment.State}
}

// fromStatus turns the contract's status codes back into its errors:
// UNIMPLEMENTED is ErrUnsupported, INVALID_ARGUMENT is invalid, the call's
// sentinel saying what was, and RESOURCE_EXHAUSTED is ErrNoRoom. Other errors are returned as they are.
func fromStatus(err error, invalid error) error {
	if err == nil {
		return nil
	}
	s, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch s.Code() {
	case codes.Unimplemented:
		return fmt.Errorf("%s: %w", s.Message(), ErrUnsupported)
	case codes.InvalidArgument:
		return fmt.Errorf("%s: %w", s.Message(), invalid)
	case codes.ResourceExhausted:
		return fmt.Errorf("%s: %w", s.Message(), ErrNoRoom)
	case codes.Canceled:
		return fmt.Errorf("%s: %w", s.Message(), context.Canceled)
	case codes.DeadlineExceeded:
		return fmt.Errorf("%s: %w", s.Message(), context.DeadlineExceeded)
	}
	return err
}

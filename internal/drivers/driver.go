// Package drivers holds the release driver contract (docs/spec/06-delivery.md,
// D109, D112): the Go interface the release workflow's Activities call, the
// registry of the drivers a deployment has, and adapters to and from the gRPC
// service in proto/frankie/driver/v1, so a driver can later run out of process
// without changing the interface. Drivers run only in control-plane
// Activities, never in agent pods.
package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Errors a driver returns, which the gRPC adapters carry as status codes.
var (
	// ErrUnsupported: the driver lacks the capability the call needs.
	ErrUnsupported = errors.New("not supported by the driver")
	// ErrInvalidConfig: a stage's driver configuration is rejected.
	ErrInvalidConfig = errors.New("invalid driver configuration")
	// ErrInvalidRequest: a call is incomplete or malformed.
	ErrInvalidRequest = errors.New("invalid driver request")
)

// Capabilities are what a driver can do beyond Publish and Status.
type Capabilities struct {
	// Rollback undoes a deployment.
	Rollback bool
	// Progressive deployments move through rollout steps with Promote.
	Progressive bool
	// Health: Status reflects the running target, not only that the change
	// was made.
	Health bool
	// Preview deploys change requests before they merge (D14) rather than
	// releases.
	Preview bool
}

// Missing names the capabilities of need that c lacks, in a fixed order.
func (c Capabilities) Missing(need Capabilities) []string {
	var out []string
	for _, f := range []struct {
		name      string
		have, set bool
	}{
		{"rollback", c.Rollback, need.Rollback},
		{"progressive", c.Progressive, need.Progressive},
		{"health", c.Health, need.Health},
		{"preview", c.Preview, need.Preview},
	} {
		if f.set && !f.have {
			out = append(out, f.name)
		}
	}
	return out
}

// State is a driver's own record of a deployment: a JSON object, opaque to the
// control plane, stored on the Deployment and handed back on later calls.
type State = json.RawMessage

// EmptyState is a deployment's state before Publish returned.
var EmptyState = State(`{}`)

// Stage is the stage being deployed to.
type Stage struct {
	Name string
	// Config is the stage's driver configuration, a JSON object.
	Config json.RawMessage
}

// Subject is what a deployment deploys: a release, or a change request's head
// for a preview stage.
type Subject struct {
	Repo string // e.g. "owner/name"
	SHA  string // the full commit
	// ReleaseID is the Release's ID; zero for a preview.
	ReleaseID uuid.UUID
	// ChangeRequest is the change request's number for a preview; 0 for a release.
	ChangeRequest int
}

// Preview reports whether the subject is a change request's head.
func (s Subject) Preview() bool { return s.ChangeRequest > 0 }

// Validate checks the subject is a release or a change request, not both.
func (s Subject) Validate() error {
	switch {
	case s.Repo == "" || s.SHA == "":
		return errors.New("subject needs a repo and a commit")
	case s.ChangeRequest < 0:
		return fmt.Errorf("subject: change request %d is not a number", s.ChangeRequest)
	case (s.ReleaseID == uuid.Nil) == (s.ChangeRequest == 0):
		return errors.New("subject is a release or a change request, exactly one")
	}
	return nil
}

// Deployment is a subject at a stage as the control plane recorded it.
type Deployment struct {
	ID    uuid.UUID
	State State
}

// Request is what every deployment call carries.
type Request struct {
	Stage      Stage
	Subject    Subject
	Deployment Deployment
}

// Validate checks a request before it reaches a driver.
func (r Request) Validate() error {
	if strings.TrimSpace(r.Stage.Name) == "" {
		return errors.New("request needs a stage")
	}
	if err := ValidateObject(r.Stage.Config); err != nil {
		return fmt.Errorf("stage %s config: %w", r.Stage.Name, err)
	}
	if r.Deployment.ID == uuid.Nil {
		return errors.New("request needs a deployment")
	}
	if err := ValidateObject(r.Deployment.State); err != nil {
		return fmt.Errorf("deployment %s state: %w", r.Deployment.ID, err)
	}
	return r.Subject.Validate()
}

// RolloutStep is one step of a progressive rollout.
type RolloutStep struct {
	Name       string
	Percentage int // 0 to 100
}

// Phase is how far a stage is with a deployment.
type Phase string

// Phases.
const (
	// PhaseProgressing: the change is made, the stage does not run it yet.
	PhaseProgressing Phase = "progressing"
	// PhaseReady: the stage runs the subject.
	PhaseReady Phase = "ready"
	// PhaseFailed: the stage will not run it without a new change.
	PhaseFailed Phase = "failed"
)

// Valid reports whether p is a known phase.
func (p Phase) Valid() bool {
	return slices.Contains([]Phase{PhaseProgressing, PhaseReady, PhaseFailed}, p)
}

// Status is what a driver sees of a deployment.
type Status struct {
	Phase Phase
	// Message says what the driver saw, e.g. the version the app reports.
	Message string
}

// Driver deploys a project's commit to one stage. A driver keeps no state
// between calls: what it did is the State it returns, which later calls get
// back. Publish and Rollback may be retried with the same request and must be
// harmless when repeated.
type Driver interface {
	// Name is how stage configuration refers to the driver, e.g. "flux".
	Name() string
	Capabilities() Capabilities
	// ValidateConfig checks a stage's driver configuration (a JSON object)
	// when the project's configuration is saved; a rejection wraps
	// ErrInvalidConfig.
	ValidateConfig(ctx context.Context, config json.RawMessage) error
	// Publish starts deploying the subject: it returns once the change is
	// made, not once it is live.
	Publish(ctx context.Context, r Request) (State, error)
	// Status reports whether the stage runs the subject yet.
	Status(ctx context.Context, r Request) (Status, error)
	// Promote moves the deployment one rollout step on; ErrUnsupported
	// without Capabilities.Progressive.
	Promote(ctx context.Context, r Request, step RolloutStep) (State, error)
	// Rollback puts the stage back to what it ran before the deployment;
	// ErrUnsupported without Capabilities.Rollback.
	Rollback(ctx context.Context, r Request) (State, error)
}

// ValidateObject checks raw is a JSON object.
func ValidateObject(raw json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return errors.New("must be a JSON object")
	}
	return nil
}

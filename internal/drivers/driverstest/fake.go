// Package driverstest provides a fake release driver for tests of the release
// workflow and its Activities.
package driverstest

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/misarescu/frankie/internal/drivers"
)

// Operations a Fake records, also the keys of Fake.Errs.
const (
	OpValidate = "validate"
	OpPublish  = "publish"
	OpStatus   = "status"
	OpPromote  = "promote"
	OpRollback = "rollback"
)

// Call is one call a Fake received.
type Call struct {
	Op      string
	Request drivers.Request
	Step    drivers.RolloutStep // Promote only
}

// FakeState is the state a Fake returns: what it last did to the deployment.
type FakeState struct {
	Op         string `json:"op"`
	SHA        string `json:"sha"`
	Step       string `json:"step,omitempty"`
	RolledBack bool   `json:"rolledBack,omitempty"`
}

// Fake is a release driver that records its calls. Its zero value is not
// usable: set DriverName. Safe for concurrent use.
type Fake struct {
	DriverName string
	Caps       drivers.Capabilities
	// Phase is what Status reports, ready when empty; Message goes with it.
	Phase   drivers.Phase
	Message string
	// Errs makes an operation fail with the error.
	Errs map[string]error
	// FailFirst limits an Errs entry to that many calls of its operation;
	// later calls succeed.
	FailFirst map[string]int
	// RejectKey makes ValidateConfig reject a config that has this key.
	RejectKey string

	mu    sync.Mutex
	calls []Call
}

// Calls returns the calls so far, oldest first.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Ops returns the operations called so far, oldest first.
func (f *Fake) Ops() []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Op)
	}
	return out
}

// Name implements drivers.Driver.
func (f *Fake) Name() string { return f.DriverName }

// Capabilities implements drivers.Driver.
func (f *Fake) Capabilities() drivers.Capabilities { return f.Caps }

// ValidateConfig implements drivers.Driver.
func (f *Fake) ValidateConfig(_ context.Context, config json.RawMessage) error {
	if err := f.record(Call{Op: OpValidate}); err != nil {
		return err
	}
	if f.RejectKey == "" {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(config, &obj); err != nil {
		return fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	if _, ok := obj[f.RejectKey]; ok {
		return fmt.Errorf("%w: %s is not allowed", drivers.ErrInvalidConfig, f.RejectKey)
	}
	return nil
}

// Publish implements drivers.Driver.
func (f *Fake) Publish(_ context.Context, r drivers.Request) (drivers.State, error) {
	if err := f.record(Call{Op: OpPublish, Request: r}); err != nil {
		return nil, err
	}
	return state(FakeState{Op: OpPublish, SHA: r.Subject.SHA})
}

// Status implements drivers.Driver.
func (f *Fake) Status(_ context.Context, r drivers.Request) (drivers.Status, error) {
	if err := f.record(Call{Op: OpStatus, Request: r}); err != nil {
		return drivers.Status{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	phase := f.Phase
	if phase == "" {
		phase = drivers.PhaseReady
	}
	return drivers.Status{Phase: phase, Message: f.Message}, nil
}

// Promote implements drivers.Driver.
func (f *Fake) Promote(_ context.Context, r drivers.Request, step drivers.RolloutStep) (drivers.State, error) {
	if err := f.record(Call{Op: OpPromote, Request: r, Step: step}); err != nil {
		return nil, err
	}
	return state(FakeState{Op: OpPromote, SHA: r.Subject.SHA, Step: step.Name})
}

// Rollback implements drivers.Driver.
func (f *Fake) Rollback(_ context.Context, r drivers.Request) (drivers.State, error) {
	if err := f.record(Call{Op: OpRollback, Request: r}); err != nil {
		return nil, err
	}
	return state(FakeState{Op: OpRollback, SHA: r.Subject.SHA, RolledBack: true})
}

// SetPhase changes what Status reports, e.g. while a test waits for a stage.
func (f *Fake) SetPhase(p drivers.Phase, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Phase, f.Message = p, message
}

func (f *Fake) record(c Call) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	err := f.Errs[c.Op]
	if n, limited := f.FailFirst[c.Op]; limited && err != nil {
		if n <= 0 {
			return nil
		}
		f.FailFirst[c.Op] = n - 1
	}
	return err
}

func state(s FakeState) (drivers.State, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("fake driver: %w", err)
	}
	return b, nil
}

package drivers

import (
	"context"
	"encoding/json"
	"fmt"
)

// checked enforces the contract around a driver, built in or remote: requests
// are complete, a capability the driver lacks is ErrUnsupported without
// calling it, a preview driver gets only change requests and any other only
// releases, and what it returns is a JSON object state or a known phase.
type checked struct {
	d    Driver
	caps Capabilities
}

// Checked wraps d so every call keeps the contract; the registry returns
// drivers wrapped.
func Checked(d Driver) Driver {
	if c, ok := d.(*checked); ok {
		return c
	}
	return &checked{d: d, caps: d.Capabilities()}
}

func (c *checked) Name() string               { return c.d.Name() }
func (c *checked) Capabilities() Capabilities { return c.caps }

func (c *checked) ValidateConfig(ctx context.Context, config json.RawMessage) error {
	if err := ValidateObject(config); err != nil {
		return fmt.Errorf("%s: config %w: %w", c.d.Name(), err, ErrInvalidConfig)
	}
	return c.d.ValidateConfig(ctx, config)
}

func (c *checked) Publish(ctx context.Context, r Request) (State, error) {
	if err := c.check(r); err != nil {
		return nil, err
	}
	return c.state("publish", func() (State, error) { return c.d.Publish(ctx, r) })
}

func (c *checked) Status(ctx context.Context, r Request) (Status, error) {
	if err := c.check(r); err != nil {
		return Status{}, err
	}
	st, err := c.d.Status(ctx, r)
	if err != nil {
		return Status{}, err
	}
	if !st.Phase.Valid() {
		return Status{}, fmt.Errorf("%s: status: unknown phase %q", c.d.Name(), st.Phase)
	}
	return st, nil
}

func (c *checked) Promote(ctx context.Context, r Request, step RolloutStep) (State, error) {
	if !c.caps.Progressive {
		return nil, fmt.Errorf("%s: promote: %w", c.d.Name(), ErrUnsupported)
	}
	if err := c.check(r); err != nil {
		return nil, err
	}
	if step.Name == "" || step.Percentage < 0 || step.Percentage > 100 {
		return nil, fmt.Errorf("%s: promote: %w: rollout step needs a name and a percentage from 0 to 100", c.d.Name(), ErrInvalidRequest)
	}
	return c.state("promote", func() (State, error) { return c.d.Promote(ctx, r, step) })
}

func (c *checked) Rollback(ctx context.Context, r Request) (State, error) {
	if !c.caps.Rollback {
		return nil, fmt.Errorf("%s: rollback: %w", c.d.Name(), ErrUnsupported)
	}
	if err := c.check(r); err != nil {
		return nil, err
	}
	return c.state("rollback", func() (State, error) { return c.d.Rollback(ctx, r) })
}

func (c *checked) check(r Request) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%s: %w: %w", c.d.Name(), ErrInvalidRequest, err)
	}
	if r.Subject.Preview() != c.caps.Preview {
		if c.caps.Preview {
			return fmt.Errorf("%s: a preview driver deploys change requests, not releases: %w", c.d.Name(), ErrUnsupported)
		}
		return fmt.Errorf("%s: previews need a preview driver: %w", c.d.Name(), ErrUnsupported)
	}
	return nil
}

func (c *checked) state(op string, call func() (State, error)) (State, error) {
	st, err := call()
	if err != nil {
		return nil, err
	}
	if err := ValidateObject(st); err != nil {
		return nil, fmt.Errorf("%s: %s returned a state that %w", c.d.Name(), op, err)
	}
	return st, nil
}

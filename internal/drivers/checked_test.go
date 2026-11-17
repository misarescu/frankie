package drivers_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

// returning is a driver whose Publish returns a fixed state and Status a fixed phase.
type returning struct {
	*driverstest.Fake
	state drivers.State
	phase drivers.Phase
}

func (r returning) Publish(context.Context, drivers.Request) (drivers.State, error) {
	return r.state, nil
}
func (r returning) Status(context.Context, drivers.Request) (drivers.Status, error) {
	return drivers.Status{Phase: r.phase}, nil
}

// M7, D112: the contract is enforced around every driver.
func TestCheckedRejectsWhatTheDriverCannotDo(t *testing.T) {
	ctx := t.Context()
	fake := &driverstest.Fake{DriverName: "flux"} // no capabilities
	d := drivers.Checked(fake)
	if _, err := d.Rollback(ctx, releaseRequest()); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("Rollback without the capability = %v, want ErrUnsupported", err)
	}
	if _, err := d.Promote(ctx, releaseRequest(), drivers.RolloutStep{Name: "all", Percentage: 100}); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("Promote without the capability = %v, want ErrUnsupported", err)
	}
	if _, err := d.Publish(ctx, previewRequest()); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("a preview to a release driver = %v, want ErrUnsupported", err)
	}
	bad := releaseRequest()
	bad.Subject.SHA = ""
	if _, err := d.Publish(ctx, bad); !errors.Is(err, drivers.ErrInvalidRequest) {
		t.Errorf("an incomplete request = %v, want ErrInvalidRequest", err)
	}
	if _, err := d.Status(ctx, bad); !errors.Is(err, drivers.ErrInvalidRequest) {
		t.Errorf("Status of an incomplete request = %v, want ErrInvalidRequest", err)
	}
	if ops := fake.Ops(); len(ops) != 0 {
		t.Errorf("the driver was called: %v", ops)
	}

	preview := drivers.Checked(&driverstest.Fake{DriverName: "flux-preview", Caps: drivers.Capabilities{Preview: true}})
	if _, err := preview.Publish(ctx, releaseRequest()); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("a release to a preview driver = %v, want ErrUnsupported", err)
	}
	if _, err := preview.Publish(ctx, previewRequest()); err != nil {
		t.Errorf("a preview to a preview driver = %v", err)
	}
}

func TestCheckedPassesValidCalls(t *testing.T) {
	ctx := t.Context()
	fake := &driverstest.Fake{DriverName: "flux", Caps: drivers.Capabilities{Rollback: true, Progressive: true}}
	d := drivers.Checked(fake)
	if drivers.Checked(d) != d {
		t.Error("Checked wrapped a checked driver again")
	}
	if d.Name() != "flux" || d.Capabilities() != fake.Caps {
		t.Errorf("Name, Capabilities = %s, %+v", d.Name(), d.Capabilities())
	}
	r := releaseRequest()
	if _, err := d.Publish(ctx, r); err != nil {
		t.Fatal(err)
	}
	if st, err := d.Status(ctx, r); err != nil || st.Phase != drivers.PhaseReady {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	if _, err := d.Promote(ctx, r, drivers.RolloutStep{Name: "half", Percentage: 50}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []drivers.RolloutStep{{Percentage: 50}, {Name: "x", Percentage: 101}, {Name: "x", Percentage: -1}} {
		if _, err := d.Promote(ctx, r, step); !errors.Is(err, drivers.ErrInvalidRequest) {
			t.Errorf("Promote(%+v) = %v, want ErrInvalidRequest", step, err)
		}
	}
	if _, err := d.Rollback(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.Ops(), []string{driverstest.OpPublish, driverstest.OpStatus, driverstest.OpPromote, driverstest.OpRollback}; !slices.Equal(got, want) {
		t.Errorf("ops = %v, want %v", got, want)
	}

	// Driver errors pass through.
	boom := errors.New("git push failed")
	fake.Errs = map[string]error{driverstest.OpPublish: boom}
	if _, err := d.Publish(ctx, r); !errors.Is(err, boom) {
		t.Errorf("Publish = %v, want the driver's error", err)
	}
}

func TestCheckedRejectsWhatTheDriverReturns(t *testing.T) {
	ctx := t.Context()
	for _, st := range []string{`[]`, `null`, ``} {
		d := drivers.Checked(returning{Fake: &driverstest.Fake{DriverName: "x"}, state: drivers.State(st), phase: drivers.PhaseReady})
		if _, err := d.Publish(ctx, releaseRequest()); err == nil {
			t.Errorf("state %q accepted", st)
		}
	}
	d := drivers.Checked(returning{Fake: &driverstest.Fake{DriverName: "x"}, state: drivers.EmptyState, phase: "done"})
	if _, err := d.Status(ctx, releaseRequest()); err == nil {
		t.Error("unknown phase accepted")
	}
}

func TestCheckedValidateConfig(t *testing.T) {
	fake := &driverstest.Fake{DriverName: "flux", RejectKey: "token"}
	d := drivers.Checked(fake)
	if err := d.ValidateConfig(t.Context(), json.RawMessage(`{"path":"staging"}`)); err != nil {
		t.Errorf("valid config = %v", err)
	}
	for _, cfg := range []string{`{"token":"x"}`, `[]`, `"x"`} {
		if err := d.ValidateConfig(t.Context(), json.RawMessage(cfg)); !errors.Is(err, drivers.ErrInvalidConfig) {
			t.Errorf("config %s = %v, want ErrInvalidConfig", cfg, err)
		}
	}
	if n := len(fake.Ops()); n != 2 {
		t.Errorf("driver validated %d configs, want 2 (not the ones that are not objects)", n)
	}
}

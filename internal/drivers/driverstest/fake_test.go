package driverstest

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/drivers"
)

// M7, D112: the fake records calls, returns its state and fails on request.
func TestFake(t *testing.T) {
	ctx := t.Context()
	f := &Fake{DriverName: "flux", RejectKey: "token"}
	r := drivers.Request{Subject: drivers.Subject{SHA: "abc"}, Deployment: drivers.Deployment{ID: uuid.New()}}

	st, err := f.Publish(ctx, r)
	var got FakeState
	if err != nil || json.Unmarshal(st, &got) != nil || got != (FakeState{Op: OpPublish, SHA: "abc"}) {
		t.Errorf("Publish = %s, %v", st, err)
	}
	if s, err := f.Status(ctx, r); err != nil || s.Phase != drivers.PhaseReady {
		t.Errorf("default Status = %+v, %v; want ready", s, err)
	}
	f.SetPhase(drivers.PhaseProgressing, "waiting")
	if s, _ := f.Status(ctx, r); s.Phase != drivers.PhaseProgressing || s.Message != "waiting" {
		t.Errorf("Status after SetPhase = %+v", s)
	}
	st, _ = f.Rollback(ctx, r)
	if json.Unmarshal(st, &got) != nil || !got.RolledBack {
		t.Errorf("Rollback state = %s", st)
	}
	st, _ = f.Promote(ctx, r, drivers.RolloutStep{Name: "half"})
	if json.Unmarshal(st, &got) != nil || got.Step != "half" {
		t.Errorf("Promote state = %s", st)
	}
	if err := f.ValidateConfig(ctx, json.RawMessage(`{"token":1}`)); !errors.Is(err, drivers.ErrInvalidConfig) {
		t.Errorf("rejected key = %v", err)
	}
	if err := f.ValidateConfig(ctx, json.RawMessage(`{"path":1}`)); err != nil {
		t.Errorf("valid config = %v", err)
	}

	boom := errors.New("boom")
	f.Errs = map[string]error{OpPublish: boom}
	if _, err := f.Publish(ctx, r); !errors.Is(err, boom) {
		t.Errorf("Publish with Errs = %v", err)
	}
	want := []string{OpPublish, OpStatus, OpStatus, OpRollback, OpPromote, OpValidate, OpValidate, OpPublish}
	if got := f.Ops(); !slices.Equal(got, want) {
		t.Errorf("Ops = %v, want %v", got, want)
	}
}

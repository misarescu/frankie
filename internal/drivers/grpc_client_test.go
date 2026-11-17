package drivers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	driverv1 "github.com/misarescu/frankie/gen/frankie/driver/v1"
	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

// remote serves d over an in-memory connection and returns its client.
func remote(t *testing.T, d drivers.Driver) drivers.Driver {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	driverv1.RegisterReleaseDriverServiceServer(srv, drivers.NewServer(d))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c, err := drivers.NewClient(t.Context(), conn)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// M7, D109, D112: a driver served out of process behaves as the same driver in process.
func TestClientRoundTrip(t *testing.T) {
	ctx := t.Context()
	fake := &driverstest.Fake{DriverName: "flux", Caps: drivers.Capabilities{Rollback: true, Progressive: true, Health: true},
		RejectKey: "token", Phase: drivers.PhaseProgressing, Message: "version is def456"}
	c := remote(t, fake)
	if c.Name() != "flux" || c.Capabilities() != fake.Caps {
		t.Errorf("described as %s %+v, want flux %+v", c.Name(), c.Capabilities(), fake.Caps)
	}

	if err := c.ValidateConfig(ctx, json.RawMessage(`{"path":"staging"}`)); err != nil {
		t.Errorf("ValidateConfig = %v", err)
	}
	if err := c.ValidateConfig(ctx, json.RawMessage(`{"token":"x"}`)); !errors.Is(err, drivers.ErrInvalidConfig) {
		t.Errorf("rejected config = %v, want ErrInvalidConfig", err)
	}

	r := releaseRequest()
	st, err := c.Publish(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	var got driverstest.FakeState
	if err := json.Unmarshal(st, &got); err != nil || got.Op != driverstest.OpPublish || got.SHA != r.Subject.SHA {
		t.Errorf("state = %s, %v", st, err)
	}
	if s, err := c.Status(ctx, r); err != nil || s.Phase != drivers.PhaseProgressing || s.Message != "version is def456" {
		t.Errorf("Status = %+v, %v", s, err)
	}
	if _, err := c.Promote(ctx, r, drivers.RolloutStep{Name: "half", Percentage: 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Rollback(ctx, r); err != nil {
		t.Fatal(err)
	}

	// The driver saw the request as sent.
	calls := fake.Calls()
	if ops := fake.Ops(); !slices.Equal(ops, []string{"validate", "validate", "publish", "status", "promote", "rollback"}) {
		t.Fatalf("ops = %v", ops)
	}
	seen := calls[2].Request
	if seen.Stage.Name != r.Stage.Name || string(seen.Stage.Config) != string(r.Stage.Config) || seen.Subject != r.Subject ||
		seen.Deployment.ID != r.Deployment.ID || string(seen.Deployment.State) != string(r.Deployment.State) {
		t.Errorf("driver saw %+v, want %+v", seen, r)
	}
	if step := calls[4].Step; step.Name != "half" || step.Percentage != 50 {
		t.Errorf("step = %+v", step)
	}

	p := previewRequest()
	previewFake := &driverstest.Fake{DriverName: "flux-preview", Caps: drivers.Capabilities{Preview: true}}
	if _, err := remote(t, previewFake).Publish(ctx, p); err != nil {
		t.Fatal(err)
	}
	if seen := previewFake.Calls()[0].Request.Subject; seen != p.Subject {
		t.Errorf("preview subject = %+v, want %+v", seen, p.Subject)
	}
}

func TestClientErrors(t *testing.T) {
	ctx := t.Context()
	boom := errors.New("git push failed")
	c := remote(t, &driverstest.Fake{DriverName: "flux", Errs: map[string]error{driverstest.OpStatus: boom}})
	r := releaseRequest()
	if _, err := c.Rollback(ctx, r); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("Rollback without the capability = %v, want ErrUnsupported", err)
	}
	if _, err := c.Promote(ctx, r, drivers.RolloutStep{Name: "x"}); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("Promote without the capability = %v, want ErrUnsupported", err)
	}
	bad := r
	bad.Subject.Repo = ""
	if _, err := c.Publish(ctx, bad); !errors.Is(err, drivers.ErrInvalidRequest) {
		t.Errorf("incomplete request = %v, want ErrInvalidRequest", err)
	}
	if _, err := c.Status(ctx, r); err == nil || errors.Is(err, drivers.ErrInvalidRequest) || errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("driver failure = %v, want an error that is neither", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Publish(canceled, r); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled call = %v, want context.Canceled", err)
	}
	// M7 (D126): a driver with no room says so across the wire.
	full := remote(t, &driverstest.Fake{DriverName: "flux-preview", Caps: drivers.Capabilities{Preview: true},
		Errs: map[string]error{driverstest.OpPublish: fmt.Errorf("2 previews run: %w", drivers.ErrNoRoom)}})
	if _, err := full.Publish(ctx, previewRequest()); !errors.Is(err, drivers.ErrNoRoom) || !strings.Contains(err.Error(), "2 previews run") {
		t.Errorf("no room = %v, want ErrNoRoom", err)
	}
}

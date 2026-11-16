package keepalive

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

// D102: the runner must accept the control plane's pings, or it closes the
// connection with "too many pings" while a session streams events.
func TestServerAcceptsClientPings(t *testing.T) {
	if MinTime > Time {
		t.Errorf("runner accepts pings every %s, client pings every %s", MinTime, Time)
	}
	// gRPC raises a client ping interval below 10s to 10s; Time must not rely on less.
	if Time < 10*time.Second {
		t.Errorf("client ping interval %s is below gRPC's 10s floor", Time)
	}
	// A dead runner is noticed within a minute of silence.
	if Time+Timeout > time.Minute {
		t.Errorf("a lost runner is noticed after %s", Time+Timeout)
	}
}

// Both options are usable together on a real connection.
func TestOptionsConnect(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(Server())
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet", Client(),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	resp, err := healthpb.NewHealthClient(conn).Check(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("check = %v, %v", resp, err)
	}
}

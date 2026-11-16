// Package keepalive holds the gRPC keepalive settings of the control plane's
// connection to a runner (D102), so both ends agree on them: the control plane
// pings a silent connection to notice a runner whose sandbox is gone, and the
// runner accepts pings that often instead of closing the connection.
package keepalive

import (
	"time"

	"google.golang.org/grpc"
	grpckeepalive "google.golang.org/grpc/keepalive"
)

const (
	// Time is how long a connection may stay silent before the client pings.
	Time = 30 * time.Second
	// Timeout is how long the client waits for a ping's answer before it
	// closes the connection, failing the calls on it.
	Timeout = 15 * time.Second
	// MinTime is the shortest ping interval the runner accepts. It must not
	// exceed Time: gRPC servers close connections that ping more often than
	// their policy allows (by default every 5 minutes).
	MinTime = 20 * time.Second
)

// Client is the dial option the control plane connects to a runner with.
func Client() grpc.DialOption {
	return grpc.WithKeepaliveParams(grpckeepalive.ClientParameters{Time: Time, Timeout: Timeout, PermitWithoutStream: true})
}

// Server is the option the runner serves with, accepting the client's pings.
func Server() grpc.ServerOption {
	return grpc.KeepaliveEnforcementPolicy(grpckeepalive.EnforcementPolicy{MinTime: MinTime, PermitWithoutStream: true})
}

package egress

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
)

// ErrAddressNotAllowed means the destination resolved to a non-public address.
var ErrAddressNotAllowed = errors.New("egress: destination address not allowed")

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),      // carrier-grade NAT (also Tailscale)
	netip.MustParsePrefix("192.0.0.0/24"),       // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),      // benchmarking
	netip.MustParsePrefix("255.255.255.255/32"), // broadcast
	netip.MustParsePrefix("64:ff9b:1::/48"),     // local-use NAT64
}

// CheckPublic rejects loopback, private, link-local (including cloud metadata),
// multicast, unspecified and other non-public addresses. It runs on the address
// actually dialed, after DNS, so an allowlisted name cannot be pointed inward.
func CheckPublic(ip netip.Addr) error {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("%w: %s", ErrAddressNotAllowed, ip)
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return fmt.Errorf("%w: %s", ErrAddressNotAllowed, ip)
		}
	}
	return nil
}

// dialControl adapts an address check to net.Dialer.Control.
func dialControl(check func(netip.Addr) error) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("%w: unparseable %q", ErrAddressNotAllowed, address)
		}
		return check(ap.Addr())
	}
}

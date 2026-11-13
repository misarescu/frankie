package egress

import (
	"errors"
	"net/netip"
	"testing"
)

func TestCheckPublic(t *testing.T) {
	for addr, public := range map[string]bool{
		"140.82.112.3":    true,  // a GitHub address
		"2606:4700::1111": true,  // public IPv6
		"127.0.0.1":       false, // loopback
		"::1":             false,
		"10.43.0.10":      false, // k3s service range
		"172.16.5.4":      false,
		"192.168.3.7":     false, // the homelab LAN
		"169.254.169.254": false, // cloud metadata
		"fe80::1":         false,
		"fd00::1":         false, // unique local
		"100.100.100.100": false, // CGNAT / Tailscale
		"0.0.0.0":         false,
		"::":              false,
		"224.0.0.1":       false, // multicast
		"::ffff:10.0.0.1": false, // IPv4-mapped private
		"198.18.0.1":      false,
	} {
		err := CheckPublic(netip.MustParseAddr(addr))
		if (err == nil) != public {
			t.Errorf("CheckPublic(%s) = %v, want public=%v", addr, err, public)
		}
		if err != nil && !errors.Is(err, ErrAddressNotAllowed) {
			t.Errorf("CheckPublic(%s) error %v is not ErrAddressNotAllowed", addr, err)
		}
	}
}

// dialControl is what the proxy's dialer runs on every resolved address.
func TestDialControl(t *testing.T) {
	control := dialControl(CheckPublic)
	if err := control("tcp4", "140.82.112.3:443", nil); err != nil {
		t.Errorf("public address refused: %v", err)
	}
	for _, addr := range []string{"127.0.0.1:443", "[::1]:443", "192.168.3.7:443", "not-an-address"} {
		if err := control("tcp", addr, nil); !errors.Is(err, ErrAddressNotAllowed) {
			t.Errorf("control(%q) = %v, want ErrAddressNotAllowed", addr, err)
		}
	}
}

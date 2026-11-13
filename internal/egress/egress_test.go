package egress

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParsePolicy(t *testing.T) {
	p, err := ParsePolicy([]string{" proxy.golang.org ", "*.githubusercontent.com", "Registry.Example:8443", "", "203.0.113.7", "trailing.dot."})
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	if p.Len() != 5 {
		t.Fatalf("Len = %d, want 5", p.Len())
	}
	for _, bad := range []string{"https://github.com", "github.com/path", "*", "*.com", "a*.example.com", "host:0", "host:99999", "host:abc", "user@host"} {
		if _, err := ParsePolicy([]string{bad}); err == nil {
			t.Errorf("ParsePolicy accepted %q", bad)
		}
	}
}

func TestPolicyAllows(t *testing.T) {
	p, err := ParsePolicy([]string{"proxy.golang.org", "*.githubusercontent.com", "registry.example:8443", "203.0.113.7", "trailing.dot."})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		host string
		port int
		want bool
	}{
		{"proxy.golang.org", 443, true},
		{"PROXY.golang.org.", 443, true},             // case and trailing dot
		{"proxy.golang.org", 80, false},              // default port is 443 only
		{"sum.golang.org", 443, false},               // not listed
		{"evilproxy.golang.org", 443, false},         // exact entries do not match subdomains
		{"objects.githubusercontent.com", 443, true}, // wildcard subdomain
		{"a.b.githubusercontent.com", 443, true},     // deeper subdomain
		{"githubusercontent.com", 443, false},        // wildcard excludes the apex
		{"evilgithubusercontent.com", 443, false},    // suffix must be a label boundary
		{"registry.example", 8443, true},             // explicit port
		{"registry.example", 443, false},             // only that port
		{"203.0.113.7", 443, true},                   // listed IP literal
		{"203.0.113.8", 443, false},                  // unlisted IP literal
		{"trailing.dot", 443, true},
	}
	for _, tt := range tests {
		if got := p.Allows(tt.host, tt.port); got != tt.want {
			t.Errorf("Allows(%q, %d) = %v, want %v", tt.host, tt.port, got, tt.want)
		}
	}
}

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

func startProxy(t *testing.T, entries []string, check func(netip.Addr) error) string {
	t.Helper()
	policy, err := ParsePolicy(entries)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newProxy(policy, slog.New(slog.NewTextHandler(io.Discard, nil)), check))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// connect sends a raw request line to the proxy and returns the response.
func connect(t *testing.T, proxyAddr, requestLine, host string) (*http.Response, net.Conn) {
	t.Helper()
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(conn, "%s\r\nHost: %s\r\n\r\n", requestLine, host); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read proxy response: %v", err)
	}
	return resp, conn
}

func bodyOf(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// An allowed destination gets a working tunnel: a real TLS request succeeds through it.
func TestProxyTunnelsAllowedDestination(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello through the tunnel")
	}))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	target := "localhost:" + port
	// Loopback is normally refused; this test permits it to stand in for a public host.
	proxyAddr := startProxy(t, []string{target}, func(netip.Addr) error { return nil })

	proxyURL := &url.URL{Scheme: "http", Host: proxyAddr}
	tr := upstream.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(proxyURL)
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test server certificate
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+target+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := bodyOf(resp); got != "hello through the tunnel" {
		t.Errorf("body = %q", got)
	}
}

func TestProxyRefusals(t *testing.T) {
	// "localhost" is allowlisted, but it resolves to loopback, which the real
	// address guard must refuse (an allowlisted name pointed inward).
	proxyAddr := startProxy(t, []string{"allowed.example", "localhost:1"}, CheckPublic)

	tests := []struct {
		name, line, host string
		wantCode         int
		wantBody         string
	}{
		{"unlisted host", "CONNECT evil.example:443 HTTP/1.1", "evil.example:443", http.StatusForbidden, "not in egress allowlist"},
		{"listed host, wrong port", "CONNECT allowed.example:22 HTTP/1.1", "allowed.example:22", http.StatusForbidden, "not in egress allowlist"},
		{"unlisted IP literal", "CONNECT 192.168.3.7:443 HTTP/1.1", "192.168.3.7:443", http.StatusForbidden, "not in egress allowlist"},
		{"allowlisted name resolving to loopback", "CONNECT localhost:1 HTTP/1.1", "localhost:1", http.StatusForbidden, "non-public address"},
		{"plain HTTP", "GET http://allowed.example/ HTTP/1.1", "allowed.example", http.StatusMethodNotAllowed, "only HTTPS"},
		{"malformed target", "CONNECT allowed.example HTTP/1.1", "allowed.example", http.StatusBadRequest, "host:port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := connect(t, proxyAddr, tt.line, tt.host)
			defer func() { _ = resp.Body.Close() }()
			body := bodyOf(resp)
			if resp.StatusCode != tt.wantCode || !strings.Contains(body, tt.wantBody) {
				t.Errorf("status %d body %q; want %d containing %q", resp.StatusCode, body, tt.wantCode, tt.wantBody)
			}
		})
	}
}

func TestProxyHealthz(t *testing.T) {
	proxyAddr := startProxy(t, nil, CheckPublic)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+proxyAddr+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status %d", resp.StatusCode)
	}
}

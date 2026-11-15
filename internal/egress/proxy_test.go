package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
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

// tokenProxy is a proxy allowing allowed.example, with egress tokens signed by
// signer, that tunnels every allowed CONNECT to upstream.
func tokenProxy(t *testing.T, signer *Signer, upstream string) string {
	t.Helper()
	policy, err := ParsePolicy([]string{"allowed.example"})
	if err != nil {
		t.Fatal(err)
	}
	p := newProxy(policy, slog.New(slog.NewTextHandler(io.Discard, nil)), CheckPublic)
	p.Tokens = signer
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream)
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func testSigner(t *testing.T, key string) *Signer {
	t.Helper()
	s, err := NewSigner([]byte(strings.Repeat(key, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// M5.2 (D80): a pod whose token grants its project's hosts reaches them
// through a client that puts the token in the proxy URL, as pods do.
func TestProxyAllowsProjectHostsWithAToken(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello project host")
	}))
	defer upstream.Close()
	signer := testSigner(t, "k")
	proxyAddr := tokenProxy(t, signer, upstream.Listener.Addr().String())
	token, err := signer.Issue(Grant{Repo: "misarescu/Goodreads-Clone", Hosts: []string{"nodejs.org"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	tr := upstream.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr, User: url.UserPassword("frankie", token)})
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test server certificate
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://nodejs.org/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := bodyOf(resp); got != "hello project host" {
		t.Errorf("body = %q", got)
	}
}

// M5.2 (D80): a token adds only its own hosts, on 443, while it is valid and
// signed with the proxy's key; without one, a pod gets the platform's list.
func TestProxyProjectHostRefusals(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler()) // never reached when refused
	defer upstream.Close()
	signer := testSigner(t, "k")
	proxyAddr := tokenProxy(t, signer, upstream.Listener.Addr().String())
	issue := func(s *Signer, hosts ...string) string {
		tok, err := s.Issue(Grant{Repo: "o/r", Hosts: hosts}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	expired := testSigner(t, "k")
	expired.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	basic := func(token string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte("frankie:"+token))
	}

	tests := []struct {
		name, target, auth string
		wantCode           int
	}{
		{"no token", "nodejs.org:443", "", http.StatusProxyAuthRequired},
		{"another project's host", "nodejs.org:443", basic(issue(signer, "registry.npmjs.org")), http.StatusForbidden},
		{"granted host, other port", "nodejs.org:22", basic(issue(signer, "nodejs.org")), http.StatusForbidden},
		{"subdomain of a granted host", "evil.nodejs.org:443", basic(issue(signer, "nodejs.org")), http.StatusForbidden},
		{"expired token", "nodejs.org:443", basic(issue(expired, "nodejs.org")), http.StatusForbidden},
		{"signed with another key", "nodejs.org:443", basic(issue(testSigner(t, "x"), "nodejs.org")), http.StatusForbidden},
		{"not basic", "nodejs.org:443", "Bearer " + issue(signer, "nodejs.org"), http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", proxyAddr)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", tt.target, tt.target)
			if tt.auth != "" {
				req += "Proxy-Authorization: " + tt.auth + "\r\n"
			}
			if _, err := io.WriteString(conn, req+"\r\n"); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.wantCode {
				t.Errorf("status %d, want %d (%s)", resp.StatusCode, tt.wantCode, bodyOf(resp))
			}
			if tt.wantCode == http.StatusProxyAuthRequired && resp.Header.Get("Proxy-Authenticate") == "" {
				t.Error("407 without a Proxy-Authenticate challenge")
			}
		})
	}
}

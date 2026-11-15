package egress

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Proxy is the egress proxy's HTTP handler. It serves CONNECT tunnels for allowed
// destinations and GET /healthz; everything else is refused.
type Proxy struct {
	policy Policy
	log    *slog.Logger
	// dial reaches a destination; it refuses non-public addresses.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Tokens verifies the egress tokens pods send as their proxy password
	// (D80); a valid one adds its project's hosts to policy. Nil ignores them.
	Tokens *Signer
}

// NewProxy returns a proxy enforcing policy. Dialed addresses must pass CheckPublic.
func NewProxy(policy Policy, log *slog.Logger) *Proxy {
	return newProxy(policy, log, CheckPublic)
}

func newProxy(policy Policy, log *slog.Logger, checkAddr func(netip.Addr) error) *Proxy {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: dialControl(checkAddr)}
	return &Proxy{policy: policy, log: log, dial: dialer.DialContext}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Host == "" && r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if r.Method != http.MethodConnect {
		p.deny(w, r, http.StatusMethodNotAllowed, "only HTTPS (CONNECT) egress is allowed")
		return
	}
	host, portStr, err := net.SplitHostPort(r.Host)
	port, perr := strconv.Atoi(portStr)
	if err != nil || perr != nil {
		p.deny(w, r, http.StatusBadRequest, "CONNECT target must be host:port")
		return
	}
	repo, ok := p.allows(r, host, port)
	if !ok {
		if r.Header.Get("Proxy-Authorization") == "" && p.Tokens != nil {
			// Clients that send credentials only when challenged get the chance.
			w.Header().Set("Proxy-Authenticate", `Basic realm="frankie-egress"`)
			p.deny(w, r, http.StatusProxyAuthRequired, "destination not in egress allowlist")
			return
		}
		p.deny(w, r, http.StatusForbidden, "destination not in egress allowlist")
		return
	}

	upstream, err := p.dial(r.Context(), "tcp", r.Host)
	if err != nil {
		if errors.Is(err, ErrAddressNotAllowed) {
			p.deny(w, r, http.StatusForbidden, "destination resolves to a non-public address")
		} else {
			p.deny(w, r, http.StatusBadGateway, "cannot reach destination")
		}
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		p.deny(w, r, http.StatusInternalServerError, "connection cannot be tunneled")
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	p.log.Info("egress allowed", "host", host, "port", port, "client", r.RemoteAddr, "project", repo)
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	// Bytes the client sent after the CONNECT headers may already be buffered.
	tunnel(client, io.MultiReader(buf.Reader, client), upstream)
}

// allows reports whether the destination is in the platform's list or, for a
// pod with a valid egress token, in its project's hosts (D80), and names the
// project when its hosts allowed it. A bad or expired token adds nothing.
func (p *Proxy) allows(r *http.Request, host string, port int) (string, bool) {
	if p.policy.Allows(host, port) {
		return "", true
	}
	if p.Tokens == nil {
		return "", false
	}
	token, ok := proxyPassword(r.Header.Get("Proxy-Authorization"))
	if !ok {
		return "", false
	}
	g, err := p.Tokens.Verify(token)
	if err != nil {
		p.log.Warn("egress token refused", "error", err, "client", r.RemoteAddr)
		return "", false
	}
	// Project hosts are plain hostnames on port 443 (ValidateProjectHosts),
	// so they parse into exact rules.
	project, err := ParsePolicy(g.Hosts)
	if err != nil || ValidateProjectHosts(g.Hosts) != nil {
		return "", false
	}
	return g.Repo, project.Allows(host, port)
}

// proxyPassword returns the password of Basic proxy credentials, where pods
// put their egress token (user:token in the proxy URL).
func proxyPassword(header string) (string, bool) {
	scheme, cred, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cred))
	if err != nil {
		return "", false
	}
	_, pass, ok := strings.Cut(string(raw), ":")
	return pass, ok && pass != ""
}

func (p *Proxy) deny(w http.ResponseWriter, r *http.Request, code int, reason string) {
	p.log.Warn("egress denied", "target", r.Host, "method", r.Method, "status", code, "reason", reason, "client", r.RemoteAddr)
	http.Error(w, "egress denied: "+reason, code)
}

// tunnel copies both directions until both sides are done, then closes both.
func tunnel(client net.Conn, clientReader io.Reader, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(upstream, clientReader)
		closeWrite(upstream)
	})
	wg.Go(func() {
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
	})
	wg.Wait()
	_ = client.Close()
	_ = upstream.Close()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// Serve runs the proxy on lis until ctx ends.
func Serve(ctx context.Context, lis net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

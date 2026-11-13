package egress

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// Proxy is the egress proxy's HTTP handler. It serves CONNECT tunnels for allowed
// destinations and GET /healthz; everything else is refused.
type Proxy struct {
	policy Policy
	log    *slog.Logger
	dialer *net.Dialer
}

// NewProxy returns a proxy enforcing policy. Dialed addresses must pass CheckPublic.
func NewProxy(policy Policy, log *slog.Logger) *Proxy {
	return newProxy(policy, log, CheckPublic)
}

func newProxy(policy Policy, log *slog.Logger, checkAddr func(netip.Addr) error) *Proxy {
	return &Proxy{
		policy: policy,
		log:    log,
		dialer: &net.Dialer{Timeout: 10 * time.Second, Control: dialControl(checkAddr)},
	}
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
	if !p.policy.Allows(host, port) {
		p.deny(w, r, http.StatusForbidden, "destination not in egress allowlist")
		return
	}

	upstream, err := p.dialer.DialContext(r.Context(), "tcp", r.Host)
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
	p.log.Info("egress allowed", "host", host, "port", port, "client", r.RemoteAddr)
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	// Bytes the client sent after the CONNECT headers may already be buffered.
	tunnel(client, io.MultiReader(buf.Reader, client), upstream)
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

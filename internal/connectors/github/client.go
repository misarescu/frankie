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
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: encode: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("github: decode %s: %w", path, err)
		}
	}
	return nil
}

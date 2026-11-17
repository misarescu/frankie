package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
)

// maxReleaseBody keeps a release's notes under GitHub's limit (125,000
// characters).
const maxReleaseBody = 100_000

// CreateRelease publishes a version as a GitHub Release at its commit (D129)
// and returns the release's page. The tag is created with the release; a tag
// that already names another commit is domain.ErrTagTaken, since GitHub would
// otherwise attach the release to it. A release that already exists for the
// tag at this commit is returned as it is, so a retry publishes once.
func (c *Client) CreateRelease(ctx context.Context, repo string, in domain.NewProviderRelease) (string, error) {
	if err := in.Validate(); err != nil {
		return "", fmt.Errorf("github: release: %w", err)
	}
	tagged, err := c.tagCommit(ctx, repo, in.Tag)
	if err != nil {
		return "", err
	}
	switch {
	case tagged != "" && tagged != in.SHA:
		return "", fmt.Errorf("github: release %s: %w (%s)", in.Tag, domain.ErrTagTaken, tagged)
	case tagged != "":
		if u, found, err := c.releaseByTag(ctx, repo, in.Tag); err != nil || found {
			return u, err
		}
	}
	body := in.Body
	if len(body) > maxReleaseBody {
		body = body[:maxReleaseBody] + "\n\n[...]"
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	err = c.do(ctx, http.MethodPost, "/repos/"+repo+"/releases", map[string]any{
		"tag_name": in.Tag, "target_commitish": in.SHA, "name": in.Name, "body": body,
	}, &out)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusUnprocessableEntity && strings.Contains(se.Body, "already_exists") {
		// Created meanwhile, e.g. by an earlier attempt whose answer was lost.
		if u, found, err := c.releaseByTag(ctx, repo, in.Tag); err != nil || found {
			return u, err
		}
	}
	if err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

// tagCommit is the commit a tag names, or "" when there is no such tag.
func (c *Client) tagCommit(ctx context.Context, repo, tag string) (string, error) {
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/ref/tags/"+url.PathEscape(tag), nil, &ref)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if ref.Object.Type != "tag" {
		return ref.Object.SHA, nil
	}
	// An annotated tag points at a tag object, which points at the commit.
	var annotated struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/tags/"+ref.Object.SHA, nil, &annotated); err != nil {
		return "", err
	}
	return annotated.Object.SHA, nil
}

// releaseByTag is the page of the release for a tag, if there is one.
func (c *Client) releaseByTag(ctx context.Context, repo, tag string) (string, bool, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/releases/tags/"+url.PathEscape(tag), nil, &out)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return out.HTMLURL, true, nil
}

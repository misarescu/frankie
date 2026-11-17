package github

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
)

// ErrFileChanged means a write named a blob version the file no longer has:
// someone changed it since it was read.
var ErrFileChanged = errors.New("github: file changed since it was read")

func contentsPath(repo, path string) string {
	return "/repos/" + repo + "/contents/" + (&url.URL{Path: path}).EscapedPath()
}

// ReadFile returns path in repo at ref with its blob SHA; found is false when
// the file does not exist there (D117).
func (c *Client) ReadFile(ctx context.Context, repo, path, ref string) (domain.RepoFile, bool, error) {
	var out struct {
		Type     string `json:"type"`
		SHA      string `json:"sha"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	err := c.do(ctx, http.MethodGet, contentsPath(repo, path)+"?"+url.Values{"ref": {ref}}.Encode(), nil, &out)
	var se *StatusError
	switch {
	case errors.As(err, &se) && se.Code == http.StatusNotFound:
		return domain.RepoFile{}, false, nil
	case err != nil:
		return domain.RepoFile{}, false, err
	case out.Type != "file" || out.Encoding != "base64":
		return domain.RepoFile{}, false, fmt.Errorf("github: %s in %s is a %s (%s), not a file", path, repo, out.Type, out.Encoding)
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	if err != nil {
		return domain.RepoFile{}, false, fmt.Errorf("github: decode %s: %w", path, err)
	}
	return domain.RepoFile{Content: data, BlobSHA: out.SHA}, true, nil
}

// WriteFile commits content to path on branch, replacing the blob blobSHA, and
// returns the new commit. A file changed since blobSHA was read is
// ErrFileChanged, never overwritten.
func (c *Client) WriteFile(ctx context.Context, repo, path, branch string, content []byte, blobSHA, message string) (string, error) {
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	body := map[string]string{"message": message, "content": base64.StdEncoding.EncodeToString(content), "branch": branch, "sha": blobSHA}
	err := c.do(ctx, http.MethodPut, contentsPath(repo, path), body, &out)
	var se *StatusError
	if errors.As(err, &se) && (se.Code == http.StatusConflict || se.Code == http.StatusUnprocessableEntity && strings.Contains(se.Body, "does not match")) {
		return "", fmt.Errorf("%w: %s", ErrFileChanged, se.Body)
	}
	if err != nil {
		return "", err
	}
	if out.Commit.SHA == "" {
		return "", fmt.Errorf("github: writing %s returned no commit", path)
	}
	return out.Commit.SHA, nil
}

// FileCommits returns the latest commits on branch that changed path, newest
// first, at most limit (at most 100).
func (c *Client) FileCommits(ctx context.Context, repo, path, branch string, limit int) ([]domain.Commit, error) {
	q := url.Values{"sha": {branch}, "path": {path}, "per_page": {fmt.Sprint(min(max(limit, 1), 100))}}
	var raw []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/commits?"+q.Encode(), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]domain.Commit, 0, len(raw))
	for _, r := range raw {
		cm := domain.Commit{SHA: r.SHA, Message: r.Commit.Message}
		if len(r.Parents) > 0 {
			cm.ParentSHA = r.Parents[0].SHA
		}
		out = append(out, cm)
	}
	return out, nil
}

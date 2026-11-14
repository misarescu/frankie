package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
)

// rawIssue is the part of a GitHub issue object intake reads. The list,
// sub-issue and blocked-by endpoints all return these.
type rawIssue struct {
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	HTMLURL       string    `json:"html_url"`
	State         string    `json:"state"`
	RepositoryURL string    `json:"repository_url"`
	PullRequest   *struct{} `json:"pull_request"`
	User          struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Type *struct {
		Name string `json:"name"`
	} `json:"type"`
	SubIssuesSummary *struct {
		Total int `json:"total"`
	} `json:"sub_issues_summary"`
	DependenciesSummary *struct {
		BlockedBy int `json:"blocked_by"` // open blockers only
	} `json:"issue_dependencies_summary"`
}

func (r rawIssue) external() domain.ExternalIssue {
	e := domain.ExternalIssue{Ref: r.HTMLURL, Repo: repoFromURL(r.RepositoryURL), Number: r.Number, Title: r.Title,
		Body: r.Body, Author: r.User.Login, Open: r.State == "open"}
	for _, l := range r.Labels {
		e.Labels = append(e.Labels, l.Name)
	}
	if r.Type != nil {
		e.SourceType = r.Type.Name
	}
	if r.SubIssuesSummary != nil {
		e.SubIssues = r.SubIssuesSummary.Total
	}
	if r.DependenciesSummary != nil {
		e.OpenBlockers = r.DependenciesSummary.BlockedBy
	}
	return e
}

// repoFromURL turns "https://api.github.com/repos/owner/name" into "owner/name".
func repoFromURL(u string) string {
	_, after, ok := strings.Cut(u, "/repos/")
	if !ok {
		return ""
	}
	return after
}

// ListLabeledIssues returns the open issues (not pull requests) of repo
// ("owner/name") that carry label.
func (c *Client) ListLabeledIssues(ctx context.Context, repo, label string) ([]domain.ExternalIssue, error) {
	q := url.Values{"state": {"open"}, "labels": {label}, "per_page": {"100"}}
	return c.issues(ctx, "/repos/"+repo+"/issues?"+q.Encode())
}

// SubIssues returns an issue's sub-issues, open and closed (GitHub allows at most
// 100 per parent).
func (c *Client) SubIssues(ctx context.Context, repo string, number int) ([]domain.ExternalIssue, error) {
	return c.issues(ctx, fmt.Sprintf("/repos/%s/issues/%d/sub_issues?per_page=100", repo, number))
}

// BlockedBy returns the issues an issue is blocked by, open and closed, possibly
// in other repositories.
func (c *Client) BlockedBy(ctx context.Context, repo string, number int) ([]domain.ExternalIssue, error) {
	return c.issues(ctx, fmt.Sprintf("/repos/%s/issues/%d/dependencies/blocked_by?per_page=100", repo, number))
}

func (c *Client) issues(ctx context.Context, path string) ([]domain.ExternalIssue, error) {
	var raw []rawIssue
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]domain.ExternalIssue, 0, len(raw))
	for _, r := range raw {
		if r.PullRequest != nil {
			continue
		}
		out = append(out, r.external())
	}
	return out, nil
}

// IssueNumber reads the issue number from a GitHub issue URL
// ("https://github.com/owner/name/issues/42"), the source ref intake stores.
func IssueNumber(ref string) (int, bool) {
	_, n, ok := parseIssueRef(ref)
	return n, ok
}

// parseIssueRef reads the repo ("owner/name") and number from a GitHub issue URL.
func parseIssueRef(ref string) (repo string, number int, ok bool) {
	u, err := url.Parse(ref)
	if err != nil {
		return "", 0, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "issues" || parts[0] == "" || parts[1] == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return parts[0] + "/" + parts[1], n, true
}

// issuePath is the API path of the issue a source ref names.
func issuePath(ref string) (string, error) {
	repo, n, ok := parseIssueRef(ref)
	if !ok {
		return "", fmt.Errorf("github: %q is not a GitHub issue URL", ref)
	}
	return fmt.Sprintf("/repos/%s/issues/%d", repo, n), nil
}

// Get reads the issue a source ref names (IssueSource.Get, docs/spec/02).
func (c *Client) Get(ctx context.Context, ref string) (domain.ExternalIssue, error) {
	path, err := issuePath(ref)
	if err != nil {
		return domain.ExternalIssue{}, err
	}
	var raw rawIssue
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		var se *StatusError
		if errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code == http.StatusGone) {
			return domain.ExternalIssue{}, fmt.Errorf("%w: %s: %w", domain.ErrExternalIssueGone, ref, err)
		}
		return domain.ExternalIssue{}, err
	}
	return raw.external(), nil
}

// UpdateStatus mirrors a factory status on the issue (IssueSource.UpdateStatus,
// D71): merged closes it as completed, canceled as not planned. Closing a
// closed issue is harmless. Other statuses leave GitHub as it is.
func (c *Client) UpdateStatus(ctx context.Context, ref string, status domain.IssueStatus) error {
	var reason string
	switch status {
	case domain.IssueMerged, domain.IssueReleased:
		reason = "completed"
	case domain.IssueCanceled:
		reason = "not_planned"
	default:
		return nil
	}
	path, err := issuePath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPatch, path, map[string]string{"state": "closed", "state_reason": reason}, nil)
}

// Comment adds a comment to the issue (IssueSource.Comment).
func (c *Client) Comment(ctx context.Context, ref, body string) error {
	path, err := issuePath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path+"/comments", map[string]string{"body": body}, nil)
}

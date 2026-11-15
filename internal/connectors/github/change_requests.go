package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/misarescu/frankie/internal/domain"
)

// pullRequest is the part of a pull request the merge queue reads.
type pullRequest struct {
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

func (c *Client) pullRequest(ctx context.Context, repo string, number int) (pullRequest, error) {
	var pr pullRequest
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), nil, &pr)
	return pr, err
}

// MergeChangeRequest merges pull request number of repo with the request's
// method, only if its head is still the request's HeadSHA (D85), and returns
// the commit it put on the base branch. A head that moved is
// domain.ErrHeadMoved; a merge GitHub refuses (conflict, branch protection,
// closed) is domain.ErrNotMergeable. A merge that already happened at that
// head, e.g. before an Activity was retried, returns its commit.
func (c *Client) MergeChangeRequest(ctx context.Context, repo string, number int, req domain.MergeRequest) (string, error) {
	if err := req.Validate(); err != nil {
		return "", err
	}
	body := map[string]any{"sha": req.HeadSHA, "merge_method": string(req.Method)}
	if req.Title != "" {
		body["commit_title"] = req.Title
	}
	if req.Message != "" {
		body["commit_message"] = req.Message
	}
	var out struct {
		SHA    string `json:"sha"`
		Merged bool   `json:"merged"`
	}
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number), body, &out)
	var se *StatusError
	switch {
	case err == nil && out.Merged && out.SHA != "":
		return out.SHA, nil
	case err == nil:
		return "", fmt.Errorf("github: merge %s#%d: not merged", repo, number)
	case errors.As(err, &se) && se.Code == http.StatusConflict:
		return "", fmt.Errorf("github: merge %s#%d: %w: %s", repo, number, domain.ErrHeadMoved, se.Body)
	case errors.As(err, &se) && (se.Code == http.StatusMethodNotAllowed || se.Code == http.StatusUnprocessableEntity):
		pr, perr := c.pullRequest(ctx, repo, number)
		if perr == nil && pr.Merged && pr.Head.SHA == req.HeadSHA && pr.MergeCommitSHA != "" {
			return pr.MergeCommitSHA, nil
		}
		return "", fmt.Errorf("github: merge %s#%d: %w: %s", repo, number, domain.ErrNotMergeable, se.Body)
	default:
		return "", err
	}
}

// CloseChangeRequest closes pull request number of repo without merging it,
// after commenting why (D85). A closed one is left as it is, so a retry does
// not comment twice; a merged one cannot be closed.
func (c *Client) CloseChangeRequest(ctx context.Context, repo string, number int, comment string) error {
	pr, err := c.pullRequest(ctx, repo, number)
	switch {
	case err != nil:
		return err
	case pr.Merged:
		return fmt.Errorf("github: close %s#%d: already merged", repo, number)
	case pr.State == "closed":
		return nil
	}
	if comment != "" {
		if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number), map[string]string{"body": comment}, nil); err != nil {
			return err
		}
	}
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), map[string]string{"state": "closed"}, nil)
}

// maxCheckPages bounds the check or workflow runs read for one commit (100 per page).
const maxCheckPages = 10

// CommitStatus returns the provider checks of commit sha in repo (D85, D86):
// its latest check runs and its commit statuses, and their combined state. A
// fine-grained personal access token cannot read check runs (there is no
// Checks permission for one), so when GitHub refuses them the commit's GitHub
// Actions workflow runs stand in; checks that other apps report are then not
// seen.
func (c *Client) CommitStatus(ctx context.Context, repo, sha string) (domain.CommitStatus, error) {
	out := domain.CommitStatus{SHA: sha}
	ref := url.PathEscape(sha)
	checks, err := c.checkRuns(ctx, repo, ref)
	var se *StatusError
	if errors.As(err, &se) && (se.Code == http.StatusForbidden || se.Code == http.StatusNotFound) {
		checks, err = c.workflowRuns(ctx, repo, sha)
	}
	if err != nil {
		return domain.CommitStatus{}, err
	}
	out.Checks = checks
	var combined struct {
		Statuses []struct {
			Context     string `json:"context"`
			State       string `json:"state"`
			TargetURL   string `json:"target_url"`
			Description string `json:"description"`
		} `json:"statuses"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/commits/"+ref+"/status?per_page=100", nil, &combined); err != nil {
		return domain.CommitStatus{}, err
	}
	for _, s := range combined.Statuses {
		out.Checks = append(out.Checks, domain.Check{Name: s.Context, State: statusState(s.State), URL: s.TargetURL, Summary: s.Description})
	}
	out.State = domain.CombineChecks(out.Checks)
	return out, nil
}

// checkRuns reads the commit's latest check runs (GitHub Actions jobs and
// other apps' checks).
func (c *Client) checkRuns(ctx context.Context, repo, ref string) ([]domain.Check, error) {
	var checks []domain.Check
	for page := 1; page <= maxCheckPages; page++ {
		var resp struct {
			TotalCount int `json:"total_count"`
			CheckRuns  []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				HTMLURL    string `json:"html_url"`
				Output     struct {
					Title string `json:"title"`
				} `json:"output"`
			} `json:"check_runs"`
		}
		q := url.Values{"per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/commits/"+ref+"/check-runs?"+q.Encode(), nil, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.CheckRuns {
			checks = append(checks, domain.Check{Name: r.Name, State: checkRunState(r.Status, r.Conclusion), URL: r.HTMLURL, Summary: r.Output.Title})
		}
		if len(resp.CheckRuns) == 0 || len(checks) >= resp.TotalCount {
			break
		}
	}
	return checks, nil
}

// workflowRuns reads the commit's GitHub Actions workflow runs (Actions:
// read), the latest per workflow and triggering event.
func (c *Client) workflowRuns(ctx context.Context, repo, sha string) ([]domain.Check, error) {
	var checks []domain.Check
	seen, read := map[string]bool{}, 0
	for page := 1; page <= maxCheckPages; page++ {
		var resp struct {
			TotalCount   int `json:"total_count"`
			WorkflowRuns []struct {
				Name       string `json:"name"`
				WorkflowID int64  `json:"workflow_id"`
				Event      string `json:"event"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				HTMLURL    string `json:"html_url"`
			} `json:"workflow_runs"`
		}
		q := url.Values{"head_sha": {sha}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/actions/runs?"+q.Encode(), nil, &resp); err != nil {
			return nil, err
		}
		// Newest first: an older run of the same workflow and event was superseded.
		for _, r := range resp.WorkflowRuns {
			key := fmt.Sprintf("%d/%s", r.WorkflowID, r.Event)
			if seen[key] {
				continue
			}
			seen[key] = true
			checks = append(checks, domain.Check{Name: r.Name, State: checkRunState(r.Status, r.Conclusion), URL: r.HTMLURL})
		}
		read += len(resp.WorkflowRuns)
		if len(resp.WorkflowRuns) == 0 || read >= resp.TotalCount {
			break
		}
	}
	return checks, nil
}

// checkRunState maps a check run: done when completed, passed when it
// succeeded or did not apply (neutral, skipped).
func checkRunState(status, conclusion string) domain.CheckState {
	if status != "completed" {
		return domain.CheckPending
	}
	switch conclusion {
	case "success", "neutral", "skipped":
		return domain.CheckSuccess
	}
	return domain.CheckFailure // failure, timed_out, cancelled, action_required, startup_failure, stale
}

func statusState(state string) domain.CheckState {
	switch state {
	case "success":
		return domain.CheckSuccess
	case "failure", "error":
		return domain.CheckFailure
	}
	return domain.CheckPending
}

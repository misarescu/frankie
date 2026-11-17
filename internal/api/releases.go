package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// Releases for the releases page (D130): a project's releases with their
// stages, and one release with its notes, deployments, verifications and the
// release agent's runs.

const (
	defaultReleasesLimit = 50
	maxReleasesLimit     = 200
)

// releaseSummaryJSON is a release as the releases page lists it.
type releaseSummaryJSON struct {
	ID      uuid.UUID `json:"id"`
	Repo    string    `json:"repo"`
	SHA     string    `json:"sha"`
	Status  string    `json:"status"`
	Reason  string    `json:"reason,omitempty"`
	Version string    `json:"version,omitempty"`
	Bump    string    `json:"bump,omitempty"`
	// Issues are the keys of the issues it contains, in the order they merged.
	Issues     []string          `json:"issues"`
	Stages     []stageStatusJSON `json:"stages"`
	CreatedAt  time.Time         `json:"created_at"`
	FinishedAt *time.Time        `json:"finished_at,omitempty"`
}

type stageStatusJSON struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
}

// releaseDetailJSON is one release with everything recorded about it.
type releaseDetailJSON struct {
	releaseSummaryJSON
	Project            string             `json:"project"`
	Notes              string             `json:"notes,omitempty"`
	NotesSource        string             `json:"notes_source,omitempty"`
	ProviderReleaseURL string             `json:"provider_release_url,omitempty"`
	PublishError       string             `json:"publish_error,omitempty"`
	BugIssueRef        string             `json:"bug_issue_ref,omitempty"`
	IssueDetails       []releaseIssueJSON `json:"issue_details"`
	Deployments        []deploymentJSON   `json:"deployments"`
	Runs               []releaseRunJSON   `json:"runs"`
	CostUSD            float64            `json:"cost_usd"`
}

type releaseIssueJSON struct {
	Key    string `json:"key"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type deploymentJSON struct {
	Stage        string               `json:"stage"`
	Driver       string               `json:"driver"`
	Status       string               `json:"status"`
	Reason       string               `json:"reason,omitempty"`
	Verification *domain.Verification `json:"verification,omitempty"`
	StartedAt    *time.Time           `json:"started_at,omitempty"`
	FinishedAt   *time.Time           `json:"finished_at,omitempty"`
}

// releaseRunJSON is a run of the release agent (D129); its steps are the
// agent's sessions, e.g. notes and analysis.
type releaseRunJSON struct {
	ID        uuid.UUID `json:"id"`
	Status    string    `json:"status"`
	Steps     []string  `json:"steps"`
	CostUSD   float64   `json:"cost_usd"`
	CreatedAt time.Time `json:"created_at"`
}

// listReleases lists one project's releases, newest first:
// GET /api/releases?project=<name>[&limit=<n>].
func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.URL.Query().Get("project")
	if name == "" {
		http.Error(w, "project is required, e.g. ?project=misarescu/frankie", http.StatusBadRequest)
		return
	}
	limit := defaultReleasesLimit
	if v, ok := r.URL.Query()["limit"]; ok {
		n, err := strconv.Atoi(v[0])
		if err != nil || n < 1 || n > maxReleasesLimit {
			http.Error(w, "limit must be an integer from 1 to 200", http.StatusBadRequest)
			return
		}
		limit = n
	}
	p, err := s.Store.GetProjectByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such project", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	rels, err := s.Store.ListReleases(ctx, p.ID, limit)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	issues, err := s.projectIssues(ctx, p.ID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := make([]releaseSummaryJSON, 0, len(rels))
	for _, rel := range rels {
		deps, err := s.Store.ListDeployments(ctx, rel.ID)
		if err != nil {
			http.Error(w, "store error", http.StatusInternalServerError)
			return
		}
		out = append(out, releaseSummary(rel, deps, issues))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// getRelease returns one release: GET /api/releases/{id}.
func (s *Server) getRelease(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "no such release", http.StatusNotFound)
		return
	}
	rel, err := s.Store.GetRelease(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such release", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out, err := s.releaseDetail(ctx, rel)
	if err != nil {
		s.Log.Warn("release detail", "release", rel.ID, "error", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) releaseDetail(ctx context.Context, rel domain.Release) (releaseDetailJSON, error) {
	p, err := s.Store.GetProject(ctx, rel.ProjectID)
	if err != nil {
		return releaseDetailJSON{}, err
	}
	issues, err := s.projectIssues(ctx, rel.ProjectID)
	if err != nil {
		return releaseDetailJSON{}, err
	}
	deps, err := s.Store.ListDeployments(ctx, rel.ID)
	if err != nil {
		return releaseDetailJSON{}, err
	}
	out := releaseDetailJSON{releaseSummaryJSON: releaseSummary(rel, deps, issues), Project: p.Name,
		Notes: rel.Notes, NotesSource: string(rel.NotesSource), ProviderReleaseURL: rel.ProviderReleaseURL,
		PublishError: rel.PublishError, BugIssueRef: rel.BugIssueRef,
		IssueDetails: []releaseIssueJSON{}, Deployments: make([]deploymentJSON, 0, len(deps)), Runs: []releaseRunJSON{}}
	for _, id := range rel.IssueIDs {
		if is, ok := issues[id]; ok {
			out.IssueDetails = append(out.IssueDetails, releaseIssueJSON{Key: is.Key, Type: string(is.Type), Title: is.Title, Status: string(is.Status)})
		}
	}
	for _, d := range deps {
		out.Deployments = append(out.Deployments, deploymentJSON{Stage: d.Stage, Driver: d.Driver, Status: string(d.Status),
			Reason: d.Reason, Verification: d.Verification, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt})
	}
	runs, err := s.Store.ListReleaseRuns(ctx, rel.ID)
	if err != nil {
		return out, err
	}
	for _, run := range runs {
		steps, err := s.Store.ListStepRuns(ctx, run.ID)
		if err != nil {
			return out, err
		}
		rr := releaseRunJSON{ID: run.ID, Status: string(run.Status), Steps: make([]string, 0, len(steps)), CreatedAt: run.CreatedAt}
		for _, st := range steps {
			rr.Steps = append(rr.Steps, st.StepID)
			rr.CostUSD += st.Usage.CostUSD
		}
		out.CostUSD += rr.CostUSD
		out.Runs = append(out.Runs, rr)
	}
	return out, nil
}

// projectIssues maps a project's issues by ID, to name a release's issues.
func (s *Server) projectIssues(ctx context.Context, projectID uuid.UUID) (map[uuid.UUID]domain.Issue, error) {
	list, err := s.Store.ListProjectIssues(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]domain.Issue, len(list))
	for _, is := range list {
		out[is.ID] = is.Issue
	}
	return out, nil
}

func releaseSummary(rel domain.Release, deps []domain.Deployment, issues map[uuid.UUID]domain.Issue) releaseSummaryJSON {
	out := releaseSummaryJSON{ID: rel.ID, Repo: rel.Repo, SHA: rel.SHA, Status: string(rel.Status), Reason: rel.Reason,
		Version: rel.Version, Bump: string(rel.Bump), Issues: make([]string, 0, len(rel.IssueIDs)),
		Stages: make([]stageStatusJSON, 0, len(deps)), CreatedAt: rel.CreatedAt, FinishedAt: rel.FinishedAt}
	for _, id := range rel.IssueIDs {
		if is, ok := issues[id]; ok {
			out.Issues = append(out.Issues, is.Key)
		}
	}
	for _, d := range deps {
		out.Stages = append(out.Stages, stageStatusJSON{Stage: d.Stage, Status: string(d.Status)})
	}
	return out
}

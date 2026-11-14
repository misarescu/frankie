package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// issueSummaryJSON is an issue as the board lists it (D59). Parent and
// dependencies are keys of issues in the same project, so a client can draw
// the hierarchy and the dependency graph from one list.
type issueSummaryJSON struct {
	Key       string      `json:"key"`
	Number    int         `json:"number"`
	Type      string      `json:"type"`
	Title     string      `json:"title"`
	Status    string      `json:"status"`
	Priority  int         `json:"priority"`
	Parent    string      `json:"parent,omitempty"`
	DependsOn []string    `json:"depends_on"`
	Labels    []string    `json:"labels"`
	SourceRef string      `json:"source_ref,omitempty"`
	CostUSD   float64     `json:"cost_usd"`
	LatestRun *runRefJSON `json:"latest_run,omitempty"`
	// StartRequestedAt is set once someone asked to start the issue itself (D62).
	StartRequestedAt *time.Time `json:"start_requested_at,omitempty"`
}

type runRefJSON struct {
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status"`
}

// listIssues lists one project's issues, highest priority first: GET /api/issues?project=<name>.
func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("project")
	if name == "" {
		http.Error(w, "project is required, e.g. ?project=misarescu/frankie", http.StatusBadRequest)
		return
	}
	p, err := s.Store.GetProjectByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such project", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	issues, err := s.Store.ListProjectIssues(r.Context(), p.ID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	deps, err := s.Store.ProjectDependencies(r.Context(), p.ID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	byID := make(map[uuid.UUID]store.IssueSummary, len(issues))
	for _, is := range issues {
		byID[is.ID] = is
	}
	dependsOn := make(map[uuid.UUID][]store.IssueSummary)
	for _, d := range deps {
		dependsOn[d.IssueID] = append(dependsOn[d.IssueID], byID[d.DependsOnID])
	}
	out := make([]issueSummaryJSON, len(issues))
	for i, is := range issues {
		js := issueSummaryJSON{Key: is.Key, Number: is.Number, Type: string(is.Type), Title: is.Title, Status: string(is.Status),
			Priority: is.Priority, DependsOn: []string{}, Labels: nonNilStrings(is.Labels),
			SourceRef: is.SourceRef, CostUSD: is.CostUSD, StartRequestedAt: is.StartRequestedAt}
		deps := dependsOn[is.ID]
		slices.SortFunc(deps, func(a, b store.IssueSummary) int { return a.Number - b.Number })
		for _, d := range deps {
			js.DependsOn = append(js.DependsOn, d.Key)
		}
		if is.ParentID != nil {
			js.Parent = byID[*is.ParentID].Key
		}
		if is.LatestRunID != uuid.Nil {
			js.LatestRun = &runRefJSON{ID: is.LatestRunID, Status: string(is.LatestRunStatus)}
		}
		out[i] = js
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// issueRefJSON names a related issue on the issue page.
type issueRefJSON struct {
	Key    string `json:"key"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type issueRunJSON struct {
	ID         uuid.UUID  `json:"id"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CostUSD    float64    `json:"cost_usd"`
}

type usageJSON struct {
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

type changeRequestJSON struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Branch  string `json:"branch"`
	HeadSHA string `json:"head_sha"`
	Status  string `json:"status"`
}

// issueDetailJSON is the issue page (D59).
type issueDetailJSON struct {
	Key                string              `json:"key"`
	Number             int                 `json:"number"`
	Project            string              `json:"project"`
	Type               string              `json:"type"`
	Title              string              `json:"title"`
	Status             string              `json:"status"`
	Priority           int                 `json:"priority"`
	Spec               string              `json:"spec"`
	AcceptanceCriteria []string            `json:"acceptance_criteria"`
	Labels             []string            `json:"labels"`
	SourceRef          string              `json:"source_ref,omitempty"`
	CreatedBy          string              `json:"created_by"`
	StartRequestedAt   *time.Time          `json:"start_requested_at,omitempty"`
	StartRequestedBy   string              `json:"start_requested_by,omitempty"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
	Parent             *issueRefJSON       `json:"parent,omitempty"`
	Children           []issueRefJSON      `json:"children"`
	DependsOn          []issueRefJSON      `json:"depends_on"`
	Dependents         []issueRefJSON      `json:"dependents"`
	Runs               []issueRunJSON      `json:"runs"`
	Usage              usageJSON           `json:"usage"`
	ChangeRequests     []changeRequestJSON `json:"change_requests"`
	// FileHints and ChangedPaths are what overlap warnings compare (D63);
	// Overlaps lists the in-flight issues of the project this one likely collides with.
	FileHints    []string      `json:"file_hints"`
	ChangedPaths []string      `json:"changed_paths"`
	Overlaps     []overlapJSON `json:"overlaps"`
}

// getIssue returns one issue with its relations, runs and usage: GET /api/issues/{key}.
func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	is, err := s.Store.GetIssueByKey(ctx, r.PathValue("key"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such issue", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out, err := s.issueDetail(ctx, is)
	if err != nil {
		s.Log.Warn("issue detail", "issue", is.Key, "error", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) issueDetail(ctx context.Context, is domain.Issue) (issueDetailJSON, error) {
	out := issueDetailJSON{Key: is.Key, Number: is.Number, Type: string(is.Type), Title: is.Title, Status: string(is.Status),
		Priority: is.Priority, Spec: is.Spec, AcceptanceCriteria: nonNilStrings(is.AcceptanceCriteria), Labels: nonNilStrings(is.Labels),
		SourceRef: is.SourceRef, CreatedBy: is.CreatedBy, CreatedAt: is.CreatedAt, UpdatedAt: is.UpdatedAt,
		StartRequestedAt: is.StartRequestedAt, StartRequestedBy: is.StartRequestedBy}
	p, err := s.Store.GetProject(ctx, is.ProjectID)
	if err != nil {
		return out, err
	}
	out.Project = p.Name
	if is.ParentID != nil {
		parent, err := s.Store.GetIssue(ctx, *is.ParentID)
		if err != nil {
			return out, err
		}
		ref := issueRef(parent)
		out.Parent = &ref
	}
	for _, rel := range []struct {
		list func() ([]domain.Issue, error)
		into *[]issueRefJSON
	}{
		{func() ([]domain.Issue, error) { return s.Store.ListIssueChildren(ctx, is.ID) }, &out.Children},
		{func() ([]domain.Issue, error) { return s.Store.ListIssueDependencies(ctx, is.ID) }, &out.DependsOn},
		{func() ([]domain.Issue, error) { return s.Store.ListIssueDependents(ctx, is.ID) }, &out.Dependents},
	} {
		issues, err := rel.list()
		if err != nil {
			return out, err
		}
		*rel.into = make([]issueRefJSON, len(issues))
		for i, x := range issues {
			(*rel.into)[i] = issueRef(x)
		}
	}
	runs, err := s.Store.ListIssueRuns(ctx, is.ID)
	if err != nil {
		return out, err
	}
	out.Runs = make([]issueRunJSON, len(runs))
	for i, run := range runs {
		out.Runs[i] = issueRunJSON{ID: run.ID, Status: string(run.Status), CreatedAt: run.CreatedAt,
			StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, CostUSD: run.CostUSD}
	}
	u, err := s.Store.IssueUsage(ctx, is.ID)
	if err != nil {
		return out, err
	}
	out.Usage = usageJSON{CostUSD: u.CostUSD, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
	crs, err := s.Store.ListChangeRequestsForIssue(ctx, is.ID)
	if err != nil {
		return out, err
	}
	out.FileHints, out.ChangedPaths = nonNilStrings(is.FileHints), nonNilStrings(is.ChangedPaths)
	if out.Overlaps, err = s.overlaps(ctx, map[string]bool{is.Key: true}); err != nil {
		return out, err
	}
	out.ChangeRequests = make([]changeRequestJSON, len(crs))
	for i, cr := range crs {
		out.ChangeRequests[i] = changeRequestJSON{Repo: cr.Repo, Number: cr.Number, URL: cr.ProviderRef,
			Branch: cr.Branch, HeadSHA: cr.HeadSHA, Status: cr.Status}
	}
	return out, nil
}

func issueRef(is domain.Issue) issueRefJSON {
	return issueRefJSON{Key: is.Key, Type: string(is.Type), Title: is.Title, Status: string(is.Status)}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

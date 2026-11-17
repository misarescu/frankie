package api

import (
	"net/http"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

const releaseSHA = "0123456789abcdef0123456789abcdef01234567"

// releasedIssue records a merged issue of the test project and a succeeded
// release of its merge, deployed to staging and verified, with the release
// agent's notes and a version, and the agent's run with its notes session.
func releasedIssue(t *testing.T, st *store.Store) (domain.Issue, domain.Release) {
	t.Helper()
	ctx := t.Context()
	p, err := st.GetProjectByName(ctx, "misarescu/Goodreads-Clone")
	if err != nil {
		t.Fatal(err)
	}
	is, err := st.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueBug, Title: "Fix search", Status: domain.IssueAwaitingHuman, CreatedBy: "t"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateRun(ctx, store.NewRun{IssueID: is.ID, WorkflowSnapshot: []byte(`{}`), WorkflowVersion: "1", TemporalWorkflowID: "issue-" + is.Key})
	if err != nil {
		t.Fatal(err)
	}
	cr, err := st.UpsertChangeRequest(ctx, store.NewChangeRequest{IssueID: is.ID, RunID: run.ID, Repo: "misarescu/Goodreads-Clone", Number: 7,
		ProviderRef: "https://github.com/misarescu/Goodreads-Clone/pull/7", Branch: "agent/" + is.Key + "/x", HeadSHA: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordChangeRequestMerged(ctx, cr.ID, releaseSHA); err != nil {
		t.Fatal(err)
	}
	rel, _, err := st.CreateRelease(ctx, store.NewRelease{ProjectID: p.ID, Repo: "misarescu/Goodreads-Clone", SHA: releaseSHA})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateDeployment(ctx, store.NewDeployment{ReleaseID: rel.ID, Stage: "staging", Driver: "flux"})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]domain.ReleaseStatus{{domain.ReleasePending, domain.ReleaseInProgress}, {domain.ReleaseInProgress, domain.ReleaseSucceeded}} {
		if _, err := st.TransitionDeployment(ctx, dep.ID, step[0], step[1], ""); err != nil {
			t.Fatal(err)
		}
		if _, err := st.TransitionRelease(ctx, rel.ID, step[0], step[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.SetDeploymentVerification(ctx, dep.ID, domain.Verification{Passed: true, Summary: "280 tests passed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetReleaseNotes(ctx, rel.ID, domain.BumpPatch, "- Fix search ("+is.Key+")", domain.NotesByAgent); err != nil {
		t.Fatal(err)
	}
	if rel, err = st.SetReleaseVersion(ctx, rel.ID, domain.FirstVersion, "https://github.com/misarescu/Goodreads-Clone/releases/tag/v0.1.0", ""); err != nil {
		t.Fatal(err)
	}
	agent, err := st.CreateRun(ctx, store.NewRun{ReleaseID: rel.ID, WorkflowSnapshot: []byte(`{}`), WorkflowVersion: "1", TemporalWorkflowID: "release/misarescu/Goodreads-Clone"})
	if err != nil {
		t.Fatal(err)
	}
	notes, err := st.CreateStepRun(ctx, store.NewStepRun{RunID: agent.ID, StepID: "notes", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddStepRunUsage(ctx, notes.ID, domain.Usage{CostUSD: 0.05}); err != nil {
		t.Fatal(err)
	}
	return is, rel
}

// M7, D130: the releases page lists a project's releases with their version,
// issues and stages.
func TestListReleases(t *testing.T) {
	st, srv, _, _ := testServer(t)
	is, rel := releasedIssue(t, st)

	var got []releaseSummaryJSON
	if code := getJSON(t, srv.URL+"/api/releases?project=misarescu/Goodreads-Clone", &got); code != http.StatusOK {
		t.Fatalf("GET /api/releases = %d", code)
	}
	if len(got) != 1 {
		t.Fatalf("releases = %+v, want one", got)
	}
	r := got[0]
	if r.ID != rel.ID || r.SHA != releaseSHA || r.Status != "succeeded" || r.Version != "v0.1.0" || r.Bump != "patch" {
		t.Errorf("release = %+v", r)
	}
	if len(r.Issues) != 1 || r.Issues[0] != is.Key {
		t.Errorf("issues = %v, want [%s]", r.Issues, is.Key)
	}
	if len(r.Stages) != 1 || r.Stages[0] != (stageStatusJSON{Stage: "staging", Status: "succeeded"}) {
		t.Errorf("stages = %+v", r.Stages)
	}

	for url, want := range map[string]int{
		"/api/releases":                                           http.StatusBadRequest,
		"/api/releases?project=misarescu/none":                    http.StatusNotFound,
		"/api/releases?project=misarescu/Goodreads-Clone&limit=0": http.StatusBadRequest,
	} {
		if code := getJSON(t, srv.URL+url, nil); code != want {
			t.Errorf("GET %s = %d, want %d", url, code, want)
		}
	}
}

// M7, D129, D130: a release's page shows its notes, version, deployments with
// their verification, issues and the release agent's runs.
func TestGetRelease(t *testing.T) {
	st, srv, _, _ := testServer(t)
	is, rel := releasedIssue(t, st)

	var got releaseDetailJSON
	if code := getJSON(t, srv.URL+"/api/releases/"+rel.ID.String(), &got); code != http.StatusOK {
		t.Fatalf("GET /api/releases/{id} = %d", code)
	}
	if got.Project != "misarescu/Goodreads-Clone" || got.Version != "v0.1.0" || got.NotesSource != "agent" ||
		got.Notes != "- Fix search ("+is.Key+")" || got.ProviderReleaseURL == "" {
		t.Errorf("release = %+v", got)
	}
	if len(got.IssueDetails) != 1 || got.IssueDetails[0] != (releaseIssueJSON{Key: is.Key, Type: "bug", Title: "Fix search", Status: "released"}) {
		t.Errorf("issues = %+v", got.IssueDetails)
	}
	if len(got.Deployments) != 1 {
		t.Fatalf("deployments = %+v", got.Deployments)
	}
	d := got.Deployments[0]
	if d.Stage != "staging" || d.Status != "succeeded" || d.Verification == nil || !d.Verification.Passed || d.Verification.Summary != "280 tests passed" {
		t.Errorf("deployment = %+v, verification %+v", d, d.Verification)
	}
	if len(got.Runs) != 1 || len(got.Runs[0].Steps) != 1 || got.Runs[0].Steps[0] != "notes" || got.Runs[0].CostUSD != 0.05 || got.CostUSD != 0.05 {
		t.Errorf("runs = %+v, cost %v", got.Runs, got.CostUSD)
	}

	for _, id := range []string{"nope", "00000000-0000-0000-0000-000000000000"} {
		if code := getJSON(t, srv.URL+"/api/releases/"+id, nil); code != http.StatusNotFound {
			t.Errorf("GET /api/releases/%s = %d, want 404", id, code)
		}
	}
}

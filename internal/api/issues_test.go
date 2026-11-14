package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// boardFixture adds an epic with three tasks to the test server's project:
// gc-2 epic > gc-3, gc-4, gc-5, where gc-4 and gc-5 wait for gc-3, and gc-5 for
// the running gc-1 as well. gc-1 (from testServer) has one run.
func boardFixture(t *testing.T, st *store.Store, run domain.Run) map[string]domain.Issue {
	t.Helper()
	ctx := t.Context()
	running, err := st.GetIssue(ctx, run.IssueID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.GetProject(ctx, running.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(typ domain.IssueType, title string, parent *domain.Issue) domain.Issue {
		in := store.NewIssue{ProjectID: p.ID, Type: typ, Title: title, CreatedBy: "t", Labels: []string{"factory"}}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		is, err := st.CreateIssue(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return is
	}
	epic := mk(domain.IssueEpic, "Search", nil)
	api := mk(domain.IssueTask, "Search API", &epic)
	ui := mk(domain.IssueTask, "Search UI", &epic)
	docs := mk(domain.IssueTask, "Search docs", &epic)
	for _, e := range [][2]domain.Issue{{ui, api}, {docs, api}, {docs, running}} {
		if err := st.AddIssueDependency(ctx, e[0].ID, e[1].ID); err != nil {
			t.Fatal(err)
		}
	}
	return map[string]domain.Issue{"running": running, "epic": epic, "api": api, "ui": ui, "docs": docs}
}

// D59: one list carries what the board needs to draw the hierarchy and the DAG.
func TestListIssues(t *testing.T) {
	st, srv, run, _ := testServer(t)
	is := boardFixture(t, st, run)
	// gc-6 .. gc-10: a dependency on gc-10 sorts after gc-3 by number, not before it as text would.
	var tenth domain.Issue
	for range 5 {
		var err error
		tenth, err = st.CreateIssue(t.Context(), store.NewIssue{ProjectID: is["epic"].ProjectID, Type: domain.IssueBug, Title: "filler", CreatedBy: "t"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddIssueDependency(t.Context(), is["docs"].ID, tenth.ID); err != nil {
		t.Fatal(err)
	}

	var got []issueSummaryJSON
	if code := getJSON(t, srv.URL+"/api/issues?project=misarescu/Goodreads-Clone", &got); code != http.StatusOK {
		t.Fatalf("GET /api/issues = %d", code)
	}
	by := map[string]issueSummaryJSON{}
	for _, x := range got {
		by[x.Key] = x
	}
	if len(got) != 10 {
		t.Fatalf("got %d issues, want 10: %+v", len(got), got)
	}
	docs := by[is["docs"].Key]
	if docs.Parent != is["epic"].Key || docs.Type != "task" || docs.Status != "draft" ||
		!slices.Equal(docs.DependsOn, []string{is["running"].Key, is["api"].Key, "gc-10"}) || !slices.Equal(docs.Labels, []string{"factory"}) {
		t.Errorf("docs = %+v", docs)
	}
	if epic := by[is["epic"].Key]; epic.Parent != "" || epic.DependsOn == nil || len(epic.DependsOn) != 0 || epic.LatestRun != nil {
		t.Errorf("epic = %+v; want a root with no dependencies and no run", epic)
	}
	if r := by[is["running"].Key]; r.LatestRun == nil || r.LatestRun.ID != run.ID || r.LatestRun.Status != "running" {
		t.Errorf("running issue latest run = %+v, want %s running", r.LatestRun, run.ID)
	}

	for _, tt := range []struct {
		query string
		want  int
	}{
		{"", http.StatusBadRequest},
		{"?project=nope/nope", http.StatusNotFound},
	} {
		if code := getJSON(t, srv.URL+"/api/issues"+tt.query, nil); code != tt.want {
			t.Errorf("GET /api/issues%s = %d, want %d", tt.query, code, tt.want)
		}
	}
}

func TestGetIssue(t *testing.T) {
	st, srv, run, _ := testServer(t)
	is := boardFixture(t, st, run)
	ctx := t.Context()

	sr, err := st.CreateStepRun(ctx, store.NewStepRun{RunID: run.ID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddStepRunUsage(ctx, sr.ID, domain.Usage{CostUSD: 0.4, InputTokens: 1000, OutputTokens: 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertChangeRequest(ctx, store.NewChangeRequest{IssueID: run.IssueID, RunID: run.ID, Repo: "misarescu/Goodreads-Clone",
		Number: 7, ProviderRef: "https://github.com/misarescu/Goodreads-Clone/pull/7", Branch: "agent/gc-1/search", HeadSHA: "abc"}); err != nil {
		t.Fatal(err)
	}

	var running issueDetailJSON
	if code := getJSON(t, srv.URL+"/api/issues/"+is["running"].Key, &running); code != http.StatusOK {
		t.Fatalf("GET running issue = %d", code)
	}
	if running.Project != "misarescu/Goodreads-Clone" || running.Status != "running" || running.Title != "Add <b>search</b>" {
		t.Errorf("running = %+v", running)
	}
	if len(running.Runs) != 1 || running.Runs[0].ID != run.ID || running.Runs[0].CostUSD != 0.4 {
		t.Errorf("runs = %+v", running.Runs)
	}
	if running.Usage != (usageJSON{CostUSD: 0.4, InputTokens: 1000, OutputTokens: 50}) {
		t.Errorf("usage = %+v", running.Usage)
	}
	if len(running.ChangeRequests) != 1 || running.ChangeRequests[0].URL != "https://github.com/misarescu/Goodreads-Clone/pull/7" {
		t.Errorf("change requests = %+v", running.ChangeRequests)
	}
	if len(running.Dependents) != 1 || running.Dependents[0].Key != is["docs"].Key {
		t.Errorf("dependents = %+v, want [%s]", running.Dependents, is["docs"].Key)
	}

	var epic issueDetailJSON
	if code := getJSON(t, srv.URL+"/api/issues/"+is["epic"].Key, &epic); code != http.StatusOK {
		t.Fatalf("GET epic = %d", code)
	}
	if keys := refKeys(epic.Children); !slices.Equal(keys, []string{is["api"].Key, is["ui"].Key, is["docs"].Key}) {
		t.Errorf("epic children = %v", keys)
	}
	if epic.Parent != nil || epic.Runs == nil || len(epic.Runs) != 0 || epic.ChangeRequests == nil || epic.AcceptanceCriteria == nil {
		t.Errorf("epic = %+v; want no parent and empty (not null) lists", epic)
	}

	var api issueDetailJSON
	if code := getJSON(t, srv.URL+"/api/issues/"+is["api"].Key, &api); code != http.StatusOK {
		t.Fatalf("GET api task = %d", code)
	}
	if api.Parent == nil || api.Parent.Key != is["epic"].Key || api.Parent.Type != "epic" {
		t.Errorf("api parent = %+v", api.Parent)
	}
	if keys := refKeys(api.Dependents); !slices.Equal(keys, []string{is["ui"].Key, is["docs"].Key}) {
		t.Errorf("api dependents = %v", keys)
	}
	var docs issueDetailJSON
	getJSON(t, srv.URL+"/api/issues/"+is["docs"].Key, &docs)
	if keys := refKeys(docs.DependsOn); !slices.Equal(keys, []string{is["running"].Key, is["api"].Key}) {
		t.Errorf("docs depends on = %v", keys)
	}

	if code := getJSON(t, srv.URL+"/api/issues/gc-999", nil); code != http.StatusNotFound {
		t.Errorf("unknown issue = %d, want 404", code)
	}
}

func refKeys(refs []issueRefJSON) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Key
	}
	return out
}

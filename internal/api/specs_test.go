package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// refine stands in for the refiner on a draft: a refinement run that returned spec.
func refine(t *testing.T, st *store.Store, is domain.Issue, spec *domain.RefinedSpec) {
	t.Helper()
	ctx := t.Context()
	if _, err := st.TransitionIssue(ctx, is.ID, domain.IssueDraft, domain.IssueRefining); err != nil {
		t.Fatal(err)
	}
	r, err := st.CreateRun(ctx, store.NewRun{IssueID: is.ID, WorkflowSnapshot: []byte(`{}`), WorkflowVersion: "refine@1", TemporalWorkflowID: "refine-" + is.Key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionRun(ctx, r.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	if spec != nil {
		if err := st.RecordRefinement(ctx, is.ID, r.ID, *spec); err != nil {
			t.Fatal(err)
		}
	}
}

// D64, D66: once someone asks to start an epic, the refined specs of its issues
// that need approval wait in the inbox; approving applies one, rejecting drops it.
func TestSpecApprovalInbox(t *testing.T) {
	st, srv, run, _ := testServer(t)
	is := boardFixture(t, st, run) // epic > api, ui, docs, all drafts
	ctx := t.Context()
	running, _ := st.GetIssue(ctx, run.IssueID)
	if err := st.SetProjectSpecApproval(ctx, running.ProjectID, true); err != nil {
		t.Fatal(err)
	}
	proposal := func(k string) *domain.RefinedSpec {
		return &domain.RefinedSpec{Spec: "Refined " + k, AcceptanceCriteria: []string{k + " works"}, FileHints: []string{"src/" + k + "/"},
			Summary: "assumed " + k}
	}

	var specs []specJSON
	if _, err := st.RequestIssueStart(ctx, []uuid.UUID{is["epic"].ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	refine(t, st, is["api"], proposal("api"))
	refine(t, st, is["ui"], proposal("ui"))
	refine(t, st, is["docs"], nil) // the refiner is still working
	if code := getJSON(t, srv.URL+"/api/specs", &specs); code != http.StatusOK {
		t.Fatalf("GET /api/specs = %d", code)
	}
	keys := map[string]specJSON{}
	for _, sp := range specs {
		keys[sp.Key] = sp
	}
	api := keys[is["api"].Key]
	if len(specs) != 2 || api.Spec != "Refined api" || api.Summary != "assumed api" || api.OriginalSpec != "" ||
		api.RunID == "" || api.RequestedBy != "ana" || api.Project != "misarescu/Goodreads-Clone" {
		t.Fatalf("specs = %+v; want api and ui with their proposals", specs)
	}

	url := func(k string) string { return srv.URL + "/api/issues/" + is[k].Key + "/spec" }
	if code := postDecode(t, url("docs"), `{"decision":"approve"}`, nil); code != http.StatusConflict {
		t.Errorf("approving before the refiner returned = %d, want 409", code)
	}
	var got issueRefJSON
	if code := postDecode(t, url("api"), `{"decision":"approve","by":"ana"}`, &got); code != http.StatusOK || got.Status != "ready" {
		t.Fatalf("approve = %d, %+v", code, got)
	}
	var detail issueDetailJSON
	getJSON(t, srv.URL+"/api/issues/"+is["api"].Key, &detail)
	if detail.Spec != "Refined api" || detail.SpecApprovedBy != "ana" || detail.SpecProposal != nil ||
		!slices.Equal(detail.AcceptanceCriteria, []string{"api works"}) || !slices.Equal(detail.FileHints, []string{"src/api/"}) {
		t.Errorf("approved detail = %+v", detail)
	}

	if code := postDecode(t, url("ui"), `{"decision":"reject"}`, nil); code != http.StatusBadRequest {
		t.Errorf("reject without a comment = %d, want 400", code)
	}
	if code := postDecode(t, url("ui"), `{"decision":"reject","comment":"which page?","by":"ana"}`, &got); code != http.StatusOK || got.Status != "draft" {
		t.Fatalf("reject = %d, %+v", code, got)
	}
	getJSON(t, srv.URL+"/api/issues/"+is["ui"].Key, &detail)
	if detail.SpecFeedback != "which page? (ana)" || detail.SpecProposal != nil {
		t.Errorf("rejected detail: feedback %q, proposal %+v", detail.SpecFeedback, detail.SpecProposal)
	}
	// M5.2: the page sees the draft is held, so it can offer to refine again.
	if detail.SpecRejectedAt == nil {
		t.Error("rejected detail: not held")
	}
	if getJSON(t, srv.URL+"/api/specs", &specs); len(specs) != 0 {
		t.Fatalf("specs after deciding = %+v; want none", specs)
	}

	for _, tt := range []struct {
		name, url, body string
		want            int
	}{
		{"approve twice", url("api"), `{"decision":"approve"}`, http.StatusConflict},
		{"bad decision", url("docs"), `{"decision":"maybe"}`, http.StatusBadRequest},
		{"unknown issue", srv.URL + "/api/issues/gc-999/spec", `{"decision":"approve"}`, http.StatusNotFound},
	} {
		if code := postDecode(t, tt.url, tt.body, nil); code != tt.want {
			t.Errorf("%s = %d, want %d", tt.name, code, tt.want)
		}
	}
}

package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// D64: once someone asks to start an epic, its drafts that need approval wait in
// the inbox with their spec; approving makes one ready, rejecting holds it.
func TestSpecApprovalInbox(t *testing.T) {
	st, srv, run, _ := testServer(t)
	is := boardFixture(t, st, run) // epic > api, ui, docs, all drafts
	ctx := t.Context()
	running, _ := st.GetIssue(ctx, run.IssueID)
	if err := st.SetProjectSpecApproval(ctx, running.ProjectID, true); err != nil {
		t.Fatal(err)
	}
	epicID := is["epic"].ID
	skip, err := st.CreateIssue(ctx, store.NewIssue{ProjectID: running.ProjectID, ParentID: &epicID, Type: domain.IssueTask,
		Title: "skips", CreatedBy: "t", Labels: []string{"no-spec-approval"}})
	if err != nil {
		t.Fatal(err)
	}

	var specs []specJSON
	if code := getJSON(t, srv.URL+"/api/specs", &specs); code != http.StatusOK || len(specs) != 0 {
		t.Fatalf("specs before any request = %d, %+v; want none", code, specs)
	}
	if _, err := st.RequestIssueStart(ctx, []uuid.UUID{is["epic"].ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	getJSON(t, srv.URL+"/api/specs", &specs)
	keys := map[string]specJSON{}
	for _, sp := range specs {
		keys[sp.Key] = sp
	}
	if len(specs) != 3 || keys[is["api"].Key].Project != "misarescu/Goodreads-Clone" || keys[is["api"].Key].RequestedBy != "ana" {
		t.Fatalf("specs = %+v; want api, ui and docs, requested by ana through the epic", specs)
	}
	if _, ok := keys[skip.Key]; ok {
		t.Errorf("%s carries no-spec-approval but is listed", skip.Key)
	}

	url := func(k string) string { return srv.URL + "/api/issues/" + is[k].Key + "/spec" }
	var got issueRefJSON
	if code := postDecode(t, url("api"), `{"decision":"approve","by":"ana"}`, &got); code != http.StatusOK || got.Status != "ready" {
		t.Fatalf("approve = %d, %+v", code, got)
	}
	if code := postDecode(t, url("ui"), `{"decision":"reject"}`, nil); code != http.StatusBadRequest {
		t.Errorf("reject without a comment = %d, want 400", code)
	}
	if code := postDecode(t, url("ui"), `{"decision":"reject","comment":"which page?","by":"ana"}`, &got); code != http.StatusOK || got.Status != "draft" {
		t.Fatalf("reject = %d, %+v", code, got)
	}
	getJSON(t, srv.URL+"/api/specs", &specs)
	if len(specs) != 1 || specs[0].Key != is["docs"].Key {
		t.Fatalf("specs after deciding = %+v; want only docs", specs)
	}
	var detail issueDetailJSON
	getJSON(t, srv.URL+"/api/issues/"+is["ui"].Key, &detail)
	if detail.SpecFeedback != "which page? (ana)" {
		t.Errorf("feedback = %q", detail.SpecFeedback)
	}
	getJSON(t, srv.URL+"/api/issues/"+is["api"].Key, &detail)
	if detail.SpecApprovedBy != "ana" || detail.SpecApprovedAt == nil {
		t.Errorf("approval = %q at %v", detail.SpecApprovedBy, detail.SpecApprovedAt)
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

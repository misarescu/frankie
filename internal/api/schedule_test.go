package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// postDecode posts JSON with the test token and decodes a 2xx reply into out.
func postDecode(t *testing.T, url, body string, out any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode/100 == 2 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

// D62: starting an epic requests it, and the reply shows the plan for
// everything under it.
func TestStartIssues(t *testing.T) {
	st, srv, run, _ := testServer(t)
	is := boardFixture(t, st, run) // epic > api, ui, docs; ui and docs wait for api; docs also for the running gc-1
	ctx := t.Context()
	for _, k := range []string{"epic", "api", "ui", "docs"} {
		if _, err := st.TransitionIssue(ctx, is[k].ID, domain.IssueDraft, domain.IssueReady); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture's running gc-1 holds the only slot (its run is active).
	var got startResponse
	if code := postDecode(t, srv.URL+"/api/issues/start", `{"keys":["`+is["epic"].Key+`"],"by":"ana"}`, &got); code != http.StatusAccepted {
		t.Fatalf("start = %d", code)
	}
	if !slices.Equal(got.Requested, []string{is["epic"].Key}) || len(got.Starting) != 0 {
		t.Fatalf("reply = %+v; want the epic requested and nothing starting (no free slot)", got)
	}
	reasons := map[string]string{}
	for _, w := range got.Waiting {
		reasons[w.Key] = w.Reason
	}
	if !strings.Contains(reasons[is["api"].Key], "no free slot") || reasons[is["ui"].Key] != "waits for "+is["api"].Key {
		t.Errorf("waiting = %+v", got.Waiting)
	}
	if _, ok := reasons[is["running"].Key]; ok {
		t.Errorf("waiting lists %s, which is outside the epic", is["running"].Key)
	}
	var epic issueDetailJSON
	getJSON(t, srv.URL+"/api/issues/"+is["epic"].Key, &epic)
	if epic.StartRequestedAt == nil || epic.StartRequestedBy != "ana" {
		t.Errorf("epic request = %v by %q", epic.StartRequestedAt, epic.StartRequestedBy)
	}
	var list []issueSummaryJSON
	getJSON(t, srv.URL+"/api/issues?project=misarescu/Goodreads-Clone", &list)
	for _, x := range list {
		if (x.StartRequestedAt != nil) != (x.Key == is["epic"].Key) {
			t.Errorf("%s start_requested_at = %v; only the epic itself was requested", x.Key, x.StartRequestedAt)
		}
	}

	for _, tt := range []struct {
		name, body string
		want       int
	}{
		{"no keys", `{"keys":[]}`, http.StatusBadRequest},
		{"unknown key", `{"keys":["gc-999"]}`, http.StatusNotFound},
		{"by too long", `{"keys":["gc-1"],"by":"` + strings.Repeat("x", 101) + `"}`, http.StatusBadRequest},
	} {
		if code := postDecode(t, srv.URL+"/api/issues/start", tt.body, nil); code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, code, tt.want)
		}
	}
	if _, err := st.TransitionIssue(ctx, is["docs"].ID, domain.IssueReady, domain.IssueCanceled); err != nil {
		t.Fatal(err)
	}
	if code := postDecode(t, srv.URL+"/api/issues/start", `{"keys":["`+is["docs"].Key+`"]}`, nil); code != http.StatusConflict {
		t.Errorf("starting a canceled issue = %d, want 409", code)
	}
}

func TestRetryIssue(t *testing.T) {
	st, srv, run, _ := testServer(t)
	ctx := t.Context()
	running, _ := st.GetIssue(ctx, run.IssueID)
	url := srv.URL + "/api/issues/" + running.Key + "/retry"

	if code := postDecode(t, url, `{}`, nil); code != http.StatusConflict {
		t.Fatalf("retrying a running issue = %d, want 409", code)
	}
	if _, err := st.TransitionIssue(ctx, running.ID, domain.IssueRunning, domain.IssueFailed); err != nil {
		t.Fatal(err)
	}
	var got issueRefJSON
	if code := postDecode(t, url, `{"by":"ana"}`, &got); code != http.StatusAccepted || got.Status != "ready" {
		t.Fatalf("retry = %d, %+v; want 202 and ready", code, got)
	}
	is, _ := st.GetIssue(ctx, running.ID)
	if is.StartRequestedAt == nil || is.StartRequestedBy != "ana" {
		t.Errorf("retry did not request a start: %+v", is)
	}
	if code := postDecode(t, srv.URL+"/api/issues/gc-999/retry", `{}`, nil); code != http.StatusNotFound {
		t.Errorf("unknown issue = %d, want 404", code)
	}
}

// D63: the start reply and the issue page warn about likely overlap with
// in-flight work and between issues that may start together.
func TestStartIssuesWarnsAboutOverlap(t *testing.T) {
	st, srv, run, _ := testServer(t)
	ctx := t.Context()
	running, _ := st.GetIssue(ctx, run.IssueID) // gc-1, running
	if err := st.SetIssueChangedPaths(ctx, running.ID, []string{"src/search/index.ts"}); err != nil {
		t.Fatal(err)
	}
	mk := func(hints ...string) domain.Issue {
		is, err := st.CreateIssue(ctx, store.NewIssue{ProjectID: running.ProjectID, Type: domain.IssueTask, Title: "t",
			Status: domain.IssueReady, CreatedBy: "t", FileHints: hints})
		if err != nil {
			t.Fatal(err)
		}
		return is
	}
	a := mk("src/search/ui.tsx")
	b := mk("src/search/")
	c := mk("docs/x.md")

	var got startResponse
	body := `{"keys":["` + a.Key + `","` + b.Key + `","` + c.Key + `"]}`
	if code := postDecode(t, srv.URL+"/api/issues/start", body, &got); code != http.StatusAccepted {
		t.Fatalf("start = %d", code)
	}
	want := []overlapJSON{
		{Key: a.Key, With: running.Key, Paths: []string{"src/search"}},
		{Key: a.Key, With: b.Key, Paths: []string{"src/search"}},
		{Key: b.Key, With: running.Key, Paths: []string{"src/search"}},
	}
	if !reflect.DeepEqual(got.Overlaps, want) {
		t.Fatalf("overlaps = %+v, want %+v", got.Overlaps, want)
	}

	var detail issueDetailJSON
	getJSON(t, srv.URL+"/api/issues/"+a.Key, &detail)
	if !slices.Equal(detail.FileHints, []string{"src/search/ui.tsx"}) || len(detail.ChangedPaths) != 0 ||
		len(detail.Overlaps) != 1 || detail.Overlaps[0].With != running.Key {
		t.Errorf("detail = hints %q, changed %q, overlaps %+v", detail.FileHints, detail.ChangedPaths, detail.Overlaps)
	}
}

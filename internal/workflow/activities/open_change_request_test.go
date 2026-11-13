package activities

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
)

func TestOpenChangeRequestIsIdempotent(t *testing.T) {
	st, in := readyIssue(t)
	posts := 0
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":3,"html_url":"https://github.com/misarescu/Goodreads-Clone/pull/3","head":{"sha":"abc"}}`))
	}))
	defer gh.Close()
	a := &Activities{Store: st, GitHub: github.NewClient("pat", gh.URL), Log: quietLog()}
	env := activityEnv(a)
	var run StartRunResult
	v, err := env.ExecuteActivity(a.StartRun, in)
	if err != nil {
		t.Fatal(err)
	}
	_ = v.Get(&run)
	session := SessionResult{Status: "succeeded", Branch: run.Branch, CommitSHA: "abc", Pushed: true}

	for i := range 2 {
		v, err := env.ExecuteActivity(a.OpenChangeRequest, in, run, session)
		if err != nil {
			t.Fatalf("OpenChangeRequest #%d: %v", i, err)
		}
		var url string
		if err := v.Get(&url); err != nil || url != "https://github.com/misarescu/Goodreads-Clone/pull/3" {
			t.Fatalf("url = %q, %v", url, err)
		}
	}
	crs, err := st.ListChangeRequestsForIssue(t.Context(), uuid.MustParse(in.IssueID))
	if err != nil || len(crs) != 1 || crs[0].Number != 3 || crs[0].Branch != run.Branch {
		t.Errorf("change requests = %+v, %v", crs, err)
	}
	if posts != 2 {
		t.Errorf("GitHub calls = %d", posts)
	}
}

package github

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

const issueJSON = `{"number":1,"title":"Search","body":"spec","state":"open","html_url":"https://github.com/o/r/issues/1",
 "repository_url":"https://api.github.com/repos/o/r","user":{"login":"mi"},"labels":[{"name":"factory"},{"name":"epic"}],
 "type":{"name":"Feature"},"sub_issues_summary":{"total":3,"completed":1},"issue_dependencies_summary":{"blocked_by":2,"total_blocked_by":4}}`

func TestListLabeledIssues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pat" || r.URL.Path != "/repos/o/r/issues" ||
			r.URL.Query().Get("labels") != "factory" || r.URL.Query().Get("state") != "open" {
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[` + issueJSON + `,
		 {"number":2,"title":"a PR","html_url":"https://github.com/o/r/pull/2","pull_request":{}}]`))
	}))
	defer srv.Close()
	issues, err := NewClient("pat", srv.URL).ListLabeledIssues(t.Context(), "o/r", "factory")
	if err != nil {
		t.Fatal(err)
	}
	want := domain.ExternalIssue{Ref: "https://github.com/o/r/issues/1", Repo: "o/r", Number: 1, Title: "Search", Body: "spec",
		Author: "mi", Labels: []string{"factory", "epic"}, SourceType: "Feature", Open: true, SubIssues: 3, OpenBlockers: 2}
	if len(issues) != 1 || !equalExternal(issues[0], want) {
		t.Fatalf("issues = %+v, want only %+v (pull requests skipped)", issues, want)
	}
}

func TestIssueRelations(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/repos/o/r/issues/1/sub_issues", "/repos/o/r/issues/1/dependencies/blocked_by":
			_, _ = w.Write([]byte(`[{"number":5,"state":"closed","html_url":"https://github.com/o/r/issues/5","repository_url":"https://api.github.com/repos/o/r"},
			 {"number":7,"state":"open","html_url":"https://github.com/x/y/issues/7","repository_url":"https://api.github.com/repos/x/y"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewClient("pat", srv.URL)
	for name, list := range map[string]func() ([]domain.ExternalIssue, error){
		"sub-issues": func() ([]domain.ExternalIssue, error) { return c.SubIssues(t.Context(), "o/r", 1) },
		"blocked by": func() ([]domain.ExternalIssue, error) { return c.BlockedBy(t.Context(), "o/r", 1) },
	} {
		got, err := list()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 2 || got[0].Open || got[0].Repo != "o/r" || !got[1].Open || got[1].Repo != "x/y" || got[1].Number != 7 {
			t.Errorf("%s = %+v", name, got)
		}
	}
	if len(paths) != 2 {
		t.Errorf("requests = %v", paths)
	}
}

func TestRepoFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.github.com/repos/o/r":         "o/r",
		"https://ghe.example.com/api/v3/repos/o/r": "o/r",
		"": "",
		"https://api.github.com/users/someone/thing": "",
	} {
		if got := repoFromURL(in); got != want {
			t.Errorf("repoFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func equalExternal(a, b domain.ExternalIssue) bool {
	return reflect.DeepEqual(a, b)
}

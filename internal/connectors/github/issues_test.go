package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
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

func TestIssueNumber(t *testing.T) {
	for ref, want := range map[string]int{
		"https://github.com/o/r/issues/42":          42,
		"https://github.com/o/r/pull/42":            0,
		"https://github.com/o/r/issues/x":           0,
		"https://github.com/o/r/issues/0":           0,
		"":                                          0,
		"https://github.com/o/r/issues/42/comments": 0,
	} {
		got, ok := IssueNumber(ref)
		if got != want || ok != (want > 0) {
			t.Errorf("IssueNumber(%q) = %d, %v; want %d", ref, got, ok, want)
		}
	}
}

// D71: the write-back reads an issue, comments on it and closes it as completed
// or not planned, addressed by its source ref.
func TestIssueWriteBack(t *testing.T) {
	type call struct{ method, path, body string }
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, call{r.Method, r.URL.Path, strings.TrimSpace(string(b))})
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/7":
			_, _ = w.Write([]byte(`{"number":7,"state":"open","title":"t","html_url":"https://github.com/o/r/issues/7","repository_url":"https://api.github.com/repos/o/r"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/o/r/issues/7":
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/repos/o/r/issues/9":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues/7/comments":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "Resource not accessible by personal access token", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c := NewClient("pat", srv.URL)
	ctx := t.Context()
	ref := "https://github.com/o/r/issues/7"

	got, err := c.Get(ctx, ref)
	if err != nil || !got.Open || got.Number != 7 || got.Repo != "o/r" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if err := c.Comment(ctx, ref, "Canceled in Frankie"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []domain.IssueStatus{domain.IssueMerged, domain.IssueCanceled, domain.IssueReady} {
		if err := c.UpdateStatus(ctx, ref, s); err != nil {
			t.Fatalf("UpdateStatus(%s): %v", s, err)
		}
	}
	want := []call{
		{"GET", "/repos/o/r/issues/7", ""},
		{"POST", "/repos/o/r/issues/7/comments", `{"body":"Canceled in Frankie"}`},
		{"PATCH", "/repos/o/r/issues/7", `{"state":"closed","state_reason":"completed"}`},
		{"PATCH", "/repos/o/r/issues/7", `{"state":"closed","state_reason":"not_planned"}`},
		// ready: nothing to mirror
	}
	if !slices.Equal(calls, want) {
		t.Errorf("requests =\n%v\nwant\n%v", calls, want)
	}

	if _, err := c.Get(ctx, "https://github.com/o/r/issues/9"); !errors.Is(err, domain.ErrExternalIssueGone) {
		t.Errorf("Get of a missing issue = %v, want ErrExternalIssueGone", err)
	}
	calls = nil
	if err := c.Comment(ctx, "https://github.com/o/r/pull/7", "x"); err == nil {
		t.Error("a pull request URL was taken as an issue")
	}
	if err := c.UpdateStatus(ctx, "https://github.com/o/r/issues/8", domain.IssueCanceled); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("a refused write = %v, want the 403", err)
	}
}

// M7, D115: the factory opens an issue with labels.
func TestCreateIssue(t *testing.T) {
	var got struct {
		method, path string
		body         map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":131,"title":"t","state":"open","html_url":"https://github.com/o/r/issues/131",
 "repository_url":"https://api.github.com/repos/o/r","labels":[{"name":"factory"},{"name":"bug"}]}`))
	}))
	defer srv.Close()
	c := NewClient("pat", srv.URL)
	is, err := c.CreateIssue(t.Context(), "o/r", domain.NewExternalIssue{Title: "staging failed", Body: "details", Labels: []string{"factory", "bug"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/repos/o/r/issues" || got.body["title"] != "staging failed" || got.body["body"] != "details" ||
		!reflect.DeepEqual(got.body["labels"], []any{"factory", "bug"}) {
		t.Errorf("request = %+v", got)
	}
	if is.Number != 131 || is.Ref != "https://github.com/o/r/issues/131" || is.Repo != "o/r" || !slices.Equal(is.Labels, []string{"factory", "bug"}) {
		t.Errorf("issue = %+v", is)
	}
	if _, err := c.CreateIssue(t.Context(), "o/r", domain.NewExternalIssue{Title: " "}); err == nil {
		t.Error("an issue without a title was created")
	}
}

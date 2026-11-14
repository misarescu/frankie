package activities

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/guard"
)

func compareServer(t *testing.T, files []github.ChangedFile) (*github.Client, *string) {
	t.Helper()
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.EscapedPath()
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	}))
	t.Cleanup(srv.Close)
	return github.NewClient("pat", srv.URL), &asked
}

// Invariant 6: the control plane inspects the provider's diff of the run.
func TestDetectProtectedChanges(t *testing.T) {
	gh, asked := compareServer(t, []github.ChangedFile{
		{Filename: "internal/server/routes.go", Status: "modified", Patch: "+x"},
		{Filename: "mise.toml", Status: "modified", Patch: "+go = \"1.26\""},
		{Filename: "internal/server/routes_test.go", Status: "removed", Patch: "-func TestRoutes(t *testing.T) {"},
		{Filename: "internal/server/big_test.go", Status: "modified"}, // no patch: too large
		{Filename: "internal/web/new.go", PreviousFilename: "internal/web/old.go", Status: "renamed"},
	})
	st, in := readyIssue(t)
	a := &Activities{GitHub: gh, Store: st, Log: quietLog()}
	v, err := activityEnv(a).ExecuteActivity(a.DetectProtectedChanges, in,
		GuardRequest{BaseSHA: testBaseSHA, HeadSHA: pushedSHA, Patterns: config.DefaultProtectedPaths})
	if err != nil {
		t.Fatal(err)
	}
	var r guard.Report
	_ = v.Get(&r)
	if *asked != "/repos/misarescu/Goodreads-Clone/compare/"+testBaseSHA+"..."+pushedSHA {
		t.Errorf("compared %s", *asked)
	}
	if len(r.Protected) != 1 || r.Protected[0].Path != "mise.toml" {
		t.Errorf("protected = %+v", r.Protected)
	}
	tests := strings.Join(r.Tests, "; ")
	if !strings.Contains(tests, "deleted test file internal/server/routes_test.go") || !strings.Contains(tests, "could not inspect the diff of test file internal/server/big_test.go") {
		t.Errorf("tests = %v", r.Tests)
	}
	// D63: the changed files, renames with their old name, are recorded on the issue.
	is, err := st.GetIssueByKey(t.Context(), in.IssueKey)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal/server/routes.go", "mise.toml", "internal/server/routes_test.go", "internal/server/big_test.go", "internal/web/new.go", "internal/web/old.go"}
	if !slices.Equal(is.ChangedPaths, want) {
		t.Errorf("changed paths = %q, want %q", is.ChangedPaths, want)
	}
}

func TestDetectProtectedChangesClean(t *testing.T) {
	gh, _ := compareServer(t, []github.ChangedFile{{Filename: "internal/server/routes.go", Status: "modified", Patch: "+x"},
		{Filename: "internal/server/routes_test.go", Status: "modified", Patch: "+func TestNotFound(t *testing.T) {"}})
	st, in := readyIssue(t)
	a := &Activities{GitHub: gh, Store: st, Log: quietLog()}
	v, err := activityEnv(a).ExecuteActivity(a.DetectProtectedChanges, in,
		GuardRequest{BaseSHA: testBaseSHA, HeadSHA: pushedSHA, Patterns: config.DefaultProtectedPaths})
	if err != nil {
		t.Fatal(err)
	}
	var r guard.Report
	_ = v.Get(&r)
	if !r.Clean() {
		t.Errorf("report = %+v", r)
	}
}

package activities

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	})
	a := &Activities{GitHub: gh, Log: quietLog()}
	v, err := activityEnv(a).ExecuteActivity(a.DetectProtectedChanges, testInput,
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
}

func TestDetectProtectedChangesClean(t *testing.T) {
	gh, _ := compareServer(t, []github.ChangedFile{{Filename: "internal/server/routes.go", Status: "modified", Patch: "+x"},
		{Filename: "internal/server/routes_test.go", Status: "modified", Patch: "+func TestNotFound(t *testing.T) {"}})
	a := &Activities{GitHub: gh, Log: quietLog()}
	v, err := activityEnv(a).ExecuteActivity(a.DetectProtectedChanges, testInput,
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

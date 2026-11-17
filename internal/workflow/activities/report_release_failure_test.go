package activities

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
)

// issueTracker is a GitHub that lists issues labeled bug and records the ones opened.
type issueTracker struct {
	mu      sync.Mutex
	open    []map[string]any // issues listed under the bug label
	created []map[string]any
}

func (it *issueTracker) client(t *testing.T) *github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		it.mu.Lock()
		defer it.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/"+relRepo+"/issues" && r.URL.Query().Get("labels") == "bug":
			_ = json.NewEncoder(w).Encode(it.open)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/"+relRepo+"/issues":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			it.created = append(it.created, body)
			n := 130 + len(it.created)
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"number":%d,"state":"open","html_url":"https://github.com/%s/issues/%d","repository_url":"https://api.github.com/repos/%s"}`, n, relRepo, n, relRepo)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return github.NewClient("pat", srv.URL)
}

func (f *releaseFixture) report(t *testing.T, req ReleaseFailureReport) string {
	t.Helper()
	v, err := activityEnv(f.a).ExecuteActivity(f.a.ReportReleaseFailure, req)
	if err != nil {
		t.Fatalf("ReportReleaseFailure: %v", err)
	}
	var ref string
	_ = v.Get(&ref)
	return ref
}

// M7, FR-RL-2, FR-TA-3, D115: a failed verification opens one bug issue that
// links the release, the failing tests and the suspect issues.
func TestReportReleaseFailure(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	plan := f.prepare(t)
	tracker := &issueTracker{}
	f.a.GitHub, f.a.IntakeLabel = tracker.client(t), "factory"
	req := ReleaseFailureReport{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, Stage: "staging", URL: "https://goodreads-stg.example.ts.net",
		RolledBack: true, Verification: Verification{Summary: "1 of 12 tests failed", Failures: []TestFailure{
			{Test: "Books/search", IssueKeys: []string{f.issue.Key, "gc-12"}, Message: "expected 200 | got 500"}}}}

	ref := f.report(t, req)
	if ref != "https://github.com/"+relRepo+"/issues/131" {
		t.Errorf("ref = %q", ref)
	}
	if rel := f.release(t, plan.ReleaseID); rel.BugIssueRef != ref {
		t.Errorf("release bug issue = %q, want %q", rel.BugIssueRef, ref)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created %d issues", len(tracker.created))
	}
	got := tracker.created[0]
	if got["title"] != "Release ccccccc failed on staging: Books/search" {
		t.Errorf("title = %q", got["title"])
	}
	if labels, _ := got["labels"].([]any); !slices.Equal(labels, []any{"factory", "bug"}) {
		t.Errorf("labels = %v", got["labels"])
	}
	body, _ := got["body"].(string)
	for _, want := range []string{
		"failed verification on **staging** and was rolled back",
		"1 of 12 tests failed",
		"| Books/search | " + f.issue.Key + ", gc-12 | expected 200 \\| got 500 |",
		"the issues of this release the failing tests are tagged with",
		"- " + f.issue.Key + ": Add search",
		"also cover issues released earlier: gc-12",
		"https://github.com/" + relRepo + "/commit/" + relSHA,
		"Frankie release: `" + plan.ReleaseID + "`",
		"Stage: staging at https://goodreads-stg.example.ts.net",
		releaseBugMarker(uuid.MustParse(plan.ReleaseID)),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}

	// A retry opens nothing new.
	if again := f.report(t, req); again != ref || len(tracker.created) != 1 {
		t.Errorf("retry = %q with %d issues, want the first", again, len(tracker.created))
	}
}

// An issue opened before its reference was recorded is found by its marker.
func TestReportReleaseFailureFindsItsIssue(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	plan := f.prepare(t)
	ref := "https://github.com/" + relRepo + "/issues/140"
	tracker := &issueTracker{open: []map[string]any{
		{"number": 139, "html_url": "https://github.com/" + relRepo + "/issues/139", "body": "another bug", "state": "open"},
		{"number": 140, "html_url": ref, "body": "...\n" + releaseBugMarker(uuid.MustParse(plan.ReleaseID)) + "\n", "state": "open"},
	}}
	f.a.GitHub = tracker.client(t)
	got := f.report(t, ReleaseFailureReport{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, Stage: "staging"})
	if got != ref || len(tracker.created) != 0 {
		t.Errorf("ref = %q, created %d; want the marked issue and none created", got, len(tracker.created))
	}
	if rel := f.release(t, plan.ReleaseID); rel.BugIssueRef != ref {
		t.Errorf("release bug issue = %q", rel.BugIssueRef)
	}
}

// Without a failing test tagged with one of its issues, every issue is a suspect.
func TestSuspects(t *testing.T) {
	tests := []struct {
		name             string
		release          []string
		failures         []TestFailure
		suspect, earlier []string
	}{
		{"tagged", []string{"gc-1", "gc-2"}, []TestFailure{{IssueKeys: []string{"gc-2"}}, {IssueKeys: []string{"gc-2", "gc-9"}}}, []string{"gc-2"}, []string{"gc-9"}},
		{"untagged", []string{"gc-1", "gc-2"}, []TestFailure{{Test: "x"}}, []string{"gc-1", "gc-2"}, nil},
		{"only earlier issues", []string{"gc-1"}, []TestFailure{{IssueKeys: []string{"gc-9"}}}, []string{"gc-1"}, []string{"gc-9"}},
		{"no issues", nil, nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, e := suspects(tt.release, tt.failures)
			if !slices.Equal(s, tt.suspect) || !slices.Equal(e, tt.earlier) {
				t.Errorf("suspects = %v, %v; want %v, %v", s, e, tt.suspect, tt.earlier)
			}
		})
	}
}

func TestReleaseBugTitleAndCells(t *testing.T) {
	req := ReleaseFailureReport{SHA: relSHA, Stage: "staging"}
	if got := releaseBugTitle(req); got != "Release ccccccc failed verification on staging" {
		t.Errorf("title = %q", got)
	}
	req.Verification.Failures = []TestFailure{{Test: "A"}, {Test: "B"}, {Test: "C"}}
	if got := releaseBugTitle(req); got != "Release ccccccc failed on staging: A and 2 more" {
		t.Errorf("title = %q", got)
	}
	if got := cell("a |\n b"); got != `a \| b` {
		t.Errorf("cell = %q", got)
	}
	if got := []rune(cell(strings.Repeat("x", 400))); len(got) != 300 {
		t.Errorf("long cell has %d characters, want 300", len(got))
	}
}

// M7 (D129): the release agent's analysis becomes a section of the bug issue
// when it meets the contract.
func TestReleaseBugBodyAnalysis(t *testing.T) {
	req := ReleaseFailureReport{Repo: relRepo, SHA: relSHA, Stage: "staging",
		Analysis: `{"suspects":[{"issue":"gc-74","likelihood":"high","why":"review_count counts stars"}],"summary":"The ratings change broke the counts."}`}
	body := releaseBugBody(req, uuid.New(), nil)
	for _, want := range []string{"**Analysis by the release agent**\n\nThe ratings change broke the counts.", "- gc-74 (high): review_count counts stars"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	req.Analysis = "I think gc-74 did it."
	if body := releaseBugBody(req, uuid.New(), nil); strings.Contains(body, "Analysis by the release agent") {
		t.Errorf("an answer off the contract was published:\n%s", body)
	}
}

package activities

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
)

// provider answers comparisons by "base...head" and file contents by
// "path@ref".
func provider(t *testing.T, ranges map[string][]github.ChangedFile, files map[string]string) *github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const repo = "/repos/misarescu/Goodreads-Clone/"
		if path, ok := strings.CutPrefix(r.URL.Path, repo+"contents/"); ok {
			content, ok := files[path+"@"+r.URL.Query().Get("ref")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(content))
			return
		}
		files, ok := ranges[strings.TrimPrefix(r.URL.Path, repo+"compare/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	}))
	t.Cleanup(srv.Close)
	return github.NewClient("pat", srv.URL)
}

// The reproduce step's change: a new test file, and in an existing one a new
// test function and a corrected expectation.
const (
	newTestPatch = "@@ -0,0 +1,4 @@\n+package storage\n+\n+func TestStats(t *testing.T) {\n+\tcheck(t, stats(), 3)\n+}"
	oldTestPatch = "@@ -10,7 +10,7 @@ func TestShelf(t *testing.T) {\n \tbooks := shelf()\n-\tcheck(t, len(books), 2)\n+\tcheck(t, len(books), 3)\n }\n@@ -40,3 +40,7 @@\n }\n+\n+func TestShelfCount(t *testing.T) {\n+\tcheck(t, count(), 3)\n+}\n\\ No newline at end of file"
	// the existing test file as reproduce left it
	shelfTest = "package storage\n\nfunc helper() int { return 1 }\n\nfunc TestShelf(t *testing.T) {\n\tbooks := shelf()\n\tcheck(t, len(books), 3)\n}\n\nfunc TestShelfCount(t *testing.T) {\n\tcheck(t, count(), 3)\n}"
)

// M6 (D95, D97): the fix must leave what the reproduce step wrote as it
// wrote it; changing or removing the lines it added, or deleting or renaming
// a file it wrote, fails the gate, while edits elsewhere in its files pass.
func TestCheckPreserved(t *testing.T) {
	reproduced := []github.ChangedFile{
		{Filename: "internal/storage/stats_test.go", Status: "added", Patch: newTestPatch},
		{Filename: "internal/storage/shelf_test.go", Status: "modified", Patch: oldTestPatch},
		{Filename: "testdata/shelf.db", Status: "modified"}, // binary: no patch
		{Filename: "old.go", Status: "removed"},
	}
	modified := func(file string) []github.ChangedFile {
		return []github.ChangedFile{{Filename: file, Status: "modified"}}
	}
	tests := []struct {
		name    string
		later   []github.ChangedFile
		head    map[string]string // file contents at h2
		passed  bool
		summary string
		lines   []string
	}{
		{"the fix leaves the tests alone", modified("db/queries/shelf_books.sql"), nil, true, "the 3 files reproduce changed keep what it wrote", nil},
		{"the fix edits elsewhere in a test file", modified("internal/storage/shelf_test.go"),
			map[string]string{"internal/storage/shelf_test.go": strings.Replace(shelfTest, "return 1", "return 2", 1) + "\n\nfunc TestMore(t *testing.T) {}\n"},
			true, "the 3 files reproduce changed keep what it wrote", nil},
		{"the fix reformats the test", modified("internal/storage/shelf_test.go"),
			map[string]string{"internal/storage/shelf_test.go": strings.ReplaceAll(shelfTest, "\t", "    ") + "\n\n"},
			true, "the 3 files reproduce changed keep what it wrote", nil},
		{"the fix reverts the corrected expectation", modified("internal/storage/shelf_test.go"),
			map[string]string{"internal/storage/shelf_test.go": strings.Replace(shelfTest, "len(books), 3", "len(books), 2", 1)},
			false, "changed what reproduce wrote: internal/storage/shelf_test.go (modified)", []string{"check(t, len(books), 3)"}},
		{"the fix skips the new test", modified("internal/storage/shelf_test.go"),
			map[string]string{"internal/storage/shelf_test.go": strings.Replace(shelfTest, "Count(t *testing.T) {", "Count(t *testing.T) {\n\tt.Skip()", 1)},
			false, "changed what reproduce wrote: internal/storage/shelf_test.go (modified)", []string{"func TestShelfCount(t *testing.T) {"}},
		{"the fix edits the new test file", modified("internal/storage/stats_test.go"),
			map[string]string{"internal/storage/stats_test.go": "package storage\n\nfunc TestStats(t *testing.T) {\n\tcheck(t, stats(), 2)\n}\n"},
			false, "changed what reproduce wrote: internal/storage/stats_test.go (modified)", []string{"check(t, stats(), 3)"}},
		{"the fix touches a file kept whole", modified("testdata/shelf.db"), nil, false, "changed what reproduce wrote: testdata/shelf.db (modified)", nil},
		{"the fix deletes the test", []github.ChangedFile{{Filename: "internal/storage/stats_test.go", Status: "removed"}}, nil, false,
			"changed what reproduce wrote: internal/storage/stats_test.go (removed)", nil},
		{"the fix renames the test away", []github.ChangedFile{{Filename: "x.go", PreviousFilename: "internal/storage/stats_test.go", Status: "renamed"}}, nil, false,
			"changed what reproduce wrote: internal/storage/stats_test.go (renamed)", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueActivityFixture(t)
			head := map[string]string{}
			for file, content := range tt.head {
				head[file+"@h2"] = content
			}
			f.a.GitHub = provider(t, map[string][]github.ChangedFile{"r0...r1": reproduced, "r1...h2": tt.later}, head)
			id := f.step(t, "keeps-test", domain.StepGate)
			v, err := activityEnv(f.a).ExecuteActivity(f.a.CheckPreserved, f.in, PreserveRequest{RunID: f.run.RunID, StepRunID: id, Step: "reproduce",
				From: "r0", To: "r1", Head: "h2"})
			if err != nil {
				t.Fatal(err)
			}
			var out GateOutcome
			_ = v.Get(&out)
			if out.Passed != tt.passed || out.Summary != tt.summary || (!tt.passed && !strings.Contains(out.Feedback, "git checkout r1 -- <file>")) {
				t.Fatalf("outcome = %+v", out)
			}
			for _, l := range tt.lines {
				if !strings.Contains(out.Feedback, "`"+l+"`") {
					t.Errorf("feedback does not quote %q: %s", l, out.Feedback)
				}
			}
			step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(id))
			results, _ := f.a.Store.ListGateResultsForRun(t.Context(), uuid.MustParse(f.run.RunID))
			if step.CommitSHA != "h2" || len(results) != 1 || results[0].Type != domain.GatePreserve {
				t.Fatalf("step %+v, results %+v", step, results)
			}
			var d domain.PreserveDetails
			_ = json.Unmarshal(results[0].Details, &d)
			var lines []string
			for _, c := range d.Changed {
				lines = append(lines, c.Lines...)
			}
			if !slices.Equal(lines, tt.lines) {
				t.Errorf("lost lines = %q, want %q", lines, tt.lines)
			}
			// A retry reports what was recorded.
			v, _ = activityEnv(f.a).ExecuteActivity(f.a.CheckPreserved, f.in, PreserveRequest{RunID: f.run.RunID, StepRunID: id, Step: "reproduce"})
			var again GateOutcome
			_ = v.Get(&again)
			if again.Passed != out.Passed {
				t.Errorf("retry = %+v", again)
			}
		})
	}
	t.Run("a step that changed nothing", func(t *testing.T) {
		f := newQueueActivityFixture(t)
		id := f.step(t, "keeps-test", domain.StepGate)
		v, err := activityEnv(f.a).ExecuteActivity(f.a.CheckPreserved, f.in, PreserveRequest{RunID: f.run.RunID, StepRunID: id, Step: "reproduce", Head: "h2"})
		if err != nil {
			t.Fatal(err)
		}
		var out GateOutcome
		_ = v.Get(&out)
		if !out.Passed || out.Summary != "reproduce changed nothing to keep" {
			t.Errorf("outcome = %+v", out)
		}
	})
}

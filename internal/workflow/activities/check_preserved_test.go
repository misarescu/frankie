package activities

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
)

// compareRanges answers comparisons by "base...head".
func compareRanges(t *testing.T, ranges map[string][]github.ChangedFile) *github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/repos/misarescu/Goodreads-Clone/compare/")
		files, ok := ranges[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	}))
	t.Cleanup(srv.Close)
	return github.NewClient("pat", srv.URL)
}

// M6 (D95): the fix must leave the reproducing test as the reproduce step
// wrote it; changing, deleting or renaming it fails the gate.
func TestCheckPreserved(t *testing.T) {
	reproduced := []github.ChangedFile{{Filename: "internal/storage/stats_test.go", Status: "added"}, {Filename: "old.go", Status: "removed"}}
	tests := []struct {
		name    string
		later   []github.ChangedFile
		passed  bool
		summary string
	}{
		{"the fix leaves the test alone", []github.ChangedFile{{Filename: "db/queries/shelf_books.sql", Status: "modified"}}, true, "the 1 files reproduce changed are as it left them"},
		{"the fix edits the test", []github.ChangedFile{{Filename: "internal/storage/stats_test.go", Status: "modified"}}, false,
			"changed what reproduce wrote: internal/storage/stats_test.go (modified)"},
		{"the fix renames the test away", []github.ChangedFile{{Filename: "x.go", PreviousFilename: "internal/storage/stats_test.go", Status: "renamed"}}, false,
			"changed what reproduce wrote: internal/storage/stats_test.go (renamed)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueActivityFixture(t)
			f.a.GitHub = compareRanges(t, map[string][]github.ChangedFile{"r0...r1": reproduced, "r1...h2": tt.later})
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
			step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(id))
			results, _ := f.a.Store.ListGateResultsForRun(t.Context(), uuid.MustParse(f.run.RunID))
			if step.CommitSHA != "h2" || len(results) != 1 || results[0].Type != domain.GatePreserve {
				t.Errorf("step %+v, results %+v", step, results)
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

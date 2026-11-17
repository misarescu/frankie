package activities

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
)

// ciServer answers check runs from a sequence of answers, one per read; the
// last one repeats. Each answer is the check runs JSON array.
func ciServer(t *testing.T, answers ...string) *github.Client {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			mu.Lock()
			a := answers[min(n, len(answers)-1)]
			n++
			mu.Unlock()
			var runs []json.RawMessage
			_ = json.Unmarshal([]byte(a), &runs)
			_, _ = fmt.Fprintf(w, `{"total_count":%d,"check_runs":%s}`, len(runs), a)
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = io.WriteString(w, `{"statuses":[]}`)
		case strings.HasSuffix(r.URL.Path, "/actions/jobs/7/logs"):
			_, _ = io.WriteString(w, ciFailedLog)
		case strings.HasSuffix(r.URL.Path, "/actions/jobs/8/logs"):
			http.Error(w, "logs expired", http.StatusGone)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return github.NewClient("pat", srv.URL)
}

const (
	ciRunning = `[{"name":"test","status":"in_progress"}]`
	ciPassed  = `[{"name":"test","status":"completed","conclusion":"success"},{"name":"lint","status":"completed","conclusion":"success"}]`
	ciFailed  = `[{"id":7,"name":"test","status":"completed","conclusion":"failure","html_url":"https://ci/1","output":{"title":"2 tests failed"}},{"id":9,"name":"lint","status":"completed","conclusion":"success"}]`
	// ciFailedLog is the failed job's log: the coder sees why (D120).
	ciFailedLog = "2026-11-17T10:00:00.0Z ##[group]Run make test\n2026-11-17T10:00:01.0Z --- FAIL: TestShelves\n" +
		"2026-11-17T10:00:01.1Z     no such column: rating\n2026-11-17T10:00:02.0Z ##[error]Process completed with exit code 2.\n"
	// ciFailedNoLog is a failed check whose log GitHub no longer serves.
	ciFailedNoLog = `[{"id":8,"name":"test","status":"completed","conclusion":"failure","html_url":"https://ci/1"}]`
)

func (f *queueFixture) awaitChecks(t *testing.T, gh *github.Client, timeout time.Duration) (GateOutcome, string) {
	t.Helper()
	f.a.GitHub = gh
	id := f.step(t, "merge-ci", domain.StepGate)
	v, err := activityEnv(f.a).ExecuteActivity(f.a.AwaitProviderChecks, ProviderChecksRequest{Issue: f.in, StepRunID: id, HeadSHA: queuedHead,
		Timeout: timeout, Grace: 30 * time.Millisecond, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("AwaitProviderChecks: %v", err)
	}
	var out GateOutcome
	_ = v.Get(&out)
	return out, id
}

// M6 (D90): the queue merges only once the repo's own CI passed on the commit.
func TestAwaitProviderChecks(t *testing.T) {
	tests := []struct {
		name         string
		answers      []string
		wantPassed   bool
		inconclusive bool
		summary      string
		status       domain.ExecStatus
	}{
		{"waits while CI runs, then passes", []string{ciRunning, ciRunning, ciPassed}, true, false, "the repo's CI passed (2 checks)", domain.ExecSucceeded},
		{"a failed check fails", []string{ciRunning, ciFailed}, false, false, "the repo's CI failed: test", domain.ExecFailed},
		{"a repo without CI passes after the grace period", []string{`[]`}, true, false, "the repo has no CI checks on this commit", domain.ExecSucceeded},
		{"CI that never finishes is inconclusive", []string{ciRunning}, false, true, "the repo's CI did not finish within 60ms", domain.ExecFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueActivityFixture(t)
			out, id := f.awaitChecks(t, ciServer(t, tt.answers...), 60*time.Millisecond)
			if out.Passed != tt.wantPassed || out.Inconclusive != tt.inconclusive || out.Summary != tt.summary {
				t.Fatalf("outcome = %+v", out)
			}
			step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(id))
			if step.Status != tt.status || step.CommitSHA != queuedHead {
				t.Errorf("step %s at %q", step.Status, step.CommitSHA)
			}
			results, _ := f.a.Store.ListGateResultsForRun(t.Context(), uuid.MustParse(f.run.RunID))
			var d domain.ProviderChecksDetails
			if len(results) != 1 || results[0].Type != domain.GateProviderChecks || json.Unmarshal(results[0].Details, &d) != nil || d.SHA != queuedHead {
				t.Fatalf("results = %+v", results)
			}
			// A retry of the finished step reports what was recorded.
			v, err := activityEnv(f.a).ExecuteActivity(f.a.AwaitProviderChecks, ProviderChecksRequest{Issue: f.in, StepRunID: id, HeadSHA: queuedHead})
			if err != nil {
				t.Fatal(err)
			}
			var again GateOutcome
			_ = v.Get(&again)
			if again.Passed != out.Passed || again.Summary != out.Summary {
				t.Errorf("retry = %+v, want %+v", again, out)
			}
		})
	}
}

// The coder learns which check failed, why, where to read it and, M7 (D120),
// how its log ends, which the run's gate result keeps too.
func TestAwaitProviderChecksFeedback(t *testing.T) {
	f := newQueueActivityFixture(t)
	out, _ := f.awaitChecks(t, ciServer(t, ciFailed), time.Second)
	log := "Run make test\n--- FAIL: TestShelves\n    no such column: rating\nError: Process completed with exit code 2."
	for _, want := range []string{"- test: 2 tests failed (https://ci/1)", "  The end of its log:\n```\n" + log + "\n```\n", "Reproduce it"} {
		if !strings.Contains(out.Feedback, want) {
			t.Errorf("feedback lacks %q:\n%s", want, out.Feedback)
		}
	}
	if strings.Contains(out.Feedback, "lint") {
		t.Errorf("a passed check is in the feedback:\n%s", out.Feedback)
	}
	results, _ := f.a.Store.ListGateResultsForRun(t.Context(), uuid.MustParse(f.run.RunID))
	var d domain.ProviderChecksDetails
	if len(results) != 1 || json.Unmarshal(results[0].Details, &d) != nil || len(d.Checks) != 2 || d.Checks[0].Log != log || d.Checks[1].Log != "" {
		t.Errorf("recorded checks = %+v", d.Checks)
	}
}

// M7 (D120): a log that cannot be read leaves the feedback as it was, with
// the check's URL.
func TestAwaitProviderChecksUnreadableLog(t *testing.T) {
	f := newQueueActivityFixture(t)
	out, _ := f.awaitChecks(t, ciServer(t, ciFailedNoLog), time.Second)
	if out.Passed || !strings.Contains(out.Feedback, "- test (https://ci/1)\n") || strings.Contains(out.Feedback, "The end of its log") {
		t.Errorf("outcome = %+v", out)
	}
}

package factorye2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakePreview is a preview's API: the issue's status at each poll (the last
// one repeats), its runs, and their steps.
type fakePreview struct {
	statuses    []string
	closeReason string
	runs        map[string][]step
	polls       atomic.Int32
}

func (p *fakePreview) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	status := func() string {
		i := min(int(p.polls.Load()), len(p.statuses)) - 1
		return p.statuses[max(i, 0)]
	}
	mux.HandleFunc("GET /api/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") != "misarescu/frankie-dummy" {
			http.Error(w, "unknown project", http.StatusNotFound)
			return
		}
		p.polls.Add(1)
		out := []issue{}
		if len(p.statuses) > 0 {
			out = append(out, issue{Key: "pr7dum-1", Status: status()})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /api/issues/pr7dum-1", func(w http.ResponseWriter, _ *http.Request) {
		d := map[string]any{"key": "pr7dum-1", "status": status(), "close_reason": p.closeReason}
		var runs []map[string]string
		for _, id := range []string{"run-1", "run-2"} {
			if _, ok := p.runs[id]; ok {
				runs = append(runs, map[string]string{"id": id, "status": "x"})
			}
		}
		d["runs"] = runs
		_ = json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("GET /api/runs/{id}/steps", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(p.runs[r.PathValue("id")])
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func check(t *testing.T, url string) (Report, string) {
	t.Helper()
	var log strings.Builder
	r := Run(t.Context(), Config{BaseURL: url, Project: "misarescu/frankie-dummy", Timeout: 200 * time.Millisecond,
		Poll: 5 * time.Millisecond, HTTP: http.DefaultClient, Log: &log})
	return r, log.String()
}

// names lists the report's cases as "name: failure" or "name! error".
func names(r Report) string {
	var s []string
	for _, c := range r.Cases {
		switch {
		case c.Failure != "":
			s = append(s, c.Name+": "+c.Failure)
		case c.Error != "":
			s = append(s, c.Name+"! "+c.Error)
		default:
			s = append(s, c.Name)
		}
	}
	return strings.Join(s, "\n")
}

var mergedRun = []step{
	{StepID: "implement", Attempt: 1, Kind: "agent", Status: "succeeded", Role: "coder"},
	{StepID: "checks", Attempt: 1, Kind: "gate", Status: "failed", Gate: "command"},
	{StepID: "implement", Attempt: 2, Kind: "agent", Status: "succeeded", Role: "coder"},
	{StepID: "checks", Attempt: 2, Kind: "gate", Status: "succeeded", Gate: "command"},
	{StepID: "merge", Attempt: 1, Kind: "action", Status: "succeeded"},
}

// D132: the check waits while the issue runs and passes once it merged; a
// gate that sent the coder back on the way is the factory working.
func TestRunMerged(t *testing.T) {
	p := &fakePreview{statuses: []string{"draft", "running", "queued", "merged"}, runs: map[string][]step{"run-1": mergedRun}}
	r, log := check(t, p.serve(t).URL)
	if !r.Passed() {
		t.Fatalf("report:\n%s", names(r))
	}
	if want := "implement #1\nchecks #1\nimplement #2\nchecks #2\nmerge #1\nthe issue merged"; names(r) != want {
		t.Errorf("cases:\n%s\nwant:\n%s", names(r), want)
	}
	if r.Cases[0].Suite != "factory e2e > pr7dum-1" {
		t.Errorf("suite = %q", r.Cases[0].Suite)
	}
	for _, want := range []string{"pr7dum-1 draft", "pr7dum-1 running", "pr7dum-1 queued", "pr7dum-1 merged"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// A failed issue names the steps that failed, run by run, and why it ended.
func TestRunFailed(t *testing.T) {
	p := &fakePreview{statuses: []string{"failed"}, closeReason: "step checks: sent back to implement 3 times, the limit",
		runs: map[string][]step{
			"run-1": {{StepID: "implement", Attempt: 1, Kind: "agent", Status: "failed", Role: "coder"}},
			"run-2": {{StepID: "implement", Attempt: 1, Kind: "agent", Status: "succeeded", Role: "coder"},
				{StepID: "checks", Attempt: 1, Kind: "gate", Status: "failed", Gate: "command"}},
		}}
	r, _ := check(t, p.serve(t).URL)
	if r.Passed() {
		t.Fatal("a failed issue passed")
	}
	want := "run 1: implement #1: coder implement failed on attempt 1\nrun 2: implement #1\n" +
		"run 2: checks #1: command gate checks failed on attempt 1\n" +
		"the issue merged: the issue ended failed: step checks: sent back to implement 3 times, the limit"
	if names(r) != want {
		t.Errorf("cases:\n%s\nwant:\n%s", names(r), want)
	}
}

// A preview has no person: an issue waiting for one has failed the check.
func TestRunAwaitingHuman(t *testing.T) {
	p := &fakePreview{statuses: []string{"running", "awaiting_human"}, runs: map[string][]step{"run-1": {
		{StepID: "implement", Attempt: 1, Kind: "agent", Status: "succeeded", Role: "coder"},
		{StepID: "protected-changes", Attempt: 1, Kind: "gate", Status: "running", Gate: "human", WaitsFor: "approval"},
	}}}
	r, _ := check(t, p.serve(t).URL)
	want := "implement #1\nprotected-changes #1: human gate protected-changes waits for a person's approval\n" +
		"the issue merged: the issue waits for a person, whom a preview never has: a gate or question needs a decision"
	if r.Passed() || names(r) != want {
		t.Errorf("cases:\n%s\nwant:\n%s", names(r), want)
	}
}

// An issue still going at the timeout fails, naming the step it was on.
func TestRunTimeout(t *testing.T) {
	p := &fakePreview{statuses: []string{"running"}, runs: map[string][]step{"run-1": {
		{StepID: "implement", Attempt: 1, Kind: "agent", Status: "running", Role: "coder"},
	}}}
	r, _ := check(t, p.serve(t).URL)
	want := "implement #1: coder implement is still running\nthe issue merged: the issue is still running after 200ms"
	if r.Passed() || names(r) != want {
		t.Errorf("cases:\n%s\nwant:\n%s", names(r), want)
	}
}

// No issue taken in is the factory's fault; an API that never answers is not
// shown to be, so it is an error, which verification calls inconclusive.
func TestRunNothingToJudge(t *testing.T) {
	r, _ := check(t, (&fakePreview{}).serve(t).URL)
	if r.Passed() || len(r.Cases) != 1 || !strings.Contains(r.Cases[0].Failure, "no issue of misarescu/frankie-dummy appeared within 200ms") {
		t.Errorf("no issue: %s", names(r))
	}

	srv := (&fakePreview{}).serve(t)
	srv.Close()
	r, log := check(t, srv.URL)
	if r.Passed() || len(r.Cases) != 1 || r.Cases[0].Error == "" || r.Cases[0].Failure != "" ||
		!strings.Contains(r.Cases[0].Error, "could not read misarescu/frankie-dummy's issues") {
		t.Errorf("unreachable: %s", names(r))
	}
	if !strings.Contains(log, "the preview's API:") {
		t.Errorf("log = %q", log)
	}
}

func TestPassedNeedsACase(t *testing.T) {
	if (Report{}).Passed() {
		t.Error("an empty report passed")
	}
}

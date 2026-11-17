package github

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// jobLogSample is a GitHub Actions job log shaped like the one of gc-71's
// failed CI (M7): a byte order mark, timestamps, colours, a setup step, the
// failing step with its error, then steps that run anyway and the post steps.
const jobLogSample = "\ufeff2026-11-17T10:25:05.9490797Z ##[group]Runner Image Provisioner\n" +
	"2026-11-17T10:25:05.9490800Z Hosted Compute Agent\n" +
	"2026-11-17T10:25:05.9490900Z ##[endgroup]\n" +
	"2026-11-17T10:26:32.8758632Z ##[group]Run make migrate\n" +
	"2026-11-17T10:26:32.8758700Z make migrate\n" +
	"2026-11-17T10:26:32.8758800Z ##[endgroup]\n" +
	"2026-11-17T10:26:33.0000000Z Migrations complete\n" +
	"2026-11-17T10:26:38.1571202Z ##[group]Run mise run e2e\n" +
	"2026-11-17T10:26:38.1571300Z mise run e2e\n" +
	"2026-11-17T10:26:38.1571400Z shell: /usr/bin/bash -e {0}\n" +
	"2026-11-17T10:26:38.1571500Z ##[endgroup]\n" +
	"2026-11-17T10:26:42.0000000Z Books/Create Book (500 Internal Server Error) - 2 ms\n" +
	"2026-11-17T10:26:42.0000100Z    \x1b[31m✕\x1b[39m res.status: eq 201\n" +
	"2026-11-17T10:26:42.0000200Z       expected 500 to equal 201\n" +
	"2026-11-17T10:26:42.7700000Z [e2e] ERROR task failed\n" +
	"2026-11-17T10:26:42.7715943Z ##[error]Process completed with exit code 1.\n" +
	"2026-11-17T10:26:42.7766779Z ##[group]Run actions/upload-artifact@v4\n" +
	"2026-11-17T10:26:42.7766800Z with:\n" +
	"2026-11-17T10:26:42.7766900Z ##[endgroup]\n" +
	"2026-11-17T10:26:44.1189695Z Post job cleanup.\n" +
	"2026-11-17T10:26:44.4572474Z Cleaning up orphan processes\n"

// M7 (D120): the excerpt is the failing step, from the line that ran it to its
// error, without timestamps or colours, and nothing after it.
func TestLogExcerptFailedStep(t *testing.T) {
	got, err := logExcerpt(strings.NewReader(jobLogSample), 80, 6000)
	if err != nil {
		t.Fatal(err)
	}
	want := "Run mise run e2e\n" +
		"mise run e2e\n" +
		"shell: /usr/bin/bash -e {0}\n" +
		"Books/Create Book (500 Internal Server Error) - 2 ms\n" +
		"   ✕ res.status: eq 201\n" +
		"      expected 500 to equal 201\n" +
		"[e2e] ERROR task failed\n" +
		"Error: Process completed with exit code 1."
	if got != want {
		t.Errorf("excerpt =\n%s\nwant\n%s", got, want)
	}
}

// M7 (D120): a long step keeps its header and its last lines, within the
// line and byte bounds; a log without an error gives its last lines.
func TestLogExcerptBounds(t *testing.T) {
	var long strings.Builder
	long.WriteString("2026-11-17T10:00:00.0Z ##[group]Run go test ./...\n")
	for i := range 200 {
		fmt.Fprintf(&long, "2026-11-17T10:00:01.0Z line %03d of output\n", i)
	}
	long.WriteString("2026-11-17T10:00:02.0Z ##[error]Process completed with exit code 1.\n")
	long.WriteString("2026-11-17T10:00:03.0Z Post job cleanup.\n")

	tests := []struct {
		name               string
		log                string
		maxLines, maxBytes int
		want               []string
		notWant            []string
	}{
		{"lines", long.String(), 10, 6000,
			[]string{"Run go test ./...\n... (191 lines left out)\nline 191 of output", "line 199 of output\nError: Process completed"},
			[]string{"line 190 of output", "Post job cleanup."}},
		{"bytes", long.String(), 80, 200,
			[]string{"Run go test ./...\n... (", "Error: Process completed with exit code 1."},
			[]string{"line 150 of output"}},
		{"no error", "2026-11-17T10:00:00.0Z one\n2026-11-17T10:00:00.0Z ##[group]Run two\n2026-11-17T10:00:00.0Z three\n2026-11-17T10:00:00.0Z Post job cleanup.\n2026-11-17T10:00:00.0Z four", 4, 6000,
			[]string{"... (1 line left out)\nRun two\nthree\nPost job cleanup.\nfour"}, []string{"one"}},
		{"a very long line", "##[group]Run x\n" + strings.Repeat("a", 100<<10) + "\n##[error]boom\n", 10, 6000,
			[]string{"Error: boom"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := logExcerpt(strings.NewReader(tt.log), tt.maxLines, tt.maxBytes)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) > tt.maxBytes {
				t.Errorf("excerpt has %d bytes, more than %d", len(got), tt.maxBytes)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("excerpt lacks %q:\n%s", w, got)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("excerpt has %q:\n%s", w, got)
				}
			}
		})
	}
}

// M7 (D120): a check run's log is its job's; a workflow run's are its failed
// jobs', each under the job and its failed step, within the project's limits. GitHub redirects to the log,
// and the redirect to another host does not carry the token.
func TestCheckLog(t *testing.T) {
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the token reached the log storage")
		}
		_, _ = io.WriteString(w, jobLogSample)
	}))
	defer blob.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/actions/jobs/11/logs", "/repos/o/r/actions/jobs/13/logs", "/repos/o/r/actions/jobs/14/logs":
			http.Redirect(w, r, blob.URL+"/log", http.StatusFound)
		case "/repos/o/r/actions/runs/7/jobs":
			if r.URL.Query().Get("filter") != "latest" {
				t.Errorf("jobs filter %q", r.URL.Query().Get("filter"))
			}
			_, _ = io.WriteString(w, `{"jobs":[
				{"id":12,"name":"Lint","conclusion":"success"},
				{"id":13,"name":"Tests","conclusion":"failure","steps":[
					{"name":"Run unit tests","conclusion":"success"},{"name":"Run Bruno E2E tests","conclusion":"failure"}]},
				{"id":14,"name":"Build","conclusion":"timed_out"},
				{"id":15,"name":"Docs","conclusion":"failure"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewClient("pat", srv.URL)

	tests := []struct {
		name    string
		id      string
		limits  domain.CILogLimits
		want    []string
		notWant []string
	}{
		{"check run", "check-run/11", domain.CILogLimits{}, []string{"Run mise run e2e\n", "Error: Process completed"}, []string{"Job "}},
		{"workflow run", "workflow-run/7", domain.CILogLimits{},
			[]string{"Job Tests, step Run Bruno E2E tests:\nRun mise run e2e\n", "\n\nJob Build:\nRun mise run e2e"},
			[]string{"Job Lint", "Job Docs"}},
		{"the project's limits", "workflow-run/7", domain.CILogLimits{Jobs: 1, Lines: 2},
			[]string{"Job Tests, step Run Bruno E2E tests:\nRun mise run e2e\n... (5 lines left out)\n[e2e] ERROR task failed\nError: Process completed"},
			[]string{"Job Build", "expected 500"}},
		{"another app's check", "check-run/99", domain.CILogLimits{}, nil, nil},
		{"a commit status", "", domain.CILogLimits{}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.CheckLog(t.Context(), "o/r", domain.Check{Name: "ci", ID: tt.id}, tt.limits)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == nil && got != "" {
				t.Errorf("log = %q, want none", got)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("log lacks %q:\n%s", w, got)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("log has %q:\n%s", w, got)
				}
			}
		})
	}
}

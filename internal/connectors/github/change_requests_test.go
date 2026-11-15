package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// M6 (D85): a merge names the head the gates checked and the method; GitHub's
// refusals map to errors the merge queue can tell apart.
func TestMergeChangeRequest(t *testing.T) {
	req := domain.MergeRequest{HeadSHA: "abc", Method: domain.MergeSquash, Title: "gc-1: add shelves (#9)", Message: "Root cause: ..."}
	tests := []struct {
		name    string
		status  int
		body    string
		pr      string // GET pulls/9 answer after a refusal
		want    string
		wantErr error
	}{
		{"merged", http.StatusOK, `{"sha":"m1","merged":true}`, "", "m1", nil},
		{"head moved", http.StatusConflict, `{"message":"Head branch was modified"}`, "", "", domain.ErrHeadMoved},
		{"conflict", http.StatusMethodNotAllowed, `{"message":"Pull Request is not mergeable"}`,
			`{"state":"open","merged":false,"head":{"sha":"abc"}}`, "", domain.ErrNotMergeable},
		{"already merged at that head", http.StatusMethodNotAllowed, `{"message":"Pull Request is not mergeable"}`,
			`{"state":"closed","merged":true,"merge_commit_sha":"m0","head":{"sha":"abc"}}`, "m0", nil},
		{"merged at another head", http.StatusMethodNotAllowed, `{"message":"Pull Request is not mergeable"}`,
			`{"state":"closed","merged":true,"merge_commit_sha":"m0","head":{"sha":"zzz"}}`, "", domain.ErrNotMergeable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut && r.URL.Path == "/repos/o/r/pulls/9/merge":
					_ = json.NewDecoder(r.Body).Decode(&got)
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, tt.body)
				case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/9" && tt.pr != "":
					_, _ = io.WriteString(w, tt.pr)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusTeapot)
				}
			}))
			defer srv.Close()
			sha, err := NewClient("pat", srv.URL).MergeChangeRequest(t.Context(), "o/r", 9, req)
			if sha != tt.want || (tt.wantErr == nil) != (err == nil) || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Fatalf("merge = %q, %v; want %q, %v", sha, err, tt.want, tt.wantErr)
			}
			if got["sha"] != "abc" || got["merge_method"] != "squash" || got["commit_title"] != req.Title || got["commit_message"] != req.Message {
				t.Errorf("request = %v", got)
			}
		})
	}
	if _, err := NewClient("pat", "http://unused").MergeChangeRequest(t.Context(), "o/r", 9, domain.MergeRequest{Method: domain.MergeSquash}); err == nil {
		t.Error("a merge without a head was sent")
	}
}

// Closing comments why, then closes; a closed one is not commented twice and
// a merged one is not closed.
func TestCloseChangeRequest(t *testing.T) {
	tests := []struct {
		name    string
		pr      string
		want    []string
		wantErr bool
	}{
		{"open", `{"state":"open"}`, []string{"GET /repos/o/r/pulls/9", "POST /repos/o/r/issues/9/comments", "PATCH /repos/o/r/pulls/9"}, false},
		{"already closed", `{"state":"closed"}`, []string{"GET /repos/o/r/pulls/9"}, false},
		{"merged", `{"state":"closed","merged":true}`, []string{"GET /repos/o/r/pulls/9"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var comment, patch map[string]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch r.Method {
				case http.MethodGet:
					_, _ = io.WriteString(w, tt.pr)
				case http.MethodPost:
					_ = json.NewDecoder(r.Body).Decode(&comment)
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{}`)
				case http.MethodPatch:
					_ = json.NewDecoder(r.Body).Decode(&patch)
					_, _ = io.WriteString(w, `{}`)
				}
			}))
			defer srv.Close()
			err := NewClient("pat", srv.URL).CloseChangeRequest(t.Context(), "o/r", 9, "Rejected by mi: wrong approach")
			if (err != nil) != tt.wantErr || strings.Join(calls, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("close = %v, calls %v; want %v", err, calls, tt.want)
			}
			if tt.name == "open" && (comment["body"] != "Rejected by mi: wrong approach" || patch["state"] != "closed") {
				t.Errorf("comment %v, patch %v", comment, patch)
			}
		})
	}
}

// M6 (D85): a commit's checks combine its check runs, over pages, and its
// commit statuses.
func TestCommitStatus(t *testing.T) {
	runs := func(n, from int, status, conclusion string) string {
		var rs []string
		for i := range n {
			rs = append(rs, fmt.Sprintf(`{"name":"job-%d","status":%q,"conclusion":%q,"html_url":"u","output":{"title":"t"}}`, from+i, status, conclusion))
		}
		return strings.Join(rs, ",")
	}
	tests := []struct {
		name     string
		pages    []string
		statuses string
		want     domain.CheckState
		checks   int
	}{
		{"no checks", []string{`{"total_count":0,"check_runs":[]}`}, `{"statuses":[]}`, domain.CheckNone, 0},
		{"all green over two pages", []string{
			`{"total_count":101,"check_runs":[` + runs(100, 0, "completed", "success") + `]}`,
			`{"total_count":101,"check_runs":[` + runs(1, 100, "completed", "skipped") + `]}`,
		}, `{"statuses":[{"context":"ci/legacy","state":"success"}]}`, domain.CheckSuccess, 102},
		{"a run in progress", []string{`{"total_count":2,"check_runs":[` + runs(1, 0, "completed", "success") + `,` + runs(1, 1, "in_progress", "") + `]}`},
			`{"statuses":[]}`, domain.CheckPending, 2},
		{"a cancelled run fails", []string{`{"total_count":1,"check_runs":[` + runs(1, 0, "completed", "cancelled") + `]}`},
			`{"statuses":[]}`, domain.CheckFailure, 1},
		{"a failed status fails", []string{`{"total_count":1,"check_runs":[` + runs(1, 0, "completed", "success") + `]}`},
			`{"statuses":[{"context":"ci/legacy","state":"error","description":"boom"}]}`, domain.CheckFailure, 2},
		{"only a pending status", []string{`{"total_count":0,"check_runs":[]}`},
			`{"statuses":[{"context":"ci/legacy","state":"pending"}]}`, domain.CheckPending, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/o/r/commits/abc/check-runs":
					var page int
					_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)
					if page < 1 || page > len(tt.pages) {
						t.Errorf("unexpected page %d", page)
						_, _ = io.WriteString(w, `{"total_count":0,"check_runs":[]}`)
						return
					}
					_, _ = io.WriteString(w, tt.pages[page-1])
				case "/repos/o/r/commits/abc/status":
					_, _ = io.WriteString(w, tt.statuses)
				default:
					t.Errorf("unexpected %s", r.URL)
				}
			}))
			defer srv.Close()
			got, err := NewClient("pat", srv.URL).CommitStatus(t.Context(), "o/r", "abc")
			if err != nil || got.State != tt.want || len(got.Checks) != tt.checks || got.SHA != "abc" {
				t.Fatalf("status = %s with %d checks, %v; want %s with %d", got.State, len(got.Checks), err, tt.want, tt.checks)
			}
		})
	}
}

func TestCheckRunState(t *testing.T) {
	for _, tt := range []struct {
		status, conclusion string
		want               domain.CheckState
	}{
		{"queued", "", domain.CheckPending},
		{"completed", "success", domain.CheckSuccess},
		{"completed", "neutral", domain.CheckSuccess},
		{"completed", "timed_out", domain.CheckFailure},
		{"completed", "action_required", domain.CheckFailure},
	} {
		if got := checkRunState(tt.status, tt.conclusion); got != tt.want {
			t.Errorf("%s/%s = %s, want %s", tt.status, tt.conclusion, got, tt.want)
		}
	}
}

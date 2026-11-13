package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListOpenIssuesSkipsPullRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pat" || r.URL.Path != "/repos/o/r/issues" ||
			r.URL.Query().Get("labels") != "factory" || r.URL.Query().Get("state") != "open" {
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[
		 {"number":1,"title":"Add search","body":"spec","html_url":"https://github.com/o/r/issues/1","user":{"login":"mi"},"labels":[{"name":"factory"}]},
		 {"number":2,"title":"a PR","html_url":"https://github.com/o/r/pull/2","pull_request":{}}]`))
	}))
	defer srv.Close()
	issues, err := NewClient("pat", srv.URL).ListOpenIssues(t.Context(), "o/r", "factory")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 1 || issues[0].URL != "https://github.com/o/r/issues/1" || issues[0].Author != "mi" {
		t.Errorf("issues = %+v", issues)
	}
}

func TestOpenPullRequest(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":9,"html_url":"https://github.com/o/r/pull/9","head":{"sha":"abc"}}`))
	}))
	defer srv.Close()
	pr, err := NewClient("pat", srv.URL).OpenPullRequest(t.Context(), "o/r", "agent/gc-1/x", "main", "gc-1: title", "body")
	if err != nil || pr.Number != 9 || pr.Head != "abc" {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
	if got["head"] != "agent/gc-1/x" || got["base"] != "main" || got["title"] != "gc-1: title" {
		t.Errorf("request = %v", got)
	}
}

// A retried Activity finds the PR it opened before.
func TestOpenPullRequestReturnsExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"message":"A pull request already exists for o:agent/gc-1/x."}]}`))
			return
		}
		if r.URL.Query().Get("head") != "o:agent/gc-1/x" {
			http.Error(w, "bad head filter", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[{"number":9,"html_url":"https://github.com/o/r/pull/9","head":{"sha":"abc"}}]`))
	}))
	defer srv.Close()
	pr, err := NewClient("pat", srv.URL).OpenPullRequest(t.Context(), "o/r", "agent/gc-1/x", "main", "t", "b")
	if err != nil || pr.Number != 9 {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestStatusErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := NewClient("bad", srv.URL).ListOpenIssues(t.Context(), "o/r", "factory")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveRef(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/commits/main" || r.Header.Get("Accept") != "application/vnd.github.sha" {
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("0123456789abcdef0123456789abcdef01234567\n"))
	}))
	defer srv.Close()
	sha, err := NewClient("pat", srv.URL).ResolveRef(t.Context(), "o/r", "main")
	if err != nil || sha != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("ResolveRef = %q, %v", sha, err)
	}
}

func TestFileAt(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantFound bool
		wantErr   bool
	}{
		{"present", http.StatusOK, "[tools]\n", true, false},
		{"absent", http.StatusNotFound, `{"message":"Not Found"}`, false, false},
		{"server error is not absence", http.StatusBadGateway, "oops", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/o/r/contents/.factory/config.yaml" || r.URL.Query().Get("ref") != "abc" ||
					r.Header.Get("Accept") != "application/vnd.github.raw+json" {
					http.Error(w, "unexpected request "+r.URL.String(), http.StatusBadRequest)
					return
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			got, found, err := NewClient("pat", srv.URL).FileAt(t.Context(), "o/r", ".factory/config.yaml", "abc")
			if (err != nil) != tt.wantErr || found != tt.wantFound {
				t.Fatalf("FileAt = %q, %v, %v", got, found, err)
			}
			if found && string(got) != tt.body {
				t.Errorf("content = %q", got)
			}
		})
	}
}

func TestBranchHead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/git/ref/heads/agent/gc-2/add-health":
			_, _ = w.Write([]byte(`{"ref":"refs/heads/agent/gc-2/add-health","object":{"sha":"15d3b56dc87b1d230a78cc373b793a9307ad9422","type":"commit"}}`))
		case "/repos/o/r/git/ref/heads/agent/gc-9/missing":
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		default:
			http.Error(w, "boom", http.StatusBadGateway)
		}
	}))
	defer srv.Close()
	c := NewClient("pat", srv.URL)
	if sha, found, err := c.BranchHead(t.Context(), "o/r", "agent/gc-2/add-health"); err != nil || !found || sha != "15d3b56dc87b1d230a78cc373b793a9307ad9422" {
		t.Errorf("existing branch: %q %v %v", sha, found, err)
	}
	if _, found, err := c.BranchHead(t.Context(), "o/r", "agent/gc-9/missing"); err != nil || found {
		t.Errorf("missing branch: found %v, err %v", found, err)
	}
	if _, _, err := c.BranchHead(t.Context(), "o/r", "agent/gc-1/other"); err == nil {
		t.Error("server error reported as absence")
	}
}

func TestComparePages(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		files := []ChangedFile{}
		n := 100
		if r.URL.Query().Get("page") == "2" {
			n = 1
		}
		for i := range n {
			files = append(files, ChangedFile{Filename: fmt.Sprintf("f%d.go", i), Status: "modified", Patch: "+x"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	}))
	defer srv.Close()
	files, complete, err := NewClient("pat", srv.URL).Compare(t.Context(), "o/r", "abc", "agent/gc-1/x")
	if err != nil || !complete || len(files) != 101 {
		t.Fatalf("files %d, complete %v, err %v", len(files), complete, err)
	}
	if paths[0] != "/repos/o/r/compare/abc...agent/gc-1/x?page=1&per_page=100" || len(paths) != 2 {
		t.Errorf("requests = %v", paths)
	}
}

func TestCompareTooManyFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		files := make([]ChangedFile, 100)
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	}))
	defer srv.Close()
	files, complete, err := NewClient("pat", srv.URL).Compare(t.Context(), "o/r", "a", "b")
	if err != nil || complete || len(files) != 3000 {
		t.Fatalf("files %d, complete %v, err %v", len(files), complete, err)
	}
}

func TestListFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/git/trees/abc" || r.URL.Query().Get("recursive") != "1" {
			t.Errorf("request %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"tree":[{"path":"go.mod","type":"blob"},{"path":"web","type":"tree"},{"path":"web/bun.lock","type":"blob"}],"truncated":false}`))
	}))
	defer srv.Close()
	paths, complete, err := NewClient("pat", srv.URL).ListFiles(t.Context(), "o/r", "abc")
	if err != nil || !complete || len(paths) != 2 || paths[1] != "web/bun.lock" {
		t.Fatalf("paths %v, complete %v, err %v", paths, complete, err)
	}
}

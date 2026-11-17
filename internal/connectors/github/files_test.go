package github

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// M7, D117: the flux driver reads, writes and lists the history of a GitOps file.
func TestFiles(t *testing.T) {
	var put map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/cd/contents/apps/stg/kustomization.yaml":
			if r.URL.Query().Get("ref") != "main" {
				http.Error(w, "bad ref", http.StatusBadRequest)
				return
			}
			// GitHub wraps base64 content at 60 characters.
			enc := base64.StdEncoding.EncodeToString([]byte("images:\n  - name: app\n    newTag: sha-1\n"))
			_ = json.NewEncoder(w).Encode(map[string]string{"type": "file", "sha": "blob1", "encoding": "base64", "content": enc[:20] + "\n" + enc[20:]})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/cd/contents/missing.yaml":
			http.NotFound(w, r)
		case r.Method == http.MethodPut && r.URL.Path == "/repos/o/cd/contents/apps/stg/kustomization.yaml":
			_ = json.NewDecoder(r.Body).Decode(&put)
			if put["sha"] != "blob1" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"is at blob2 but expected blob0"}`))
				return
			}
			_, _ = w.Write([]byte(`{"commit":{"sha":"c2"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/cd/commits":
			q := r.URL.Query()
			if q.Get("sha") != "main" || q.Get("path") != "apps/stg/kustomization.yaml" || q.Get("per_page") != "20" {
				http.Error(w, "bad query "+r.URL.RawQuery, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`[{"sha":"c2","commit":{"message":"Release b"},"parents":[{"sha":"c1"}]},{"sha":"c0","commit":{"message":"root"},"parents":[]}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewClient("pat", srv.URL)
	ctx := t.Context()

	f, found, err := c.ReadFile(ctx, "o/cd", "apps/stg/kustomization.yaml", "main")
	if err != nil || !found || f.BlobSHA != "blob1" || string(f.Content) != "images:\n  - name: app\n    newTag: sha-1\n" {
		t.Fatalf("ReadFile = %+v, %v, %v", f, found, err)
	}
	if _, found, err := c.ReadFile(ctx, "o/cd", "missing.yaml", "main"); found || err != nil {
		t.Errorf("missing file = %v, %v", found, err)
	}

	commit, err := c.WriteFile(ctx, "o/cd", "apps/stg/kustomization.yaml", "main", []byte("new"), "blob1", "Release b")
	if err != nil || commit != "c2" {
		t.Fatalf("WriteFile = %q, %v", commit, err)
	}
	if put["branch"] != "main" || put["message"] != "Release b" || put["content"] != base64.StdEncoding.EncodeToString([]byte("new")) {
		t.Errorf("put = %v", put)
	}
	if _, err := c.WriteFile(ctx, "o/cd", "apps/stg/kustomization.yaml", "main", []byte("new"), "blob0", "x"); !errors.Is(err, ErrFileChanged) {
		t.Errorf("stale write = %v, want ErrFileChanged", err)
	}

	commits, err := c.FileCommits(ctx, "o/cd", "apps/stg/kustomization.yaml", "main", 20)
	if err != nil || len(commits) != 2 || commits[0].SHA != "c2" || commits[0].ParentSHA != "c1" || commits[0].Message != "Release b" || commits[1].ParentSHA != "" {
		t.Errorf("FileCommits = %+v, %v", commits, err)
	}
}

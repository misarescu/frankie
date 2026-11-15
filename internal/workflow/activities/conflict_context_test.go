package activities

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/connectors/github"
)

// M6 (D89): the coder sees, per conflicting file, both sides' changes since
// the fork, so it can keep both where both belong.
func TestConflictContext(t *testing.T) {
	const head, base = queuedHead, queuedBase
	sides := map[string][]github.ChangedFile{
		head + "..." + base: { // what main changed
			{Filename: "web/App.jsx", Status: "modified", Patch: "+import Shelves from './Shelves'"},
			{Filename: "go.sum", Status: "modified"},
		},
		base + "..." + head: { // what the branch changed
			{Filename: "web/App.jsx", Status: "modified", Patch: "+import Reviews from './Reviews'"},
			{Filename: "big.txt", Status: "modified", Patch: strings.Repeat("x", conflictPatchBytes+100)},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/repos/misarescu/Goodreads-Clone/compare/")
		files, ok := sides[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	}))
	defer srv.Close()
	_, in := readyIssue(t)
	a := &Activities{GitHub: github.NewClient("pat", srv.URL), Log: quietLog()}
	v, err := activityEnv(a).ExecuteActivity(a.ConflictContext, ConflictRequest{Issue: in, HeadSHA: head, BaseSHA: base,
		Files: []string{"web/App.jsx", "go.sum", "big.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	_ = v.Get(&got)
	for _, want := range []string{
		"### web/App.jsx", "Changed on main since your branch forked:\n```diff\n+import Shelves", "Changed on your branch:\n```diff\n+import Reviews",
		"### go.sum", "no text diff", "Changed on your branch: nothing.",
		"### big.txt", "... (cut)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context lacks %q:\n%s", want, got)
		}
	}
}

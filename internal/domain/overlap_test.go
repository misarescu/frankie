package domain

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestExtractFileHints(t *testing.T) {
	text := "Change `internal/api/issues.go` and `web/src/`, see `./docs/spec/02-domain-model.md`.\n" +
		"Not paths: `true`, `cfg.Repos`, `go test ./...`, `/etc/passwd`, `../up/x.go`, `https://x.y/z`.\n" +
		"Again `internal/api/issues.go`, and a file at the root: `README.md`."
	want := []string{"internal/api/issues.go", "web/src/", "docs/spec/02-domain-model.md"}
	if got := ExtractFileHints(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractFileHints = %q, want %q", got, want)
	}
	var many strings.Builder
	for i := range 80 {
		many.WriteString("`d/f" + strings.Repeat("x", i) + ".go` ")
	}
	if got := ExtractFileHints(many.String()); len(got) != maxFileHints {
		t.Errorf("got %d hints, want at most %d", len(got), maxFileHints)
	}
}

func TestSharedScopes(t *testing.T) {
	tests := []struct {
		a, b []string
		want []string
	}{
		{[]string{"internal/api/issues.go"}, []string{"internal/api/schedule.go"}, []string{"internal/api"}},
		{[]string{"internal/api/"}, []string{"internal/api/v2/x.go"}, []string{"internal/api/v2"}},
		{[]string{"internal/api/issues.go"}, []string{"internal/apiv2/x.go"}, nil}, // not a parent: a sibling with a shared prefix
		{[]string{"README.md"}, []string{"internal/api/x.go"}, nil},                // the root contains everything; only matches itself
		{[]string{"README.md"}, []string{"go.mod"}, []string{"."}},
		{[]string{"web/src/App.tsx", "internal/store/x.go"}, []string{"internal/store/y.go", "web/src/z.ts"}, []string{"internal/store", "web/src"}},
		{nil, []string{"a/b.go"}, nil},
	}
	for _, tt := range tests {
		if got := SharedScopes(tt.a, tt.b); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SharedScopes(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

// FR-MQ-4: before parallel issues start, likely overlap with in-flight work is reported.
func TestFindOverlaps(t *testing.T) {
	p, other := uuid.New(), uuid.New()
	is := func(key string, project uuid.UUID, st IssueStatus, paths ...string) PathsIssue {
		return PathsIssue{ID: uuid.New(), Key: key, ProjectID: project, Status: st, Paths: paths}
	}
	cand := is("c", p, IssueReady, "internal/api/issues.go")
	all := []PathsIssue{
		cand,
		is("running", p, IssueRunning, "internal/api/schedule.go"),
		is("waiting-merge", p, IssueAwaitingHuman, "internal/api/"),
		is("queued", p, IssueQueued, "web/src/App.tsx"),               // in flight, but elsewhere
		is("merged", p, IssueMerged, "internal/api/x.go"),             // on main already
		is("ready", p, IssueReady, "internal/api/y.go"),               // not started
		is("other-project", other, IssueRunning, "internal/api/z.go"), // another repo
		is("no-paths", p, IssueRunning),
	}
	got := FindOverlaps([]PathsIssue{cand}, all)
	want := []Overlap{
		{Key: "c", With: "running", Paths: []string{"internal/api"}},
		{Key: "c", With: "waiting-merge", Paths: []string{"internal/api"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindOverlaps = %+v, want %+v", got, want)
	}

	// Two candidates that may start together are compared with each other, once.
	other1 := is("c2", p, IssueReady, "internal/api/z.go")
	other2 := is("c3", p, IssueReady, "docs/x.md")
	got = FindOverlaps([]PathsIssue{cand, other1, other2}, []PathsIssue{cand, other1, other2})
	want = []Overlap{{Key: "c", With: "c2", Paths: []string{"internal/api"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindOverlaps among candidates = %+v, want %+v", got, want)
	}
}

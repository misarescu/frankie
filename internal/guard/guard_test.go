package guard

import (
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/config"
)

var patterns = config.ProtectedPatterns([]string{"deploy/**/networkpolicy*", "cmd/runner/**"})

// Invariant 6: protected paths force a human gate, also when renamed away.
func TestCheckProtectedPaths(t *testing.T) {
	r := Check([]File{
		{Path: "internal/server/routes.go", Status: Modified, Patch: "+x"},
		{Path: "deploy/helm/frankie/templates/networkpolicy-agents.yaml", Status: Modified, Patch: "+x"},
		{Path: "cmd/tool/main.go", PreviousPath: "cmd/runner/main.go", Status: Renamed},
		{Path: ".factory/protected-paths.yaml", Status: Modified, Patch: "-  - cmd/runner/**"},
	}, patterns, "")
	if r.Clean() || len(r.Protected) != 3 {
		t.Fatalf("report = %+v", r)
	}
	want := []ProtectedChange{
		{"deploy/helm/frankie/templates/networkpolicy-agents.yaml", "deploy/**/networkpolicy*"},
		{"cmd/runner/main.go", "cmd/runner/**"},
		{".factory/protected-paths.yaml", ".factory/**"},
	}
	for i, w := range want {
		if r.Protected[i] != w {
			t.Errorf("protected[%d] = %+v, want %+v", i, r.Protected[i], w)
		}
	}
	if !strings.Contains(r.Reason(), "touches protected paths: deploy/helm/frankie/templates/networkpolicy-agents.yaml (deploy/**/networkpolicy*),") {
		t.Errorf("reason = %q", r.Reason())
	}
}

// Skipped or deleted tests force a human gate (docs/spec/05).
func TestCheckTests(t *testing.T) {
	tests := []struct {
		name string
		file File
		want string
	}{
		{"deleted test file", File{Path: "internal/api/api_test.go", Status: Removed, Patch: "-func TestA(t *testing.T) {}"}, "deleted test file internal/api/api_test.go"},
		{"test renamed away", File{Path: "internal/api/helpers.go", PreviousPath: "internal/api/api_test.go", Status: Renamed}, "renamed to internal/api/helpers.go, which is not a test file"},
		{"go skip", File{Path: "a_test.go", Status: Modified, Patch: "@@ -1 +1 @@\n+\tt.Skip(\"flaky\")"}, "a_test.go adds a Go test skip"},
		{"go skipf on another receiver", File{Path: "a_test.go", Status: Modified, Patch: "+\tb.Skipf(\"later\")"}, "adds a Go test skip"},
		{"build ignore", File{Path: "a_test.go", Status: Modified, Patch: "+//go:build ignore"}, "go:build ignore"},
		{"ginkgo pending", File{Path: "e2e/sandbox_test.go", Status: Modified, Patch: "+\tPIt(\"blocks egress\", func() {})"}, "pending Ginkgo spec"},
		{"js it.skip", File{Path: "web/src/events.test.ts", Status: Modified, Patch: "+it.skip(\"renders\", () => {})"}, "skipped JavaScript test"},
		{"js xit", File{Path: "src/__tests__/app.js", Status: Modified, Patch: "+  xit('works', () => {})"}, "disabled JavaScript test"},
		{"pytest skip", File{Path: "tests/test_api.py", Status: Modified, Patch: "+@pytest.mark.skip(reason=\"x\")"}, "skipped Python test"},
		{"junit disabled", File{Path: "src/FooTest.java", Status: Modified, Patch: "+  @Disabled"}, "disabled JVM test"},
		{"fewer tests", File{Path: "a_test.go", Status: Modified, Patch: "-func TestA(t *testing.T) {\n-func TestB(t *testing.T) {\n+func TestC(t *testing.T) {"}, "1 fewer tests (2 removed, 1 added)"},
		{"uninspectable test diff", File{Path: "big_test.go", Status: Modified, PatchMissing: true}, "could not inspect the diff of test file big_test.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Check([]File{tt.file}, patterns, "")
			if r.Clean() || !strings.Contains(strings.Join(r.Tests, "; "), tt.want) {
				t.Errorf("tests = %v, want %q", r.Tests, tt.want)
			}
		})
	}
}

func TestCheckLeavesOrdinaryChangesAlone(t *testing.T) {
	r := Check([]File{
		{Path: "internal/server/routes.go", Status: Modified, Patch: "+\tif skip { return }\n-func old() {}"},
		{Path: "internal/server/routes_test.go", Status: Modified, Patch: "+func TestNotFound(t *testing.T) {\n+\t// t.Skip(\"later\") was here\n"},
		{Path: "internal/server/version_test.go", Status: Added, Patch: "+func TestVersion(t *testing.T) {"},
		{Path: "docs/notes.md", Status: Removed, Patch: "-old"},
	}, patterns, "")
	// A skip call inside a comment still counts; better a spurious human gate than a missed skip.
	if len(r.Protected) != 0 || len(r.Tests) != 1 || !strings.Contains(r.Tests[0], "routes_test.go adds a Go test skip") {
		t.Errorf("report = %+v", r)
	}
	clean := Check([]File{{Path: "internal/server/routes.go", Status: Modified, Patch: "+x"}, {Path: "x_test.go", Status: Added, Patch: "+func TestX(t *testing.T) {"}}, patterns, "")
	if !clean.Clean() {
		t.Errorf("ordinary change = %+v", clean)
	}
}

func TestCheckIncompleteDiff(t *testing.T) {
	r := Check(nil, patterns, "the diff has more files than the provider returns")
	if r.Clean() || !strings.Contains(r.Reason(), "more files than the provider returns") {
		t.Errorf("report = %+v, reason %q", r, r.Reason())
	}
}

func TestIsTestFile(t *testing.T) {
	for p, want := range map[string]bool{
		"internal/api/api_test.go": true, "web/src/events.test.ts": true, "a/b.spec.tsx": true, "x.test.mjs": true,
		"tests/test_api.py": true, "api_test.py": true, "src/__tests__/app.js": true, "src/FooTest.java": true,
		"internal/api/api.go": false, "testdata/fixture.json": false, "contest.go": false, "": false,
	} {
		if got := IsTestFile(p); got != want {
			t.Errorf("IsTestFile(%q) = %v", p, got)
		}
	}
}

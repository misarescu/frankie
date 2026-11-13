// Package guard finds the changes that force a human gate whatever the
// workflow says (invariant 6, docs/spec/05): edits to protected paths, and
// deleted or skipped tests. It runs in the control plane on the provider's diff
// of the change, never in the agent's pod, and is pure: callers fetch the diff.
package guard

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/misarescu/frankie/internal/config"
)

// FileStatus is how a change touched a file.
type FileStatus string

// File statuses.
const (
	Added    FileStatus = "added"
	Modified FileStatus = "modified"
	Removed  FileStatus = "removed"
	Renamed  FileStatus = "renamed"
)

// File is one changed file of a diff.
type File struct {
	Path         string
	PreviousPath string // for renames
	Status       FileStatus
	// Patch is the unified diff hunks of the file. PatchMissing means the
	// provider left it out (a large or binary file), so it cannot be inspected.
	Patch        string
	PatchMissing bool
}

// ProtectedChange is a changed file that a protected-path pattern covers.
type ProtectedChange struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
}

// Report is what a change contains that a human must approve.
type Report struct {
	Protected []ProtectedChange `json:"protected,omitempty"`
	Tests     []string          `json:"tests,omitempty"`
	// Incomplete says why the diff could not be inspected in full; an
	// incomplete diff needs a human too.
	Incomplete string `json:"incomplete,omitempty"`
}

// Clean reports a change nobody has to approve.
func (r Report) Clean() bool { return len(r.Protected) == 0 && len(r.Tests) == 0 && r.Incomplete == "" }

// Reason explains to the approver why the gate is forced.
func (r Report) Reason() string {
	var b strings.Builder
	b.WriteString("This change needs a human decision, whatever the workflow says.")
	if len(r.Protected) > 0 {
		b.WriteString(" It touches protected paths:")
		for i, p := range r.Protected {
			if i == 20 {
				fmt.Fprintf(&b, " and %d more", len(r.Protected)-i)
				break
			}
			fmt.Fprintf(&b, " %s (%s)", p.Path, p.Pattern)
			if i < len(r.Protected)-1 && i < 19 {
				b.WriteString(",")
			}
		}
		b.WriteString(".")
	}
	if len(r.Tests) > 0 {
		b.WriteString(" Tests: " + strings.Join(r.Tests, "; ") + ".")
	}
	if r.Incomplete != "" {
		b.WriteString(" " + r.Incomplete + ".")
	}
	return b.String()
}

// Check inspects a diff against the protected-path patterns. incomplete is set
// when the caller could not fetch the whole diff.
func Check(files []File, patterns []string, incomplete string) Report {
	r := Report{Incomplete: incomplete}
	removedTests, addedTests := 0, 0
	for _, f := range files {
		for _, p := range []string{f.Path, f.PreviousPath} {
			if p == "" {
				continue
			}
			if pattern, ok := config.MatchProtected(patterns, p); ok {
				r.Protected = append(r.Protected, ProtectedChange{Path: p, Pattern: pattern})
				break
			}
		}

		wasTest, isTest := IsTestFile(f.PreviousPath) || (f.Status != Renamed && IsTestFile(f.Path)), IsTestFile(f.Path)
		switch {
		case f.Status == Removed && isTest:
			r.Tests = append(r.Tests, "deleted test file "+f.Path)
		case f.Status == Renamed && wasTest && !isTest:
			r.Tests = append(r.Tests, fmt.Sprintf("test file %s renamed to %s, which is not a test file", f.PreviousPath, f.Path))
		}
		if !isTest && !wasTest {
			continue
		}
		if f.PatchMissing {
			r.Tests = append(r.Tests, "could not inspect the diff of test file "+f.Path)
			continue
		}
		for _, line := range strings.Split(f.Patch, "\n") {
			switch {
			case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
				added := line[1:]
				if m := skipMarker(added); m != "" {
					r.Tests = append(r.Tests, fmt.Sprintf("%s adds %s", f.Path, m))
				}
				if testDecl.MatchString(added) {
					addedTests++
				}
			case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
				if testDecl.MatchString(line[1:]) {
					removedTests++
				}
			}
		}
	}
	if removedTests > addedTests {
		r.Tests = append(r.Tests, fmt.Sprintf("%d fewer tests (%d removed, %d added)", removedTests-addedTests, removedTests, addedTests))
	}
	return r
}

var testFileRE = regexp.MustCompile(`(^|/)(` +
	`[^/]+_test\.go|` + // Go
	`[^/]+\.(test|spec)\.[cm]?[jt]sx?|` + // JavaScript, TypeScript
	`test_[^/]+\.py|[^/]+_test\.py|` + // Python
	`[^/]+(Test|Tests|IT)\.(java|kt)` + // JVM
	`)$|(^|/)__tests__/`)

// IsTestFile reports whether a repo path holds tests, by common conventions.
func IsTestFile(p string) bool { return p != "" && testFileRE.MatchString(p) }

// skipMarkers are added lines that skip tests, with what to call them.
var skipMarkers = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`\.Skip(Now|f)?\(`), "a Go test skip"},
	{regexp.MustCompile(`^\s*//\s*go:build\s+ignore\b`), "a go:build ignore constraint"},
	{regexp.MustCompile(`\b(PIt|PDescribe|PContext|PEntry|PSpecify|XIt|XDescribe|XContext|XEntry)\(`), "a pending Ginkgo spec"},
	{regexp.MustCompile(`\b(it|test|describe|context|suite)\.skip\(`), "a skipped JavaScript test"},
	{regexp.MustCompile(`\b(xit|xtest|xdescribe|xcontext)\(`), "a disabled JavaScript test"},
	{regexp.MustCompile(`@pytest\.mark\.(skip|xfail)|\bpytest\.skip\(|@unittest\.skip`), "a skipped Python test"},
	{regexp.MustCompile(`@(Disabled|Ignore)\b`), "a disabled JVM test"},
}

func skipMarker(line string) string {
	for _, m := range skipMarkers {
		if m.re.MatchString(line) {
			return m.name
		}
	}
	return ""
}

// testDecl matches a line declaring one test.
var testDecl = regexp.MustCompile(`^func (Test|Fuzz)\w*\(|` + // Go
	`^\s*(it|test)\(\s*['"` + "`" + `]|` + // JavaScript
	`^\s*(async\s+)?def test_\w*\(|` + // Python
	`^\s*(It|Entry)\(\s*"`) // Ginkgo

package domain

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// maxFileHints bounds the hints read from one issue's text.
const maxFileHints = 50

// backticked matches `inline code` spans that look like repo paths.
var backticked = regexp.MustCompile("`([A-Za-z0-9_.@+\\-/]+)`")

// ExtractFileHints reads the repo paths an issue's text names in backticks, such
// as `internal/api/issues.go` or `web/src/` (D63). Only spans containing a slash
// count, so code like `true` or `cfg.Repos` is not mistaken for a path.
func ExtractFileHints(text string) []string {
	var out []string
	for _, m := range backticked.FindAllStringSubmatch(text, -1) {
		p := strings.TrimPrefix(m[1], "./")
		if !strings.Contains(strings.TrimSuffix(p, "/"), "/") && !strings.HasSuffix(p, "/") {
			continue
		}
		if strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.Contains(p, "//") {
			continue // absolute, escaping or URL-ish: not a repo path
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
		if len(out) == maxFileHints {
			break
		}
	}
	return out
}

// PathScope is the directory a path belongs to: the path itself for a directory
// ("web/src/"), else its parent ("internal/api" for "internal/api/issues.go"),
// and "." for a file at the repo root.
func PathScope(p string) string {
	if strings.HasSuffix(p, "/") {
		return path.Clean(p)
	}
	return path.Dir(path.Clean(p))
}

// scopesOverlap reports whether two scopes are the same directory or one contains
// the other. The root only matches itself: everything is under it.
func scopesOverlap(a, b string) bool {
	if a == "." || b == "." {
		return a == b
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// SharedScopes returns the directories two path sets both touch (D63), the more
// specific one of each overlapping pair, sorted.
func SharedScopes(a, b []string) []string {
	var out []string
	for _, pa := range a {
		sa := PathScope(pa)
		for _, pb := range b {
			sb := PathScope(pb)
			if !scopesOverlap(sa, sb) {
				continue
			}
			s := sa
			if len(sb) > len(sa) {
				s = sb
			}
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	slices.Sort(out)
	return out
}

// PathsIssue is an issue with the paths it is expected to touch: its file hints
// and the files its latest change touched.
type PathsIssue struct {
	ID        uuid.UUID
	Key       string
	ProjectID uuid.UUID
	Status    IssueStatus
	Paths     []string
}

// InFlight reports whether the issue's change is under way but not on main yet:
// running, waiting for a person, or in the merge queue.
func (p PathsIssue) InFlight() bool {
	return p.Status == IssueRunning || p.Status == IssueAwaitingHuman || p.Status == IssueQueued
}

// Overlap warns that an issue likely touches the same code as one in flight.
type Overlap struct {
	Key   string   // the issue about to start
	With  string   // an issue in flight, or another that may start alongside
	Paths []string // shared directories
}

// FindOverlaps compares each candidate with the in-flight issues of its project,
// and the candidates with each other, each pair once, since they may run in
// parallel (D63, FR-MQ-4). Issues without paths cannot be compared and produce
// nothing.
func FindOverlaps(candidates []PathsIssue, all []PathsIssue) []Overlap {
	var out []Overlap
	add := func(c, f PathsIssue) {
		if f.ID == c.ID || f.ProjectID != c.ProjectID {
			return
		}
		if shared := SharedScopes(c.Paths, f.Paths); len(shared) > 0 {
			out = append(out, Overlap{Key: c.Key, With: f.Key, Paths: shared})
		}
	}
	for i, c := range candidates {
		for _, f := range all {
			if f.InFlight() {
				add(c, f)
			}
		}
		for _, f := range candidates[i+1:] {
			if !f.InFlight() {
				add(c, f)
			}
		}
	}
	return out
}

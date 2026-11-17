package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// Bump is the semver part a release raises (D129).
type Bump string

// Bumps.
const (
	BumpMajor Bump = "major"
	BumpMinor Bump = "minor"
	BumpPatch Bump = "patch"
)

// Valid reports whether b is a known bump.
func (b Bump) Valid() bool { return b == BumpMajor || b == BumpMinor || b == BumpPatch }

// NotesSource says who wrote a release's notes (D129).
type NotesSource string

// Notes sources: the release agent, or the fallback when it failed.
const (
	NotesByAgent    NotesSource = "agent"
	NotesByFallback NotesSource = "fallback"
)

// Valid reports whether s is a known source.
func (s NotesSource) Valid() bool { return s == NotesByAgent || s == NotesByFallback }

// Version is a release's semantic version, written vMAJOR.MINOR.PATCH.
type Version struct {
	Major, Minor, Patch int
}

// FirstVersion is the version of a repo's first versioned release (D129).
var FirstVersion = Version{Minor: 1}

var versionRE = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// ParseVersion reads vMAJOR.MINOR.PATCH, without pre-release or build parts.
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("version %q is not vMAJOR.MINOR.PATCH", s)
	}
	var v Version
	for i, p := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("version %q: %w", s, err)
		}
		*p = n
	}
	return v, nil
}

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Next is the version after v for bump b. Below 1.0.0 a breaking change
// bumps the minor part, as semver allows for initial development; reaching
// 1.0.0 is a person's call, never a bump's (D129).
func (v Version) Next(b Bump) Version {
	switch {
	case b == BumpPatch:
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	case b == BumpMajor && v.Major > 0:
		return Version{Major: v.Major + 1}
	default:
		return Version{Major: v.Major, Minor: v.Minor + 1}
	}
}

// NextVersion is the version a release gets: FirstVersion when the repo has
// none yet, otherwise prev bumped by b.
func NextVersion(prev Version, hasPrev bool, b Bump) Version {
	if !hasPrev {
		return FirstVersion
	}
	return prev.Next(b)
}

// FallbackBump is the bump when the release agent proposed none (D129): a
// patch when every issue is a bug, otherwise a minor bump.
func FallbackBump(types []IssueType) Bump {
	if len(types) == 0 {
		return BumpPatch
	}
	for _, t := range types {
		if t != IssueBug {
			return BumpMinor
		}
	}
	return BumpPatch
}

// NewProviderRelease is the provider release a succeeded release's version
// is published as (D129): on GitHub, a Release, which creates its tag.
type NewProviderRelease struct {
	Tag  string // the version, e.g. v0.2.0
	SHA  string // the release commit
	Name string
	Body string // the release notes, Markdown
}

// ErrTagTaken reports a version tag that already names another commit.
var ErrTagTaken = errors.New("the tag already names another commit")

// Validate checks the release names a version and a full commit.
func (n NewProviderRelease) Validate() error {
	if _, err := ParseVersion(n.Tag); err != nil {
		return err
	}
	if !fullSHARE.MatchString(n.SHA) {
		return fmt.Errorf("commit %q is not a full SHA", n.SHA)
	}
	return nil
}

var fullSHARE = regexp.MustCompile(`^[0-9a-f]{40}$`)

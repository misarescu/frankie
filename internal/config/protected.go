package config

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

// ProtectedPathsFile lists a repo's own protected paths (docs/spec/05). It is
// read from the base branch, never from the change under review, and is
// itself protected.
const ProtectedPathsFile = ".factory/protected-paths.yaml"

// DefaultProtectedPaths apply to every project (invariant 6); a repo's file can
// add to them but not remove any. Repo-specific guardrails (NetworkPolicies,
// RBAC, the runner, budgets) belong in the repo's file.
var DefaultProtectedPaths = []string{
	// The factory's own configuration: workflow and gate definitions, roles,
	// the protected-paths list, project config, the Dockerfile escape hatch.
	".factory/**",
	// Toolchain files.
	"mise.toml", "mise.lock", ".mise.toml", ".mise/**",
	// CI configuration.
	".github/workflows/**", ".github/actions/**", ".gitlab-ci.yml", ".circleci/**",
	// Lint configuration.
	".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json",
	"**/.eslintrc*", "**/eslint.config.*", "**/biome.json", "**/biome.jsonc",
	".pre-commit-config.yaml",
}

const maxProtectedPatterns = 200

type protectedPathsFile struct {
	Paths []string `json:"paths"`
}

// ParseProtectedPaths parses a repo's protected-paths file: `paths:`, a list of
// slash-separated glob patterns relative to the repo root, where `*` and `?`
// match within one path segment and `**` matches any number of segments.
func ParseProtectedPaths(raw []byte) ([]string, error) {
	var f protectedPathsFile
	if err := yaml.UnmarshalStrict(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", ProtectedPathsFile, err)
	}
	if len(f.Paths) > maxProtectedPatterns {
		return nil, fmt.Errorf("%s: %d patterns, at most %d", ProtectedPathsFile, len(f.Paths), maxProtectedPatterns)
	}
	var errs []error
	for i, p := range f.Paths {
		if err := validGlob(p); err != nil {
			errs = append(errs, fmt.Errorf("paths[%d]: %w", i, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%s: %w", ProtectedPathsFile, err)
	}
	return f.Paths, nil
}

// ProtectedPatterns is the defaults followed by the repo's own patterns.
func ProtectedPatterns(repo []string) []string {
	out := slices.Clone(DefaultProtectedPaths)
	for _, p := range repo {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// MatchProtected returns the first pattern that matches the repo path p.
func MatchProtected(patterns []string, p string) (string, bool) {
	for _, pattern := range patterns {
		if matchGlob(pattern, p) {
			return pattern, true
		}
	}
	return "", false
}

func validGlob(p string) error {
	switch {
	case strings.TrimSpace(p) == "":
		return errors.New("empty pattern")
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("pattern %q must be relative to the repo root", p)
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return fmt.Errorf("pattern %q has an empty, . or .. segment", p)
		case seg == "**":
		case strings.Contains(seg, "**"):
			return fmt.Errorf("pattern %q: ** must be a whole segment", p)
		default:
			if _, err := path.Match(seg, ""); err != nil {
				return fmt.Errorf("pattern %q: %w", p, err)
			}
		}
	}
	return nil
}

// matchGlob matches a slash-separated path against a pattern whose `**`
// segments match zero or more path segments.
func matchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

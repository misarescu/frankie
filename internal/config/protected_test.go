package config

import (
	"slices"
	"strings"
	"testing"
)

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"mise.toml", "mise.toml", true},
		{"mise.toml", "sub/mise.toml", false},
		{".factory/**", ".factory/workflows/default-build.yaml", true},
		{".factory/**", ".factory", true},
		{".factory/**", ".factoryx/a", false},
		{"deploy/**/networkpolicy*", "deploy/helm/frankie/templates/networkpolicy-agents.yaml", true},
		{"deploy/**/networkpolicy*", "deploy/networkpolicy.yaml", true},
		{"deploy/**/networkpolicy*", "deploy/helm/values.yaml", false},
		{"**/.eslintrc*", ".eslintrc.json", true},
		{"**/.eslintrc*", "web/.eslintrc.cjs", true},
		{"cmd/runner/**", "cmd/runner/main.go", true},
		{"cmd/runner/**", "cmd/runnerx/main.go", false},
		{"internal/*/policy.go", "internal/egress/policy.go", true},
		{"internal/*/policy.go", "internal/a/b/policy.go", false},
	}
	for _, tt := range tests {
		if got := matchGlob(tt.pattern, tt.path); got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestDefaultProtectedPaths(t *testing.T) {
	for _, p := range DefaultProtectedPaths {
		if err := validGlob(p); err != nil {
			t.Errorf("default %q: %v", p, err)
		}
	}
	for _, p := range []string{".factory/protected-paths.yaml", ".factory/workflows/default-build.yaml", "mise.toml", "mise.lock",
		".github/workflows/ci.yml", ".golangci.yml", "frontend/eslint.config.js"} {
		if _, ok := MatchProtected(DefaultProtectedPaths, p); !ok {
			t.Errorf("%s is not protected by default", p)
		}
	}
	for _, p := range []string{"internal/server/routes.go", "README.md", "frontend/src/App.tsx"} {
		if pattern, ok := MatchProtected(DefaultProtectedPaths, p); ok {
			t.Errorf("%s matched %q", p, pattern)
		}
	}
}

func TestParseProtectedPaths(t *testing.T) {
	repo, err := ParseProtectedPaths([]byte("paths:\n  - deploy/**/networkpolicy*\n  - cmd/runner/**\n  - mise.toml\n"))
	if err != nil {
		t.Fatal(err)
	}
	all := ProtectedPatterns(repo)
	if !slices.Contains(all, "cmd/runner/**") || !slices.Contains(all, ".factory/**") || len(all) != len(DefaultProtectedPaths)+2 {
		t.Errorf("patterns = %v (defaults first, no duplicates)", all)
	}
	// A repo cannot remove a default: an empty file still protects them.
	empty, err := ParseProtectedPaths([]byte("paths: []\n"))
	if err != nil || !slices.Equal(ProtectedPatterns(empty), DefaultProtectedPaths) {
		t.Errorf("empty file: %v, %v", ProtectedPatterns(empty), err)
	}

	for raw, want := range map[string]string{
		"paths: [/etc/passwd]\n": "relative to the repo root",
		"paths: [a/../b]\n":      ". or .. segment",
		"paths: [\"a/**b\"]\n":   "whole segment",
		"paths: [\"[\"]\n":       "syntax error",
		"paths: [\"\"]\n":        "empty pattern",
		"exclude: [mise.toml]\n": "unknown field",
	} {
		if _, err := ParseProtectedPaths([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", raw, err, want)
		}
	}
}

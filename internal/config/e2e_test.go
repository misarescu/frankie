package config

import (
	"strings"
	"testing"
)

// M7, D116: a project says how its end-to-end suite runs.
func TestParseProjectE2E(t *testing.T) {
	p, err := ParseProject([]byte("e2e: {command: mise run e2e}\nstages: [{name: stg, driver: flux, verify: e2e-suite, url: 'https://stg'}]\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.E2E == nil || p.E2E.Command != "mise run e2e" || p.E2E.ResultsPath() != DefaultE2EResults {
		t.Errorf("e2e = %+v", p.E2E)
	}
	p, err = ParseProject([]byte("e2e: {command: bru run api, results: api/out/junit.xml}\n"), 0)
	if err != nil || p.E2E.ResultsPath() != "api/out/junit.xml" {
		t.Errorf("e2e = %+v, %v", p.E2E, err)
	}
}

func TestParseProjectE2ERejects(t *testing.T) {
	tests := []struct{ name, yaml, want string }{
		{"no command", "e2e: {results: x.xml}", "e2e.command is required"},
		{"absolute results", "e2e: {command: x, results: /tmp/x.xml}", "clean path inside"},
		{"results outside", "e2e: {command: x, results: ../x.xml}", "clean path inside"},
		{"unclean results", "e2e: {command: x, results: a//x.xml}", "clean path inside"},
		{"verify without e2e", "stages: [{name: stg, driver: flux, verify: e2e-suite, url: 'https://stg'}]", "verify e2e-suite needs e2e.command"},
		{"unknown field", "e2e: {command: x, tags: [a]}", "tags"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseProject([]byte(tt.yaml), 0); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseProject = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

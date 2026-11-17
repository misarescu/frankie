package domain

import (
	"strings"
	"testing"
)

// M7 (D129): the release agent's notes and analysis are checked strictly.
func TestParseReleaseNotes(t *testing.T) {
	n, err := ParseReleaseNotes([]byte(`{"bump":"minor","reason":"a new endpoint","notes":"- gc-77: similar books","breaking":[]}`))
	if err != nil || n.Bump != BumpMinor || n.Notes != "- gc-77: similar books" {
		t.Fatalf("ParseReleaseNotes = %+v, %v", n, err)
	}
	for raw, want := range map[string]string{
		`{"bump":"huge","reason":"r","notes":"n"}`:           "bump",
		`{"bump":"patch","reason":"","notes":"n"}`:           "reason is required",
		`{"bump":"patch","reason":"r","notes":" "}`:          "notes is required",
		`{"bump":"patch","reason":"r","notes":"n","x":1}`:    "unknown field",
		`{"bump":"patch","reason":"r","notes":"n"} trailing`: "trailing data",
		"Here are the notes: {}":                             "invalid character",
	} {
		if _, err := ParseReleaseNotes([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", raw, err, want)
		}
	}
}

func TestParseReleaseAnalysis(t *testing.T) {
	a, err := ParseReleaseAnalysis([]byte(`{"suspects":[{"issue":"gc-74","likelihood":"high","why":"review_count counts stars"}],"summary":"the ratings change"}`))
	if err != nil || len(a.Suspects) != 1 || a.Suspects[0].Issue != "gc-74" {
		t.Fatalf("ParseReleaseAnalysis = %+v, %v", a, err)
	}
	if a, err := ParseReleaseAnalysis([]byte(`{"suspects":[],"summary":"no issue explains it"}`)); err != nil || len(a.Suspects) != 0 {
		t.Errorf("no suspects = %+v, %v", a, err)
	}
	for raw, want := range map[string]string{
		`{"suspects":[{"issue":"","likelihood":"high","why":"w"}],"summary":"s"}`:     "needs the issue's key",
		`{"suspects":[{"issue":"gc-1","likelihood":"sure","why":"w"}],"summary":"s"}`: "likelihood",
		`{"suspects":[{"issue":"gc-1","likelihood":"low","why":""}],"summary":"s"}`:   "why is required",
		`{"suspects":[],"summary":""}`: "summary is required",
	} {
		if _, err := ParseReleaseAnalysis([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", raw, err, want)
		}
	}
}

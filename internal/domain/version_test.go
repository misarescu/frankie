package domain

import "testing"

func TestParseVersion(t *testing.T) {
	v, err := ParseVersion("v1.12.0")
	if err != nil || v != (Version{Major: 1, Minor: 12}) || v.String() != "v1.12.0" {
		t.Fatalf("ParseVersion = %v, %v", v, err)
	}
	for _, bad := range []string{"", "1.2.3", "v1.2", "v01.2.3", "v1.2.3-rc.1", "v1.2.3+build", "va.b.c", "v1.2.3 "} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) accepted", bad)
		}
	}
}

// M7 (D129): the first version is v0.1.0; below 1.0.0 a breaking change
// bumps the minor part, and no bump reaches 1.0.0 by itself.
func TestNextVersion(t *testing.T) {
	tests := []struct {
		prev    string
		hasPrev bool
		bump    Bump
		want    string
	}{
		{"", false, BumpMajor, "v0.1.0"},
		{"", false, BumpPatch, "v0.1.0"},
		{"v0.1.0", true, BumpPatch, "v0.1.1"},
		{"v0.1.3", true, BumpMinor, "v0.2.0"},
		{"v0.9.2", true, BumpMajor, "v0.10.0"},
		{"v1.4.2", true, BumpPatch, "v1.4.3"},
		{"v1.4.2", true, BumpMinor, "v1.5.0"},
		{"v1.4.2", true, BumpMajor, "v2.0.0"},
	}
	for _, tt := range tests {
		var prev Version
		if tt.hasPrev {
			var err error
			if prev, err = ParseVersion(tt.prev); err != nil {
				t.Fatal(err)
			}
		}
		if got := NextVersion(prev, tt.hasPrev, tt.bump).String(); got != tt.want {
			t.Errorf("NextVersion(%s, %s) = %s, want %s", tt.prev, tt.bump, got, tt.want)
		}
	}
}

func TestFallbackBump(t *testing.T) {
	for _, tt := range []struct {
		types []IssueType
		want  Bump
	}{
		{[]IssueType{IssueBug, IssueBug}, BumpPatch},
		{[]IssueType{IssueBug, IssueTask}, BumpMinor},
		{[]IssueType{IssueStory}, BumpMinor},
		{nil, BumpPatch},
	} {
		if got := FallbackBump(tt.types); got != tt.want {
			t.Errorf("FallbackBump(%v) = %s, want %s", tt.types, got, tt.want)
		}
	}
	if !BumpMinor.Valid() || Bump("huge").Valid() || !NotesByFallback.Valid() || NotesSource("").Valid() {
		t.Error("Valid misjudges a bump or a notes source")
	}
}

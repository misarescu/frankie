package e2e

import (
	"slices"
	"testing"
)

// M7, FR-TA-3, D116: tests are tagged with the issues whose keys their names carry.
func TestIssueKeys(t *testing.T) {
	tests := []struct {
		texts []string
		want  []string
	}{
		{[]string{"Books/gc-65 search by tag"}, []string{"gc-65"}},
		{[]string{"[GC-65] search", "@gc-12 and gc-65"}, []string{"gc-65", "gc-12"}},
		{[]string{"gc-650 not gc-65x", "abc-65", "gc-"}, []string{"gc-650"}},
		{[]string{"utf-8", "frk-3"}, nil},
		{[]string{"gc-7"}, []string{"gc-7"}},
	}
	for _, tt := range tests {
		if got := IssueKeys("gc", tt.texts...); !slices.Equal(got, tt.want) {
			t.Errorf("IssueKeys(%q) = %v, want %v", tt.texts, got, tt.want)
		}
	}
	if got := IssueKeys("", "gc-1"); got != nil {
		t.Errorf("no prefix = %v", got)
	}
}

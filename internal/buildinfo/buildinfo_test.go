package buildinfo

import "testing"

// D131: a build that stamps no commit reports none rather than a guess.
func TestCommitUnstamped(t *testing.T) {
	if got := Commit(); got != "" {
		t.Fatalf("Commit() = %q in a test build, want \"\"", got)
	}
}

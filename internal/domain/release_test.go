package domain

import "testing"

// M7, D110: the release and deployment state machine.
func TestCanTransitionRelease(t *testing.T) {
	tests := []struct {
		from, to ReleaseStatus
		want     bool
	}{
		{ReleasePending, ReleaseInProgress, true},
		{ReleasePending, ReleaseFailed, true},
		{ReleaseInProgress, ReleaseSucceeded, true},
		{ReleaseInProgress, ReleaseFailed, true},
		{ReleaseInProgress, ReleaseRolledBack, true},
		{ReleaseFailed, ReleaseRolledBack, true},
		{ReleasePending, ReleaseSucceeded, false},
		{ReleasePending, ReleaseRolledBack, false},
		{ReleaseInProgress, ReleasePending, false},
		{ReleaseSucceeded, ReleaseRolledBack, false},
		{ReleaseSucceeded, ReleaseFailed, false},
		{ReleaseFailed, ReleaseInProgress, false},
		{ReleaseRolledBack, ReleaseFailed, false},
		{ReleaseStatus("bogus"), ReleaseInProgress, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if got := CanTransitionRelease(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransitionRelease(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestReleaseStatusPredicates(t *testing.T) {
	tests := []struct {
		s                         ReleaseStatus
		valid, terminal, finished bool
	}{
		{ReleasePending, true, false, false},
		{ReleaseInProgress, true, false, false},
		{ReleaseSucceeded, true, true, true},
		{ReleaseFailed, true, false, true},
		{ReleaseRolledBack, true, true, true},
		{ReleaseStatus("bogus"), false, false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.s), func(t *testing.T) {
			if got := tt.s.Valid(); got != tt.valid {
				t.Errorf("Valid() = %v, want %v", got, tt.valid)
			}
			if got := tt.s.Terminal(); got != tt.terminal {
				t.Errorf("Terminal() = %v, want %v", got, tt.terminal)
			}
			if got := tt.s.Finished(); got != tt.finished {
				t.Errorf("Finished() = %v, want %v", got, tt.finished)
			}
		})
	}
}

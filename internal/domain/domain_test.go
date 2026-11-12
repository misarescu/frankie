package domain

import "testing"

// Cases follow the issue lifecycle in docs/spec/02-domain-model.md.
func TestCanTransitionIssue(t *testing.T) {
	tests := []struct {
		from, to IssueStatus
		want     bool
	}{
		// Happy path through the lifecycle.
		{IssueDraft, IssueRefining, true},
		{IssueRefining, IssueReady, true},
		{IssueReady, IssueRunning, true},
		{IssueRunning, IssueAwaitingHuman, true},
		{IssueAwaitingHuman, IssueRunning, true},
		{IssueRunning, IssueQueued, true},
		{IssueQueued, IssueMerged, true},
		{IssueMerged, IssueReleased, true},
		// Refinement is optional; approval can move a draft straight to ready.
		{IssueDraft, IssueReady, true},
		// The merge queue sends a conflicting change back to the coder.
		{IssueQueued, IssueRunning, true},
		// failed/canceled from any state before merge.
		{IssueDraft, IssueCanceled, true},
		{IssueAwaitingHuman, IssueFailed, true},
		{IssueQueued, IssueCanceled, true},
		// A failed issue can be re-armed for a new Run.
		{IssueFailed, IssueReady, true},

		// Spec approval cannot be skipped: no workflow before ready.
		{IssueDraft, IssueRunning, false},
		{IssueRefining, IssueRunning, false},
		// Nothing reaches main without the merge queue.
		{IssueRunning, IssueMerged, false},
		{IssueReady, IssueQueued, false},
		// Code on main cannot be failed or canceled.
		{IssueMerged, IssueFailed, false},
		{IssueMerged, IssueCanceled, false},
		// Terminal states.
		{IssueReleased, IssueReady, false},
		{IssueCanceled, IssueReady, false},
		{IssueFailed, IssueRunning, false},
		// Unknown and self transitions.
		{IssueReady, IssueReady, false},
		{IssueStatus("bogus"), IssueReady, false},
		{IssueReady, IssueStatus("bogus"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if got := CanTransitionIssue(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransitionIssue(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestIssueTransitionTableIsClosed(t *testing.T) {
	for from, targets := range issueTransitions {
		for _, to := range targets {
			if !to.Valid() {
				t.Errorf("%q -> %q: target is not a known status", from, to)
			}
		}
	}
}

func TestCanTransitionExec(t *testing.T) {
	tests := []struct {
		from, to ExecStatus
		want     bool
	}{
		{ExecPending, ExecRunning, true},
		{ExecPending, ExecCanceled, true},
		{ExecPending, ExecFailed, true},
		{ExecRunning, ExecSucceeded, true},
		{ExecRunning, ExecFailed, true},
		{ExecRunning, ExecCanceled, true},
		{ExecPending, ExecSucceeded, false},
		{ExecRunning, ExecPending, false},
		{ExecSucceeded, ExecRunning, false},
		{ExecFailed, ExecRunning, false},
		{ExecCanceled, ExecRunning, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if got := CanTransitionExec(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransitionExec(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestExecStatusTerminal(t *testing.T) {
	for s, want := range map[ExecStatus]bool{
		ExecPending: false, ExecRunning: false,
		ExecSucceeded: true, ExecFailed: true, ExecCanceled: true,
		ExecStatus("bogus"): false,
	} {
		if got := s.Terminal(); got != want {
			t.Errorf("%q.Terminal() = %v, want %v", s, got, want)
		}
	}
}

func TestNewIDIsTimeOrderedV7(t *testing.T) {
	a, b := NewID(), NewID()
	if a.Version() != 7 || b.Version() != 7 {
		t.Fatalf("versions = %d, %d; want 7", a.Version(), b.Version())
	}
	if a.String() >= b.String() {
		t.Errorf("IDs not increasing: %s then %s", a, b)
	}
}

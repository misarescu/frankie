package domain

import (
	"testing"
)

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

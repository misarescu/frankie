package domain

import (
	"errors"
	"testing"
)

func TestMergeMethodValidate(t *testing.T) {
	for _, m := range []MergeMethod{MergeSquash, MergeRebase, MergeCommit} {
		if err := m.Validate(); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}
	for _, m := range []MergeMethod{"", "fast-forward", "Squash"} {
		if err := m.Validate(); err == nil {
			t.Errorf("%q accepted", m)
		}
	}
}

func TestMergeRequestValidate(t *testing.T) {
	if err := (MergeRequest{HeadSHA: "abc", Method: MergeSquash}).Validate(); err != nil {
		t.Error(err)
	}
	if err := (MergeRequest{Method: MergeSquash}).Validate(); err == nil {
		t.Error("a merge without the checked head was accepted")
	}
	if err := (MergeRequest{HeadSHA: "abc"}).Validate(); err == nil {
		t.Error("a merge without a method was accepted")
	}
	if errors.Is(ErrHeadMoved, ErrNotMergeable) {
		t.Error("merge errors are not distinct")
	}
}

// M6 (D85): one failed check fails the commit, an unfinished one keeps it
// pending, and a commit without checks has none.
func TestCombineChecks(t *testing.T) {
	ok, wait, bad := Check{State: CheckSuccess}, Check{State: CheckPending}, Check{State: CheckFailure}
	tests := []struct {
		name   string
		checks []Check
		want   CheckState
	}{
		{"no checks", nil, CheckNone},
		{"all passed", []Check{ok, ok}, CheckSuccess},
		{"one running", []Check{ok, wait}, CheckPending},
		{"one failed while another runs", []Check{wait, bad, ok}, CheckFailure},
		{"unknown state counts as not done", []Check{ok, {State: "queued"}}, CheckPending},
	}
	for _, tt := range tests {
		if got := CombineChecks(tt.checks); got != tt.want {
			t.Errorf("%s: %s, want %s", tt.name, got, tt.want)
		}
	}
}

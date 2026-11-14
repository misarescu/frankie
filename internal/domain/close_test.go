package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestFinished(t *testing.T) {
	for s := range issueTransitions {
		want := s == IssueMerged || s == IssueReleased || s == IssueCanceled
		if s.Finished() != want {
			t.Errorf("%s.Finished() = %v", s, !want)
		}
	}
}

// D70: a parent is completed once every child is finished and at least one
// was not canceled; a leaf, a parent in another status, or one with
// unfinished children is refused, naming why.
func TestCheckComplete(t *testing.T) {
	is := func(key string, s IssueStatus) Issue { return Issue{Key: key, Status: s} }
	done := []Issue{is("gc-2", IssueMerged), is("gc-3", IssueCanceled), is("gc-4", IssueReleased)}
	for _, c := range []struct {
		name     string
		parent   Issue
		children []Issue
		want     string // empty: allowed
	}{
		{"draft parent, children finished", is("gc-1", IssueDraft), done, ""},
		{"ready parent", is("gc-1", IssueReady), done[:1], ""},
		{"a leaf", is("gc-1", IssueDraft), nil, "no sub-issues"},
		{"unfinished children", is("gc-1", IssueDraft), []Issue{is("gc-2", IssueMerged), is("gc-5", IssueAwaitingHuman), is("gc-6", IssueFailed)},
			"unfinished sub-issues: gc-5 (awaiting_human), gc-6 (failed)"},
		{"only canceled children", is("gc-1", IssueDraft), []Issue{is("gc-3", IssueCanceled)}, "cancel it instead"},
		{"already merged", is("gc-1", IssueMerged), done, "gc-1 is merged"},
		{"canceled", is("gc-1", IssueCanceled), done, "gc-1 is canceled"},
		{"running", is("gc-1", IssueRunning), done, "gc-1 is running"},
	} {
		err := CheckComplete(c.parent, c.children)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want allowed", c.name, err)
		case c.want != "" && (!errors.Is(err, ErrCannotClose) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want ErrCannotClose naming %q", c.name, err, c.want)
		}
	}
}

// D70: any unfinished issue can be canceled, with a reason.
func TestCheckCancel(t *testing.T) {
	for _, c := range []struct {
		status IssueStatus
		reason string
		want   string
	}{
		{IssueAwaitingHuman, "its change request was closed", ""},
		{IssueDraft, "out of scope", ""},
		{IssueFailed, "gave up", ""},
		{IssueRunning, "stop", ""}, // the store refuses while a run is active
		{IssueReady, "  ", "say why"},
		{IssueReady, strings.Repeat("x", maxCloseReason+1), "longer than"},
		{IssueMerged, "x", "already merged"},
		{IssueCanceled, "x", "already canceled"},
	} {
		err := CheckCancel(Issue{Key: "gc-1", Status: c.status}, c.reason)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want allowed", c.status, err)
		case c.want != "" && (!errors.Is(err, ErrCannotClose) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s %q: %v, want ErrCannotClose naming %q", c.status, c.reason[:min(len(c.reason), 10)], err, c.want)
		}
	}
}

// D70: canceling cancels the unfinished part of the subtree.
func TestToCancel(t *testing.T) {
	got := ToCancel([]Issue{{Key: "e", Status: IssueDraft}, {Key: "a", Status: IssueMerged}, {Key: "b", Status: IssueReady}, {Key: "c", Status: IssueCanceled}, {Key: "d", Status: IssueFailed}})
	var keys []string
	for _, is := range got {
		keys = append(keys, is.Key)
	}
	if strings.Join(keys, ",") != "e,b,d" {
		t.Errorf("ToCancel = %v, want e,b,d", keys)
	}
}

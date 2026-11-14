package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// walk moves an issue along the lifecycle, from draft.
func walk(t *testing.T, s *Store, is domain.Issue, path ...domain.IssueStatus) domain.Issue {
	t.Helper()
	from := domain.IssueDraft
	for _, to := range path {
		var err error
		if is, err = s.TransitionIssue(t.Context(), is.ID, from, to); err != nil {
			t.Fatalf("%s %s -> %s: %v", is.Key, from, to, err)
		}
		from = to
	}
	return is
}

var (
	toMerged   = []domain.IssueStatus{domain.IssueReady, domain.IssueRunning, domain.IssueAwaitingHuman, domain.IssueMerged}
	toAwaiting = []domain.IssueStatus{domain.IssueReady, domain.IssueRunning, domain.IssueAwaitingHuman}
)

// D70: a person completes a parent once everything under it is finished, and
// cancels an unfinished issue with a reason; both record who and when.
func TestCompleteAndCancel(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	story := mustTyped(t, s, p.ID, domain.IssueStory, &epic)
	task := walk(t, s, mustTyped(t, s, p.ID, domain.IssueTask, &story), toMerged...)
	bug := walk(t, s, mustTyped(t, s, p.ID, domain.IssueBug, &epic), toAwaiting...)

	if _, err := s.CompleteIssue(ctx, epic.ID, "ana"); !errors.Is(err, domain.ErrCannotClose) || !strings.Contains(err.Error(), story.Key) {
		t.Fatalf("completing the epic with open sub-issues = %v, want ErrCannotClose naming %s", err, story.Key)
	}
	if _, err := s.CompleteIssue(ctx, task.ID, "ana"); !errors.Is(err, domain.ErrCannotClose) {
		t.Fatalf("completing a leaf = %v, want ErrCannotClose", err)
	}
	got, err := s.CompleteIssue(ctx, story.ID, "ana")
	if err != nil || got.Status != domain.IssueMerged || got.ClosedBy != "ana" || got.ClosedAt == nil || got.CloseReason != "" {
		t.Fatalf("CompleteIssue(story) = %+v, %v", got, err)
	}

	// The bug's change request was closed without merging (like gc-1): cancel it.
	canceled, err := s.CancelIssue(ctx, bug.ID, "ana", "  its change request was closed  ")
	if err != nil || len(canceled) != 1 || canceled[0].Status != domain.IssueCanceled || canceled[0].CloseReason != "its change request was closed" {
		t.Fatalf("CancelIssue(bug) = %+v, %v", canceled, err)
	}
	if got, err := s.CompleteIssue(ctx, epic.ID, "bo"); err != nil || got.Status != domain.IssueMerged || got.ClosedBy != "bo" {
		t.Fatalf("CompleteIssue(epic) once all is finished = %+v, %v", got, err)
	}
	if _, err := s.CompleteIssue(ctx, epic.ID, "bo"); !errors.Is(err, domain.ErrCannotClose) {
		t.Fatalf("completing twice = %v, want ErrCannotClose", err)
	}
	if _, err := s.CancelIssue(ctx, epic.ID, "bo", "x"); !errors.Is(err, domain.ErrCannotClose) {
		t.Fatalf("canceling a merged issue = %v, want ErrCannotClose", err)
	}

	// Each close is in the event stream.
	var n int
	if err := s.Pool().QueryRow(ctx, `SELECT count(*) FROM events WHERE type = 'state_change' AND payload->>'entity' = 'issue'
		AND payload->>'id' = $1 AND payload->>'to' = 'merged'`, epic.ID.String()).Scan(&n); err != nil || n != 1 {
		t.Errorf("state change events for completing the epic = %d, %v; want 1", n, err)
	}
}

// D70: canceling a parent cancels everything unfinished under it; finished
// issues keep their status.
func TestCancelSubtree(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	story := mustTyped(t, s, p.ID, domain.IssueStory, &epic)
	ready := walk(t, s, mustTyped(t, s, p.ID, domain.IssueTask, &story), domain.IssueReady)
	merged := walk(t, s, mustTyped(t, s, p.ID, domain.IssueTask, &story), toMerged...)
	failed := walk(t, s, mustTyped(t, s, p.ID, domain.IssueTask, &epic), domain.IssueReady, domain.IssueFailed)
	other := mustTyped(t, s, p.ID, domain.IssueTask, nil)

	got, err := s.CancelIssue(ctx, epic.ID, "ana", "out of scope")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, is := range got {
		keys = append(keys, is.Key)
		if is.Status != domain.IssueCanceled || is.ClosedBy != "ana" || is.CloseReason != "out of scope" {
			t.Errorf("%s = %+v", is.Key, is)
		}
	}
	if want := []string{epic.Key, story.Key, failed.Key, ready.Key}; !slices.Equal(keys, want) {
		t.Errorf("canceled %v, want %v (root first, then by depth)", keys, want)
	}
	for _, c := range []struct {
		is   domain.Issue
		want domain.IssueStatus
	}{{merged, domain.IssueMerged}, {other, domain.IssueDraft}} {
		if is, _ := s.GetIssue(ctx, c.is.ID); is.Status != c.want || is.ClosedAt != nil {
			t.Errorf("%s = %s (closed %v), want untouched %s", is.Key, is.Status, is.ClosedAt, c.want)
		}
	}
}

// D70: nothing is canceled while a run under it is in progress, or without a reason.
func TestCancelRefusals(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	story := mustTyped(t, s, p.ID, domain.IssueStory, nil)
	busy := walk(t, s, mustTyped(t, s, p.ID, domain.IssueTask, &story), domain.IssueReady, domain.IssueRunning)
	run := mustRun(t, s, busy.ID)

	_, err := s.CancelIssue(ctx, story.ID, "ana", "stop")
	if !errors.Is(err, domain.ErrCannotClose) || !strings.Contains(err.Error(), busy.Key) || !strings.Contains(err.Error(), run.ID.String()) {
		t.Fatalf("cancel with a run in progress = %v, want ErrCannotClose naming %s and its run", err, busy.Key)
	}
	for _, id := range []domain.Issue{story, busy} {
		if is, _ := s.GetIssue(ctx, id.ID); is.Status == domain.IssueCanceled {
			t.Errorf("%s was canceled although the cancel was refused", is.Key)
		}
	}
	if _, err := s.CancelIssue(ctx, story.ID, "ana", " "); !errors.Is(err, domain.ErrCannotClose) {
		t.Errorf("cancel without a reason = %v, want ErrCannotClose", err)
	}
	if _, err := s.CancelIssue(ctx, story.ID, "", "x"); err == nil {
		t.Error("a cancel without a closer was accepted")
	}
}

// D71: what a person closed is closed at the source once; issues without a
// source, and leaves merged by their change request, are not listed.
func TestIssuesToCloseAtSource(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	mk := func(typ domain.IssueType, ref string, parent *domain.Issue) domain.Issue {
		t.Helper()
		in := NewIssue{ProjectID: p.ID, Type: typ, Title: "t", CreatedBy: "t", SourceRef: ref}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		is, err := s.CreateIssue(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return is
	}
	story := mk(domain.IssueStory, "https://github.com/o/r/issues/1", nil)
	walk(t, s, mk(domain.IssueTask, "https://github.com/o/r/issues/2", &story), toMerged...) // merged by its change request
	native := mk(domain.IssueTask, "", nil)
	dropped := mk(domain.IssueBug, "https://github.com/o/r/issues/3", nil)
	if _, err := s.CompleteIssue(ctx, story.ID, "ana"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.Issue{native, dropped} {
		if _, err := s.CancelIssue(ctx, id.ID, "ana", "no"); err != nil {
			t.Fatal(err)
		}
	}
	keys := func() []string {
		t.Helper()
		got, err := s.ListIssuesToCloseAtSource(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, is := range got {
			out = append(out, is.Key)
		}
		return out
	}
	if got := keys(); !slices.Equal(got, []string{story.Key, dropped.Key}) {
		t.Fatalf("to close = %v, want %s then %s", got, story.Key, dropped.Key)
	}
	if err := s.MarkSourceClosed(ctx, story.ID); err != nil {
		t.Fatal(err)
	}
	if got := keys(); !slices.Equal(got, []string{dropped.Key}) {
		t.Errorf("after marking %s: %v", story.Key, got)
	}
	if is, _ := s.GetIssue(ctx, story.ID); is.SourceClosedAt == nil {
		t.Error("SourceClosedAt not set")
	}
}

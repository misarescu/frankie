package intake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
)

// fakeWriter is a source whose issues are open unless listed; it records calls.
type fakeWriter struct {
	closed map[string]bool
	gone   map[string]bool
	fail   map[string]bool // UpdateStatus fails for these refs
	calls  []string
}

func (f *fakeWriter) Get(_ context.Context, ref string) (domain.ExternalIssue, error) {
	f.calls = append(f.calls, "get "+ref)
	if f.gone[ref] {
		return domain.ExternalIssue{}, fmt.Errorf("%w: %s", domain.ErrExternalIssueGone, ref)
	}
	return domain.ExternalIssue{Ref: ref, Open: !f.closed[ref]}, nil
}

func (f *fakeWriter) UpdateStatus(_ context.Context, ref string, s domain.IssueStatus) error {
	f.calls = append(f.calls, fmt.Sprintf("status %s %s", ref, s))
	if f.fail[ref] {
		return errors.New("403 resource not accessible")
	}
	f.closed[ref] = true
	return nil
}

func (f *fakeWriter) Comment(_ context.Context, ref, body string) error {
	f.calls = append(f.calls, fmt.Sprintf("comment %s %s", ref, body))
	return nil
}

// D71: what a person closed in Frankie is closed at the source with a comment
// naming who and why; already-closed and vanished source issues are left alone;
// a failure is retried on the next pass.
func TestCloserPoll(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, _ := s.CreateProject(ctx, "o/r", "or")
	ref := func(n int) string { return fmt.Sprintf("https://github.com/o/r/issues/%d", n) }
	mk := func(typ domain.IssueType, n int, parent *domain.Issue, status domain.IssueStatus) domain.Issue {
		t.Helper()
		in := store.NewIssue{ProjectID: p.ID, Type: typ, Title: "t", CreatedBy: "t", SourceRef: ref(n), Status: status}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		is, err := s.CreateIssue(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return is
	}
	epic := mk(domain.IssueEpic, 1, nil, domain.IssueDraft)
	task := mk(domain.IssueTask, 2, &epic, domain.IssueAwaitingHuman)
	if _, err := s.TransitionIssue(ctx, task.ID, domain.IssueAwaitingHuman, domain.IssueMerged); err != nil {
		t.Fatal(err)
	}
	stuck := mk(domain.IssueTask, 3, nil, domain.IssueAwaitingHuman)
	already := mk(domain.IssueTask, 4, nil, domain.IssueReady)
	gone := mk(domain.IssueBug, 5, nil, domain.IssueDraft)
	if _, err := s.CompleteIssue(ctx, epic.ID, "ana"); err != nil {
		t.Fatal(err)
	}
	for _, is := range []domain.Issue{stuck, already, gone} {
		if _, err := s.CancelIssue(ctx, is.ID, "bo", "its change request was closed"); err != nil {
			t.Fatal(err)
		}
	}

	src := &fakeWriter{closed: map[string]bool{ref(4): true}, gone: map[string]bool{ref(5): true}, fail: map[string]bool{ref(3): true}}
	c := &Closer{Store: s, Source: src, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err := c.Poll(ctx)
	if err == nil || !strings.Contains(err.Error(), stuck.Key) {
		t.Fatalf("Poll = %v, want the failure for %s", err, stuck.Key)
	}
	want := []string{
		"get " + ref(1),
		"comment " + ref(1) + " Completed in Frankie by ana (" + epic.Key + "): all its sub-issues are finished.",
		"status " + ref(1) + " merged",
		"get " + ref(3),
		"comment " + ref(3) + " Canceled in Frankie by bo (" + stuck.Key + "): its change request was closed",
		"status " + ref(3) + " canceled",
		"get " + ref(4), // closed already: no comment
		"get " + ref(5), // gone
	}
	if !slices.Equal(src.calls, want) {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(src.calls, "\n"), strings.Join(want, "\n"))
	}

	// The failed close is retried; the rest are done.
	src.calls, src.fail = nil, nil
	if err := c.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(src.calls) != 3 || src.calls[0] != "get "+ref(3) || !strings.HasPrefix(src.calls[2], "status "+ref(3)) {
		t.Errorf("retry calls = %v, want only %s again", src.calls, stuck.Key)
	}
	src.calls = nil
	if err := c.Poll(ctx); err != nil || len(src.calls) != 0 {
		t.Errorf("third pass = %v, %v; want nothing left to do", src.calls, err)
	}
}

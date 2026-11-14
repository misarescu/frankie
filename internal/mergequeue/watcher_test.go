package mergequeue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
)

// fakeSource answers by change request number; unknown numbers fail.
type fakeSource struct {
	status map[int]domain.ChangeRequestStatus
	asked  []int
}

func (f *fakeSource) ChangeRequestStatus(_ context.Context, _ string, n int) (domain.ChangeRequestStatus, error) {
	f.asked = append(f.asked, n)
	if st, ok := f.status[n]; ok {
		return st, nil
	}
	return "", errors.New("502 bad gateway")
}

func openCR(t *testing.T, s *store.Store, p domain.Project, status domain.IssueStatus, number int) (domain.Issue, domain.ChangeRequest) {
	t.Helper()
	is, err := s.CreateIssue(t.Context(), store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", Status: status, CreatedBy: "t"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(t.Context(), store.NewRun{IssueID: is.ID, WorkflowSnapshot: json.RawMessage(`{}`), WorkflowVersion: "1",
		TemporalWorkflowID: fmt.Sprintf("issue-%s", is.Key)})
	if err != nil {
		t.Fatal(err)
	}
	cr, err := s.UpsertChangeRequest(t.Context(), store.NewChangeRequest{IssueID: is.ID, RunID: run.ID, Repo: "o/r", Number: number,
		ProviderRef: fmt.Sprintf("https://github.com/o/r/pull/%d", number), Branch: "agent/" + is.Key + "/x", HeadSHA: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	return is, cr
}

// D61: a change request a person merged moves its issue to merged; one closed
// without merging is recorded and the issue waits on; an open one stays watched.
func TestWatcherPoll(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, err := s.CreateProject(ctx, "o/r", "or")
	if err != nil {
		t.Fatal(err)
	}
	merged, _ := openCR(t, s, p, domain.IssueAwaitingHuman, 1)
	closed, _ := openCR(t, s, p, domain.IssueAwaitingHuman, 2)
	open, _ := openCR(t, s, p, domain.IssueAwaitingHuman, 3)
	openCR(t, s, p, domain.IssueRunning, 4) // its run is still going: not watched
	src := &fakeSource{status: map[int]domain.ChangeRequestStatus{
		1: domain.ChangeRequestMerged, 2: domain.ChangeRequestClosed, 3: domain.ChangeRequestOpen, 4: domain.ChangeRequestMerged}}
	w := &Watcher{Store: s, Source: src, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if err := w.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		is   domain.Issue
		want domain.IssueStatus
	}{{merged, domain.IssueMerged}, {closed, domain.IssueAwaitingHuman}, {open, domain.IssueAwaitingHuman}} {
		got, err := s.GetIssue(ctx, tt.is.ID)
		if err != nil || got.Status != tt.want {
			t.Errorf("%s = %s, %v; want %s", tt.is.Key, got.Status, err, tt.want)
		}
	}
	if strings.Contains(fmt.Sprint(src.asked), "4") {
		t.Errorf("asked about %v; the running issue's change request is not watched", src.asked)
	}

	// Only the still-open change request is asked about again.
	src.asked = nil
	if err := w.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(src.asked) != "[3]" {
		t.Errorf("second poll asked about %v, want [3]", src.asked)
	}
}

// A provider error for one change request does not stop the others.
func TestWatcherPollReportsErrors(t *testing.T) {
	s := storetest.New(t)
	p, _ := s.CreateProject(t.Context(), "o/r", "or")
	openCR(t, s, p, domain.IssueAwaitingHuman, 9) // unknown to the source: fails
	merged, _ := openCR(t, s, p, domain.IssueAwaitingHuman, 1)
	w := &Watcher{Store: s, Source: &fakeSource{status: map[int]domain.ChangeRequestStatus{1: domain.ChangeRequestMerged}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := w.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), "o/r#9") {
		t.Fatalf("err = %v, want the failing change request named", err)
	}
	if got, _ := s.GetIssue(t.Context(), merged.ID); got.Status != domain.IssueMerged {
		t.Errorf("%s = %s, want merged despite the other error", merged.Key, got.Status)
	}
}

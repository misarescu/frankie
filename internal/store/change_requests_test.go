package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

func TestUpsertChangeRequestIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, "running")
	r1 := mustRun(t, s, is.ID)
	in := NewChangeRequest{IssueID: is.ID, RunID: r1.ID, Repo: "misarescu/toy", Number: 7,
		ProviderRef: "https://github.com/misarescu/toy/pull/7", Branch: "agent/p-1/x", HeadSHA: "aaa"}
	first, err := s.UpsertChangeRequest(t.Context(), in)
	if err != nil {
		t.Fatalf("UpsertChangeRequest: %v", err)
	}
	in.HeadSHA = "bbb"
	second, err := s.UpsertChangeRequest(t.Context(), in)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != first.ID || second.HeadSHA != "bbb" || second.Status != "open" {
		t.Errorf("second = %+v; want the same row with the new head", second)
	}
	list, err := s.ListChangeRequestsForIssue(t.Context(), is.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if _, err := s.UpsertChangeRequest(t.Context(), NewChangeRequest{IssueID: is.ID, RunID: r1.ID}); err == nil {
		t.Error("incomplete change request accepted")
	}
}

// mustChangeRequest opens change request number for an issue in status.
func mustChangeRequest(t *testing.T, s *Store, status domain.IssueStatus, number int) (domain.Issue, domain.Run, domain.ChangeRequest) {
	t.Helper()
	is := mustIssue(t, s, mustProject(t, s).ID, status)
	r := mustRun(t, s, is.ID)
	cr, err := s.UpsertChangeRequest(t.Context(), NewChangeRequest{IssueID: is.ID, RunID: r.ID, Repo: "misarescu/toy", Number: number,
		ProviderRef: "https://github.com/misarescu/toy/pull/1", Branch: "agent/p-1/x", HeadSHA: "aaa"})
	if err != nil {
		t.Fatal(err)
	}
	return is, r, cr
}

// D61: only open change requests of issues waiting for a person are watched.
func TestListChangeRequestsAwaitingMerge(t *testing.T) {
	s := newTestStore(t)
	_, _, waiting := mustChangeRequest(t, s, domain.IssueAwaitingHuman, 1)
	mustChangeRequest(t, s, domain.IssueRunning, 2)
	_, _, closed := mustChangeRequest(t, s, domain.IssueAwaitingHuman, 3)
	if err := s.RecordChangeRequestClosed(t.Context(), closed.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListChangeRequestsAwaitingMerge(t.Context())
	if err != nil || len(got) != 1 || got[0].ID != waiting.ID {
		t.Fatalf("awaiting merge = %+v, %v; want only %v", got, err, waiting.ID)
	}
}

func TestRecordChangeRequestMerged(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	is, run, cr := mustChangeRequest(t, s, domain.IssueAwaitingHuman, 1)

	got, err := s.RecordChangeRequestMerged(ctx, cr.ID)
	if err != nil || got.Status != domain.IssueMerged {
		t.Fatalf("RecordChangeRequestMerged = %s, %v; want merged", got.Status, err)
	}
	crs, _ := s.ListChangeRequestsForIssue(ctx, is.ID)
	if len(crs) != 1 || crs[0].Status != string(domain.ChangeRequestMerged) {
		t.Fatalf("change request = %+v, want merged", crs)
	}
	// The run's transcript shows the merge.
	events, err := s.ListRunEvents(ctx, run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var change domain.StateChange
	for _, e := range events {
		if e.Type == domain.EventStateChange {
			_ = json.Unmarshal(e.Payload, &change)
		}
	}
	if change.Entity != "change_request" || change.To != "merged" {
		t.Errorf("last run state change = %+v, want the change request merged", change)
	}

	again, err := s.RecordChangeRequestMerged(ctx, cr.ID)
	if err != nil || again.Status != domain.IssueMerged {
		t.Fatalf("second record = %s, %v; want a no-op", again.Status, err)
	}
	if err := s.RecordChangeRequestClosed(ctx, cr.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("closing a merged change request = %v, want ErrConflict", err)
	}
}

// An issue that moved on keeps its status; the change request is still merged.
func TestRecordChangeRequestMergedLeavesBusyIssue(t *testing.T) {
	s := newTestStore(t)
	is, _, cr := mustChangeRequest(t, s, domain.IssueRunning, 1)
	got, err := s.RecordChangeRequestMerged(t.Context(), cr.ID)
	if err != nil || got.ID != is.ID || got.Status != domain.IssueRunning {
		t.Fatalf("RecordChangeRequestMerged = %s, %v; want the issue still running", got.Status, err)
	}
	crs, _ := s.ListChangeRequestsForIssue(t.Context(), is.ID)
	if crs[0].Status != string(domain.ChangeRequestMerged) {
		t.Errorf("change request status = %s, want merged", crs[0].Status)
	}
}

func TestRecordChangeRequestClosed(t *testing.T) {
	s := newTestStore(t)
	is, _, cr := mustChangeRequest(t, s, domain.IssueAwaitingHuman, 1)
	for range 2 { // the repeat is a no-op
		if err := s.RecordChangeRequestClosed(t.Context(), cr.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GetIssue(t.Context(), is.ID)
	if got.Status != domain.IssueAwaitingHuman {
		t.Errorf("issue = %s, want awaiting_human kept (open question 11)", got.Status)
	}
}

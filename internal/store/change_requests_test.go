package store

import (
	"testing"
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

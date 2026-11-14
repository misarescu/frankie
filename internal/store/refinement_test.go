package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

var refined = domain.RefinedSpec{Spec: "Add a search box.", AcceptanceCriteria: []string{"typing filters the list"},
	FileHints: []string{"web/src/Search.tsx"}, Summary: "assumed client-side filtering"}

// refiningIssue is an issue the refiner is working on, with its running run.
func refiningIssue(t *testing.T, s *Store) (domain.Issue, domain.Run) {
	t.Helper()
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRefining)
	r := mustRun(t, s, is.ID)
	if _, err := s.TransitionRun(t.Context(), r.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	return is, r
}

// D66: the refiner's result waits on the issue; approving makes it the spec.
func TestRecordRefinementThenApprove(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	is, r := refiningIssue(t, s)

	if _, err := s.ApproveSpec(ctx, is.ID, "ana"); !errors.Is(err, ErrConflict) {
		t.Fatalf("approving before the refiner returned = %v, want ErrConflict", err)
	}
	for range 2 { // a retried Activity records it again
		if err := s.RecordRefinement(ctx, is.ID, r.ID, refined); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GetIssue(ctx, is.ID)
	if got.Status != domain.IssueRefining || got.SpecProposal == nil || got.SpecProposal.Summary != refined.Summary {
		t.Fatalf("issue = %s, proposal %+v", got.Status, got.SpecProposal)
	}
	if run, _ := s.GetRun(ctx, r.ID); run.Status != domain.ExecSucceeded {
		t.Errorf("run = %s, want succeeded", run.Status)
	}

	got, err := s.ApproveSpec(ctx, is.ID, "ana")
	if err != nil || got.Status != domain.IssueReady || got.Spec != refined.Spec ||
		!slices.Equal(got.AcceptanceCriteria, refined.AcceptanceCriteria) || !slices.Equal(got.FileHints, refined.FileHints) ||
		got.SpecProposal != nil || got.SpecApprovedBy != "ana" {
		t.Fatalf("approved = %+v, %v", got, err)
	}
}

// Rejecting the refiner's result returns the issue to draft, held with the feedback.
func TestRejectRefinement(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	is, r := refiningIssue(t, s)
	if err := s.RecordRefinement(ctx, is.ID, r.ID, refined); err != nil {
		t.Fatal(err)
	}
	got, err := s.RejectSpec(ctx, is.ID, "server-side search")
	if err != nil || got.Status != domain.IssueDraft || got.SpecProposal != nil || got.SpecRejectedAt == nil ||
		got.SpecFeedback != "server-side search" || got.Spec == refined.Spec {
		t.Fatalf("rejected = %+v, %v", got, err)
	}
}

// A refinement without a usable spec fails its run and holds the draft with the reason.
func TestFailRefinement(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	is, r := refiningIssue(t, s)
	for range 2 { // idempotent
		if err := s.FailRefinement(ctx, is.ID, r.ID, "not a refined spec"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GetIssue(ctx, is.ID)
	if got.Status != domain.IssueDraft || got.SpecRejectedAt == nil || !strings.Contains(got.SpecFeedback, "not a refined spec") {
		t.Fatalf("issue = %+v", got)
	}
	if run, _ := s.GetRun(ctx, r.ID); run.Status != domain.ExecFailed {
		t.Errorf("run = %s, want failed", run.Status)
	}
	if err := s.RecordRefinement(ctx, is.ID, r.ID, refined); !errors.Is(err, ErrConflict) {
		t.Errorf("recording on a draft = %v, want ErrConflict", err)
	}
}

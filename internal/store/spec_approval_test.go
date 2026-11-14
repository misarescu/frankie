package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// D64: approving a draft's spec makes it ready and records who approved it.
func TestApproveSpec(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	is := mustIssue(t, s, p.ID, domain.IssueDraft)
	if _, err := s.RejectSpec(ctx, is.ID, "add acceptance criteria"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ApproveSpec(ctx, is.ID, "ana")
	if err != nil || got.Status != domain.IssueReady || got.SpecApprovedBy != "ana" || got.SpecApprovedAt == nil ||
		got.SpecRejectedAt != nil || got.SpecFeedback != "" {
		t.Fatalf("ApproveSpec = %+v, %v", got, err)
	}
	if _, err := s.ApproveSpec(ctx, is.ID, "ana"); !errors.Is(err, ErrConflict) {
		t.Fatalf("approving a ready issue = %v, want ErrConflict", err)
	}
	if _, err := s.ApproveSpec(ctx, is.ID, " "); err == nil {
		t.Fatal("an approval without an approver was accepted")
	}
}

// A rejection holds the draft until someone asks to start it again.
func TestRejectSpec(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	is := mustIssue(t, s, p.ID, domain.IssueDraft)
	got, err := s.RejectSpec(ctx, is.ID, "which endpoint?")
	if err != nil || got.Status != domain.IssueDraft || got.SpecRejectedAt == nil || got.SpecFeedback != "which endpoint?" {
		t.Fatalf("RejectSpec = %+v, %v", got, err)
	}
	state := func() domain.SchedIssue {
		issues, _, err := s.SchedulingState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range issues {
			if x.ID == is.ID {
				return x
			}
		}
		t.Fatal("issue missing from the scheduling state")
		return domain.SchedIssue{}
	}
	if !state().SpecRejected {
		t.Error("the scheduler does not see the rejection")
	}
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{is.ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	if state().SpecRejected {
		t.Error("asking to start again did not lift the rejection")
	}
	if _, err := s.RejectSpec(ctx, is.ID, ""); err == nil {
		t.Error("a rejection without feedback was accepted")
	}
	ready := mustIssue(t, s, p.ID, domain.IssueReady)
	if _, err := s.RejectSpec(ctx, ready.ID, "no"); !errors.Is(err, ErrConflict) {
		t.Errorf("rejecting a ready issue = %v, want ErrConflict", err)
	}
}

// The project setting and the labels decide whether an issue needs approval.
func TestNeedsApproval(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	mk := func(labels ...string) uuid.UUID {
		is, err := s.CreateIssue(ctx, NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", CreatedBy: "t", Labels: labels})
		if err != nil {
			t.Fatal(err)
		}
		return is.ID
	}
	plain, needs, skip := mk(), mk("needs-spec-approval"), mk("no-spec-approval")
	needsApproval := func() map[uuid.UUID]bool {
		issues, _, err := s.SchedulingState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]bool{}
		for _, x := range issues {
			out[x.ID] = x.NeedsApproval
		}
		return out
	}
	if got := needsApproval(); got[plain] || !got[needs] || got[skip] {
		t.Errorf("project off: plain %v, needs %v, skip %v", got[plain], got[needs], got[skip])
	}
	if err := s.SetProjectSpecApproval(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := needsApproval(); !got[plain] || !got[needs] || got[skip] {
		t.Errorf("project required: plain %v, needs %v, skip %v", got[plain], got[needs], got[skip])
	}
	if got, _ := s.GetProject(ctx, p.ID); !got.SpecApproval {
		t.Error("project setting not stored")
	}
}

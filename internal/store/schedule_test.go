package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

func TestRequestIssueStart(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	a := mustIssue(t, s, p.ID, domain.IssueReady)
	b := mustIssue(t, s, p.ID, domain.IssueDraft)

	got, err := s.RequestIssueStart(ctx, []uuid.UUID{a.ID, b.ID}, "ana")
	if err != nil || len(got) != 2 || got[0].StartRequestedAt == nil || got[0].StartRequestedBy != "ana" {
		t.Fatalf("RequestIssueStart = %+v, %v", got, err)
	}
	first := *got[0].StartRequestedAt
	again, err := s.RequestIssueStart(ctx, []uuid.UUID{a.ID}, "bo")
	if err != nil || !again[0].StartRequestedAt.Equal(first) || again[0].StartRequestedBy != "ana" {
		t.Fatalf("second request = %+v, %v; want the first request kept", again[0], err)
	}

	canceled := mustIssue(t, s, p.ID, domain.IssueReady)
	if _, err := s.TransitionIssue(ctx, canceled.ID, domain.IssueReady, domain.IssueCanceled); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{a.ID, canceled.ID}, "ana"); !errors.Is(err, ErrConflict) {
		t.Fatalf("starting a canceled issue = %v, want ErrConflict", err)
	}
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{uuid.New()}, "ana"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("starting an unknown issue = %v, want ErrNotFound", err)
	}
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{a.ID}, ""); err == nil {
		t.Fatal("a request without a requester was accepted")
	}
}

// D62: the scheduler sees requests, parents, dependencies and busy slots.
func TestSchedulingState(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ctx := t.Context()
	epic := mustTyped(t, s, p.ID, domain.IssueEpic, nil)
	a := mustTyped(t, s, p.ID, domain.IssueTask, &epic)
	b := mustTyped(t, s, p.ID, domain.IssueTask, &epic)
	running := mustIssue(t, s, p.ID, domain.IssueRunning)
	scheduled := mustIssue(t, s, p.ID, domain.IssueReady)
	idle := mustIssue(t, s, p.ID, domain.IssueReady) // ready, never scheduled
	if err := s.AddIssueDependency(ctx, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{epic.ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	mustRun(t, s, running.ID) // pending: an active run
	if err := s.MarkIssueScheduled(ctx, scheduled.ID); err != nil {
		t.Fatal(err)
	}

	issues, deps, err := s.SchedulingState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[uuid.UUID]domain.SchedIssue{}
	for _, is := range issues {
		by[is.ID] = is
	}
	if !by[epic.ID].Requested || by[a.ID].Requested {
		t.Errorf("requested: epic %v, a %v; want only the epic itself", by[epic.ID].Requested, by[a.ID].Requested)
	}
	if by[a.ID].ParentID == nil || *by[a.ID].ParentID != epic.ID || by[a.ID].Type != domain.IssueTask || by[a.ID].Key != a.Key {
		t.Errorf("a = %+v", by[a.ID])
	}
	if !by[running.ID].Busy || !by[scheduled.ID].Busy || by[a.ID].Busy || by[idle.ID].Busy {
		t.Errorf("busy: running %v, scheduled %v, a %v, idle %v", by[running.ID].Busy, by[scheduled.ID].Busy, by[a.ID].Busy, by[idle.ID].Busy)
	}
	if len(deps) != 1 || deps[0] != (domain.Dependency{IssueID: b.ID, DependsOnID: a.ID}) {
		t.Errorf("deps = %v", deps)
	}
}

func TestRetryIssue(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	failed := mustIssue(t, s, p.ID, domain.IssueReady)
	if _, err := s.TransitionIssue(t.Context(), failed.ID, domain.IssueReady, domain.IssueFailed); err != nil {
		t.Fatal(err)
	}
	got, err := s.RetryIssue(t.Context(), failed.ID, "ana")
	if err != nil || got.Status != domain.IssueReady || got.StartRequestedAt == nil {
		t.Fatalf("RetryIssue = %+v, %v; want ready and requested", got, err)
	}
	if _, err := s.RetryIssue(t.Context(), mustIssue(t, s, p.ID, domain.IssueDraft).ID, "ana"); !errors.Is(err, domain.ErrInvalidTransition) && !errors.Is(err, ErrConflict) {
		t.Fatalf("retrying a draft = %v, want a refused transition", err)
	}
}

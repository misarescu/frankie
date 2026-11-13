package store

import (
	"errors"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

func TestApprovalLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning)
	run := mustRun(t, s, is.ID)
	step, err := s.CreateStepRun(ctx, NewStepRun{RunID: run.ID, StepID: "approve", Attempt: 1, Kind: domain.StepGate})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	in := NewApproval{StepRunID: step.ID, Reason: "protected paths changed: deploy/helm/x", CommitSHA: "abc",
		ChangeRequestURL: "https://github.com/o/r/pull/1", ExpiresAt: expires}

	a, err := s.OpenApproval(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.OpenApproval(ctx, in)
	if err != nil || again.ID != a.ID || a.Status != domain.ApprovalPending || !a.ExpiresAt.Equal(expires) || a.ChangeRequestURL == "" {
		t.Fatalf("open = %+v, reopen = %+v, %v", a, again, err)
	}

	pending, err := s.ListPendingApprovals(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	p := pending[0]
	if p.StepID != "approve" || p.IssueKey != is.Key || p.TemporalWorkflowID != run.TemporalWorkflowID || p.Reason != in.Reason {
		t.Errorf("pending approval = %+v", p)
	}
	if got, err := s.GetPendingApproval(ctx, step.ID); err != nil || got.ID != a.ID {
		t.Errorf("GetPendingApproval = %+v, %v", got, err)
	}

	later := expires.Add(time.Hour)
	if ext, err := s.ExtendApproval(ctx, step.ID, later); err != nil || !ext.ExpiresAt.Equal(later) {
		t.Errorf("extend = %+v, %v", ext, err)
	}

	d, err := s.DecideApproval(ctx, step.ID, domain.ApprovalApproved, "web", "looks right")
	if err != nil || d.Status != domain.ApprovalApproved || d.DecidedBy != "web" || d.Comment != "looks right" || d.DecidedAt == nil {
		t.Fatalf("decide = %+v, %v", d, err)
	}
	// The same decision again (a retried Activity) is fine; another one is not.
	if _, err := s.DecideApproval(ctx, step.ID, domain.ApprovalApproved, "web", "looks right"); err != nil {
		t.Errorf("repeated decision: %v", err)
	}
	if _, err := s.DecideApproval(ctx, step.ID, domain.ApprovalRejected, "web", ""); !errors.Is(err, ErrConflict) {
		t.Errorf("second decision: err = %v, want ErrConflict", err)
	}
	if _, err := s.GetPendingApproval(ctx, step.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("decided approval still pending: %v", err)
	}
	if left, _ := s.ListPendingApprovals(ctx); len(left) != 0 {
		t.Errorf("inbox after the decision = %+v", left)
	}

	// The run's transcript shows the gate opening and closing.
	events, _ := s.ListRunEvents(ctx, run.ID, 0, 100)
	var changes []string
	for _, e := range events {
		if e.Type == domain.EventStateChange && e.StepRunID != nil && *e.StepRunID == step.ID {
			changes = append(changes, string(e.Payload))
		}
	}
	if len(changes) != 2 {
		t.Errorf("approval state changes = %v", changes)
	}
}

func TestOpenApprovalValidates(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.OpenApproval(t.Context(), NewApproval{}); err == nil {
		t.Fatal("empty approval accepted")
	}
	if _, err := s.DecideApproval(t.Context(), domain.NewID(), domain.ApprovalPending, "", ""); err == nil {
		t.Fatal("pending is not a decision")
	}
}

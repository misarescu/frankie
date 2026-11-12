package store

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

func mustProject(t *testing.T, s *Store) domain.Project {
	t.Helper()
	p, err := s.CreateProject(t.Context(), "proj-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return p
}

func mustIssue(t *testing.T, s *Store, projectID uuid.UUID, status domain.IssueStatus) domain.Issue {
	t.Helper()
	is, err := s.CreateIssue(t.Context(), NewIssue{
		ProjectID: projectID, Type: domain.IssueTask, Title: "do the thing",
		Status: status, CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return is
}

func mustRun(t *testing.T, s *Store, issueID uuid.UUID) domain.Run {
	t.Helper()
	r, err := s.CreateRun(t.Context(), NewRun{
		IssueID:            issueID,
		WorkflowSnapshot:   json.RawMessage(`{"name":"default-build","steps":[]}`),
		WorkflowVersion:    "1",
		TemporalWorkflowID: "wf-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return r
}

// Migrations run at control plane startup behind an advisory lock (docs/spec/02, Postgres conventions):
// concurrent and repeated calls must all succeed.
func TestMigrateConcurrentAndRepeated(t *testing.T) {
	pool := newTestPool(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = Migrate(t.Context(), pool) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent Migrate #%d: %v", i, err)
		}
	}
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("repeated Migrate: %v", err)
	}
	if _, err := New(pool).CreateProject(t.Context(), "after-migrate"); err != nil {
		t.Fatalf("schema not usable after migrate: %v", err)
	}
}

func TestCreateIssueDefaultsAndValidation(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)

	is := mustIssue(t, s, p.ID, "")
	if is.Status != domain.IssueDraft {
		t.Errorf("default status = %q, want draft", is.Status)
	}
	if is.ID.Version() != 7 {
		t.Errorf("issue ID version = %d, want 7", is.ID.Version())
	}
	got, err := s.GetIssue(t.Context(), is.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != is.Title || got.AcceptanceCriteria == nil || got.Labels == nil {
		t.Errorf("round trip mismatch: %+v", got)
	}

	bad := []NewIssue{
		{ProjectID: p.ID, Type: "chore", Title: "x", CreatedBy: "test"},
		{ProjectID: p.ID, Type: domain.IssueBug, Title: "", CreatedBy: "test"},
		{ProjectID: p.ID, Type: domain.IssueBug, Title: "x", CreatedBy: ""},
		{ProjectID: p.ID, Type: domain.IssueBug, Title: "x", CreatedBy: "test", Status: "bogus"},
	}
	for _, in := range bad {
		if _, err := s.CreateIssue(t.Context(), in); err == nil {
			t.Errorf("CreateIssue(%+v) succeeded, want validation error", in)
		}
	}
}

// An external issue syncs into at most one Issue per project.
func TestIssueSourceRefUniquePerProject(t *testing.T) {
	s := newTestStore(t)
	p1, p2 := mustProject(t, s), mustProject(t, s)
	in := NewIssue{ProjectID: p1.ID, Type: domain.IssueTask, Title: "t", CreatedBy: "test",
		SourceRef: "https://github.com/acme/app/issues/1"}

	first, err := s.CreateIssue(t.Context(), in)
	if err != nil {
		t.Fatalf("first CreateIssue: %v", err)
	}
	if _, err := s.CreateIssue(t.Context(), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate source ref: err = %v, want ErrConflict", err)
	}
	in.ProjectID = p2.ID
	if _, err := s.CreateIssue(t.Context(), in); err != nil {
		t.Fatalf("same source ref in another project: %v", err)
	}
	got, err := s.GetIssueBySourceRef(t.Context(), p1.ID, in.SourceRef)
	if err != nil || got.ID != first.ID {
		t.Fatalf("GetIssueBySourceRef = %v, %v; want %v", got.ID, err, first.ID)
	}
}

func TestTransitionIssue(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	is := mustIssue(t, s, p.ID, domain.IssueReady)
	ctx := t.Context()

	got, err := s.TransitionIssue(ctx, is.ID, domain.IssueReady, domain.IssueRunning)
	if err != nil || got.Status != domain.IssueRunning {
		t.Fatalf("ready -> running = %v, %v", got.Status, err)
	}

	t.Run("retry of an applied transition is a no-op", func(t *testing.T) {
		got, err := s.TransitionIssue(ctx, is.ID, domain.IssueReady, domain.IssueRunning)
		if err != nil || got.Status != domain.IssueRunning {
			t.Fatalf("repeat = %v, %v; want running, nil", got.Status, err)
		}
	})
	t.Run("stale from status conflicts", func(t *testing.T) {
		_, err := s.TransitionIssue(ctx, is.ID, domain.IssueReady, domain.IssueCanceled)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
	t.Run("state machine is enforced before touching the database", func(t *testing.T) {
		_, err := s.TransitionIssue(ctx, is.ID, domain.IssueRunning, domain.IssueMerged)
		if !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("err = %v, want ErrInvalidTransition", err)
		}
		cur, _ := s.GetIssue(ctx, is.ID)
		if cur.Status != domain.IssueRunning {
			t.Fatalf("status changed to %q", cur.Status)
		}
	})
	t.Run("unknown issue", func(t *testing.T) {
		_, err := s.TransitionIssue(ctx, domain.NewID(), domain.IssueReady, domain.IssueRunning)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// Exactly one of many concurrent movers wins a compare-and-set transition.
func TestTransitionIssueConcurrentSingleWinner(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueReady)

	targets := []domain.IssueStatus{domain.IssueRunning, domain.IssueCanceled, domain.IssueFailed}
	var wg sync.WaitGroup
	errs := make([]error, len(targets))
	for i, to := range targets {
		wg.Go(func() { _, errs[i] = s.TransitionIssue(t.Context(), is.ID, domain.IssueReady, to) })
	}
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrConflict):
			t.Errorf("mover %s: unexpected error %v", targets[i], err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d movers won, want exactly 1 (errs: %v)", wins, errs)
	}
}

// A retry/rerun creates a new Run; an issue never has two active Runs.
func TestOneActiveRunPerIssue(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning)
	ctx := t.Context()

	first := mustRun(t, s, is.ID)
	if first.Status != domain.ExecPending || len(first.LoopCounts) != 0 {
		t.Fatalf("new run = %+v", first)
	}
	_, err := s.CreateRun(ctx, NewRun{IssueID: is.ID, WorkflowSnapshot: json.RawMessage(`{}`),
		WorkflowVersion: "1", TemporalWorkflowID: "wf-" + uuid.NewString()})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second active run: err = %v, want ErrConflict", err)
	}
	active, err := s.GetActiveRunForIssue(ctx, is.ID)
	if err != nil || active.ID != first.ID {
		t.Fatalf("GetActiveRunForIssue = %v, %v", active.ID, err)
	}

	if _, err := s.TransitionRun(ctx, first.ID, domain.ExecPending, domain.ExecFailed); err != nil {
		t.Fatalf("fail first run: %v", err)
	}
	if _, err := s.GetActiveRunForIssue(ctx, is.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active run after failure: err = %v, want ErrNotFound", err)
	}
	second := mustRun(t, s, is.ID)
	if second.ID == first.ID {
		t.Fatal("retry reused the Run")
	}
}

func TestCreateRunRejectsInvalidSnapshot(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning)
	_, err := s.CreateRun(t.Context(), NewRun{IssueID: is.ID, WorkflowSnapshot: json.RawMessage(`{not json`),
		WorkflowVersion: "1", TemporalWorkflowID: "wf"})
	if err == nil {
		t.Fatal("CreateRun accepted an invalid snapshot")
	}
}

func TestRunTimestamps(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning)
	r := mustRun(t, s, is.ID)
	ctx := t.Context()

	if r.StartedAt != nil || r.FinishedAt != nil {
		t.Fatalf("pending run has timestamps: %+v", r)
	}
	r, err := s.TransitionRun(ctx, r.ID, domain.ExecPending, domain.ExecRunning)
	if err != nil || r.StartedAt == nil || r.FinishedAt != nil {
		t.Fatalf("running: %+v, %v", r, err)
	}
	r, err = s.TransitionRun(ctx, r.ID, domain.ExecRunning, domain.ExecSucceeded)
	if err != nil || r.FinishedAt == nil || r.FinishedAt.Before(*r.StartedAt) {
		t.Fatalf("succeeded: %+v, %v", r, err)
	}
}

// FR-WF-7: every step transition updates StepRun and emits an Event.
// FR-OB-2: a run's stream replays in order and can be resumed from a cursor.
func TestStepTransitionsEmitOrderedReplayableEvents(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning)
	r := mustRun(t, s, is.ID)
	ctx := t.Context()

	sr, err := s.CreateStepRun(ctx, NewStepRun{RunID: r.ID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatalf("CreateStepRun: %v", err)
	}
	if _, err := s.CreateStepRun(ctx, NewStepRun{RunID: r.ID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate attempt: err = %v, want ErrConflict", err)
	}
	if _, err := s.TransitionStepRun(ctx, sr.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatalf("step -> running: %v", err)
	}
	if _, err := s.AppendEvent(ctx, NewEvent{RunID: &r.ID, StepRunID: &sr.ID, Type: domain.EventAssistantMessage,
		Payload: json.RawMessage(`{"text":"hello"}`)}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if _, err := s.SetStepRunCommit(ctx, sr.ID, "abc123"); err != nil {
		t.Fatalf("SetStepRunCommit: %v", err)
	}
	done, err := s.TransitionStepRun(ctx, sr.ID, domain.ExecRunning, domain.ExecSucceeded)
	if err != nil || done.CommitSHA != "abc123" || done.FinishedAt == nil {
		t.Fatalf("step -> succeeded: %+v, %v", done, err)
	}
	// A retried Activity repeating the last transition must not duplicate events.
	if _, err := s.TransitionStepRun(ctx, sr.ID, domain.ExecRunning, domain.ExecSucceeded); err != nil {
		t.Fatalf("repeat transition: %v", err)
	}

	all, err := s.ListRunEvents(ctx, r.ID, 0, 100)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	wantTypes := []domain.EventType{domain.EventStateChange, domain.EventAssistantMessage, domain.EventStateChange}
	if len(all) != len(wantTypes) {
		t.Fatalf("got %d events, want %d: %+v", len(all), len(wantTypes), all)
	}
	for i, e := range all {
		if e.Type != wantTypes[i] {
			t.Errorf("event %d type = %s, want %s", i, e.Type, wantTypes[i])
		}
		if i > 0 && e.Seq <= all[i-1].Seq {
			t.Errorf("event %d seq %d not after %d", i, e.Seq, all[i-1].Seq)
		}
		if e.StepRunID == nil || *e.StepRunID != sr.ID {
			t.Errorf("event %d not linked to the step run", i)
		}
	}
	var change domain.StateChange
	if err := json.Unmarshal(all[2].Payload, &change); err != nil {
		t.Fatalf("decode state change: %v", err)
	}
	if change != (domain.StateChange{Entity: "step_run", ID: sr.ID, From: "running", To: "succeeded"}) {
		t.Errorf("state change = %+v", change)
	}

	resumed, err := s.ListRunEvents(ctx, r.ID, all[0].Seq, 1)
	if err != nil || len(resumed) != 1 || resumed[0].ID != all[1].ID {
		t.Fatalf("resume after seq %d = %+v, %v; want event %v", all[0].Seq, resumed, err, all[1].ID)
	}
}

func TestStoreValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	r := mustRun(t, s, mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning).ID)

	if _, err := s.CreateStepRun(ctx, NewStepRun{RunID: r.ID, StepID: "x", Attempt: 0, Kind: domain.StepGate}); err == nil {
		t.Error("CreateStepRun accepted attempt 0")
	}
	if _, err := s.CreateStepRun(ctx, NewStepRun{RunID: r.ID, StepID: "x", Attempt: 1, Kind: "deploy"}); err == nil {
		t.Error("CreateStepRun accepted unknown kind")
	}
	if _, err := s.AppendEvent(ctx, NewEvent{RunID: &r.ID, Type: domain.EventToolCall, Payload: json.RawMessage(`nope`)}); err == nil {
		t.Error("AppendEvent accepted invalid JSON")
	}
	if _, err := s.ListRunEvents(ctx, r.ID, 0, 0); err == nil {
		t.Error("ListRunEvents accepted limit 0")
	}
	if _, err := s.GetRun(ctx, domain.NewID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRun(unknown) err = %v, want ErrNotFound", err)
	}
}

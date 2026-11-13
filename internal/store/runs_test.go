package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

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

func TestRunValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	r := mustRun(t, s, mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning).ID)

	if _, err := s.CreateStepRun(ctx, NewStepRun{RunID: r.ID, StepID: "x", Attempt: 0, Kind: domain.StepGate}); err == nil {
		t.Error("CreateStepRun accepted attempt 0")
	}
	if _, err := s.CreateStepRun(ctx, NewStepRun{RunID: r.ID, StepID: "x", Attempt: 1, Kind: "deploy"}); err == nil {
		t.Error("CreateStepRun accepted unknown kind")
	}
	if _, err := s.CreateStepRun(ctx, NewStepRun{RunID: r.ID, StepID: "", Attempt: 1, Kind: domain.StepAgent}); err == nil {
		t.Error("CreateStepRun accepted an empty step id")
	}
	if _, err := s.GetRun(ctx, domain.NewID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRun(unknown) err = %v, want ErrNotFound", err)
	}
	if _, err := s.TransitionRun(ctx, r.ID, domain.ExecSucceeded, domain.ExecRunning); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("TransitionRun(succeeded -> running) err = %v, want ErrInvalidTransition", err)
	}
}

// Reruns of an issue reuse its workflow ID; each execution is its own Run.
func TestRunsShareWorkflowIDButNotTemporalRunID(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning)
	first, err := s.CreateRun(ctx, NewRun{IssueID: is.ID, WorkflowSnapshot: json.RawMessage(`{}`), WorkflowVersion: "1",
		TemporalWorkflowID: "issue-p-1", TemporalRunID: "exec-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionRun(ctx, first.ID, domain.ExecPending, domain.ExecFailed); err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateRun(ctx, NewRun{IssueID: is.ID, WorkflowSnapshot: json.RawMessage(`{}`), WorkflowVersion: "1",
		TemporalWorkflowID: "issue-p-1", TemporalRunID: "exec-2"})
	if err != nil {
		t.Fatalf("rerun with the same workflow ID: %v", err)
	}
	got, err := s.GetRunByTemporalRunID(ctx, "exec-2")
	if err != nil || got.ID != second.ID || got.TemporalRunID != "exec-2" {
		t.Fatalf("GetRunByTemporalRunID = %+v, %v", got, err)
	}
	if _, err := s.CreateRun(ctx, NewRun{IssueID: mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning).ID,
		WorkflowSnapshot: json.RawMessage(`{}`), WorkflowVersion: "1", TemporalWorkflowID: "x", TemporalRunID: "exec-2"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate temporal run id: err = %v, want ErrConflict", err)
	}
	if _, err := s.GetRunByTemporalRunID(ctx, "exec-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown run id: err = %v", err)
	}
}

func TestListRecentRuns(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning)
	r := mustRun(t, s, is.ID)
	runs, err := s.ListRecentRuns(t.Context(), 10)
	if err != nil || len(runs) != 1 || runs[0].ID != r.ID || runs[0].IssueKey != is.Key || runs[0].IssueTitle != is.Title {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
}

func TestSetRunLoopCount(t *testing.T) {
	s := newTestStore(t)
	run := mustRun(t, s, mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning).ID)
	ctx := t.Context()
	for _, step := range []struct {
		id    string
		loops int
	}{{"checks", 1}, {"review", 1}, {"checks", 2}, {"checks", 2}} {
		if _, err := s.SetRunLoopCount(ctx, run.ID, step.id, step.loops); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LoopCounts) != 2 || got.LoopCounts["checks"] != 2 || got.LoopCounts["review"] != 1 {
		t.Fatalf("loop counts = %v", got.LoopCounts)
	}
	if _, err := s.SetRunLoopCount(ctx, uuid.New(), "checks", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown run: err = %v, want ErrNotFound", err)
	}
	if _, err := s.SetRunLoopCount(ctx, run.ID, "", 1); err == nil {
		t.Fatal("empty step id accepted")
	}
}

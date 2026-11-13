package activities

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

func TestFinishRun(t *testing.T) {
	for _, succeeded := range []bool{true, false} {
		t.Run(map[bool]string{true: "succeeded", false: "failed"}[succeeded], func(t *testing.T) {
			st, in := readyIssue(t)
			a := &Activities{Store: st, Log: quietLog()}
			env := activityEnv(a)
			var run StartRunResult
			v, err := env.ExecuteActivity(a.StartRun, in)
			if err != nil {
				t.Fatal(err)
			}
			_ = v.Get(&run)
			stepID := uuid.MustParse(run.StepRunID)
			if _, err := st.TransitionStepRun(t.Context(), stepID, domain.ExecPending, domain.ExecRunning); err != nil {
				t.Fatal(err)
			}

			finish := FinishInput{IssueID: in.IssueID, RunID: run.RunID, StepRunID: run.StepRunID, Succeeded: succeeded, Reason: "agent session made no changes"}
			if _, err := env.ExecuteActivity(a.FinishRun, finish); err != nil {
				t.Fatalf("FinishRun: %v", err)
			}
			if _, err := env.ExecuteActivity(a.FinishRun, finish); err != nil {
				t.Fatalf("retried FinishRun: %v", err)
			}

			wantExec, wantIssue := domain.ExecFailed, domain.IssueFailed
			if succeeded {
				wantExec, wantIssue = domain.ExecSucceeded, domain.IssueAwaitingHuman
			}
			issue, _ := st.GetIssueByKey(t.Context(), in.IssueKey)
			r, _ := st.GetRun(t.Context(), uuid.MustParse(run.RunID))
			step, _ := st.GetStepRun(t.Context(), stepID)
			if issue.Status != wantIssue || r.Status != wantExec || step.Status != wantExec {
				t.Errorf("issue %s run %s step %s; want %s/%s/%s", issue.Status, r.Status, step.Status, wantIssue, wantExec, wantExec)
			}

			events, _ := st.ListRunEvents(t.Context(), r.ID, 0, 100)
			var reasons []string
			for _, e := range events {
				if e.Type == domain.EventError {
					var p map[string]string
					_ = json.Unmarshal(e.Payload, &p)
					reasons = append(reasons, p["detail"])
				}
			}
			if succeeded && len(reasons) != 0 || !succeeded && (len(reasons) == 0 || reasons[0] != finish.Reason) {
				t.Errorf("error events = %v", reasons)
			}
		})
	}
}

// A run whose gate failed is failed, but its agent step did its job.
func TestFinishRunAfterAFailedGateKeepsTheAgentStepSucceeded(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	env := activityEnv(a)
	var run StartRunResult
	v, err := env.ExecuteActivity(a.StartRun, in)
	if err != nil {
		t.Fatal(err)
	}
	_ = v.Get(&run)
	stepID := uuid.MustParse(run.StepRunID)
	if _, err := st.TransitionStepRun(t.Context(), stepID, domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	finish := FinishInput{IssueID: in.IssueID, RunID: run.RunID, StepRunID: run.StepRunID,
		Reason: "command gate failed: mise run test exited 1", AgentSucceeded: true}
	if _, err := env.ExecuteActivity(a.FinishRun, finish); err != nil {
		t.Fatal(err)
	}
	r, _ := st.GetRun(t.Context(), uuid.MustParse(run.RunID))
	step, _ := st.GetStepRun(t.Context(), stepID)
	if r.Status != domain.ExecFailed || step.Status != domain.ExecSucceeded {
		t.Errorf("run %s, agent step %s; want failed and succeeded", r.Status, step.Status)
	}
}

// Interpret ends its own steps and finishes the run without a StepRunID; the
// reason still reaches the run's transcript.
func TestFinishRunWithoutAStep(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)
	finish := FinishInput{IssueID: in.IssueID, RunID: run.RunID, Reason: "step checks: gate failed: mise run test exited 1"}
	if _, err := activityEnv(a).ExecuteActivity(a.FinishRun, finish); err != nil {
		t.Fatal(err)
	}
	r, _ := st.GetRun(t.Context(), uuid.MustParse(run.RunID))
	issue, _ := st.GetIssueByKey(t.Context(), in.IssueKey)
	if r.Status != domain.ExecFailed || issue.Status != domain.IssueFailed {
		t.Errorf("run %s, issue %s", r.Status, issue.Status)
	}
	events, _ := st.ListRunEvents(t.Context(), r.ID, 0, 100)
	found := false
	for _, e := range events {
		if e.Type == domain.EventError && e.StepRunID == nil {
			found = true
		}
	}
	if !found {
		t.Error("no run-level error event")
	}
}

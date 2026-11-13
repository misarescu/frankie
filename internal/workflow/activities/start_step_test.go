package activities

import (
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
)

// startedRun is a running Interpret run for the issue.
func startedRun(t *testing.T, a *Activities, in IssueRunInput) StartRunResult {
	t.Helper()
	var run StartRunResult
	v, err := activityEnv(a).ExecuteActivity(a.StartWorkflowRun, in, snapshot.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	_ = v.Get(&run)
	return run
}

func startStep(t *testing.T, a *Activities, in StepStart) string {
	t.Helper()
	v, err := activityEnv(a).ExecuteActivity(a.StartStep, in)
	if err != nil {
		t.Fatalf("StartStep(%+v): %v", in, err)
	}
	var id string
	_ = v.Get(&id)
	return id
}

// Every execution of a step is its own StepRun; a retried StartStep finds it.
func TestStartStep(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)

	first := startStep(t, a, StepStart{RunID: run.RunID, StepID: "implement", Kind: domain.StepAgent, Attempt: 1})
	retried := startStep(t, a, StepStart{RunID: run.RunID, StepID: "implement", Kind: domain.StepAgent, Attempt: 1})
	loop := startStep(t, a, StepStart{RunID: run.RunID, StepID: "implement", Kind: domain.StepAgent, Attempt: 2})
	if first != retried || first == loop {
		t.Fatalf("first %s, retried %s, loop %s", first, retried, loop)
	}
	step, err := st.GetStepRun(t.Context(), uuid.MustParse(loop))
	if err != nil {
		t.Fatal(err)
	}
	if step.StepID != "implement" || step.Attempt != 2 || step.Kind != domain.StepAgent || step.Status != domain.ExecPending {
		t.Errorf("step = %+v", step)
	}
}

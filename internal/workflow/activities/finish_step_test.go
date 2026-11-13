package activities

import (
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

func TestFinishStep(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)
	env := activityEnv(a)
	status := func(id string) domain.ExecStatus {
		s, err := st.GetStepRun(t.Context(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		return s.Status
	}

	// An action never ran a pod, so it goes from pending to succeeded.
	action := startStep(t, a, StepStart{RunID: run.RunID, StepID: "open", Kind: domain.StepAction, Attempt: 1})
	for range 2 { // retried
		if _, err := env.ExecuteActivity(a.FinishStep, StepEnd{StepRunID: action, Succeeded: true}); err != nil {
			t.Fatal(err)
		}
	}
	if s := status(action); s != domain.ExecSucceeded {
		t.Errorf("action = %s", s)
	}

	// An agent step whose pod ran fails from running.
	agent := startStep(t, a, StepStart{RunID: run.RunID, StepID: "implement", Kind: domain.StepAgent, Attempt: 1})
	if _, err := st.TransitionStepRun(t.Context(), uuid.MustParse(agent), domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ExecuteActivity(a.FinishStep, StepEnd{StepRunID: agent}); err != nil {
		t.Fatal(err)
	}
	if s := status(agent); s != domain.ExecFailed {
		t.Errorf("agent = %s", s)
	}

	// A step that already ended (a gate records its own verdict) keeps its status.
	if _, err := env.ExecuteActivity(a.FinishStep, StepEnd{StepRunID: agent, Succeeded: true}); err != nil {
		t.Fatal(err)
	}
	if s := status(agent); s != domain.ExecFailed {
		t.Errorf("agent after a late success = %s", s)
	}
}

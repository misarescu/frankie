package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// Each agent session takes an hour of workflow time, so signals can reach it.
func slowSessions(status string) func(*interpretFixture) {
	return func(f *interpretFixture) {
		f.sessionTime = time.Hour
		f.sessionStatus = status
	}
}

// docs/spec/04, "Human intervention": a steer sent while an agent step runs
// reaches that step's runner; one addressed to another step does not.
func TestSteerReachesTheRunningStep(t *testing.T) {
	f := newInterpretFixture(t, slowSessions(""))
	f.env.RegisterDelayedCallback(func() {
		f.env.SignalWorkflow(SteerSignal, StepIntervention{StepRunID: "sr-earlier-1", By: "ana", Text: "ignored"})
		f.env.SignalWorkflow(SteerSignal, StepIntervention{StepRunID: "sr-implement-1", By: "ana", Text: "use the helper"})
	}, 10*time.Minute)
	out := f.run(t, loopSnapshot(t, loopDefinition))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	want := activities.Intervention{RunID: testRun.RunID, StepRunID: "sr-implement-1", Action: domain.InterventionSteer, By: "ana", Text: "use the helper"}
	if len(f.interventions) != 1 || f.interventions[0] != want {
		t.Errorf("interventions = %+v", f.interventions)
	}
}

// D52: an abort stops the step's session and fails the run, naming who
// aborted it and why; later steps do not run.
func TestAbortFailsTheRun(t *testing.T) {
	f := newInterpretFixture(t, slowSessions("aborted"))
	f.env.RegisterDelayedCallback(func() {
		f.env.SignalWorkflow(AbortSignal, StepIntervention{StepRunID: "sr-implement-1", By: "ana", Text: "wrong approach"})
	}, 10*time.Minute)
	out := f.run(t, loopSnapshot(t, loopDefinition))
	if out.Succeeded || !strings.Contains(out.Reason, "aborted by ana: wrong approach") {
		t.Fatalf("result = %+v", out)
	}
	if len(f.interventions) != 1 || f.interventions[0].Action != domain.InterventionAbort {
		t.Errorf("interventions = %+v", f.interventions)
	}
	if len(f.gates) != 0 {
		t.Errorf("gates ran after the abort: %+v", f.gates)
	}
	if f.ended["sr-implement-1"] {
		t.Error("the aborted step ended as succeeded")
	}
}

// D55: a step's session offers the ask-human tool, and an answer for the
// running step reaches its runner naming the question; an answer naming no
// question, or for another step, does not.
func TestAnswerReachesTheRunningStep(t *testing.T) {
	f := newInterpretFixture(t, slowSessions(""))
	f.env.RegisterDelayedCallback(func() {
		f.env.SignalWorkflow(AnswerSignal, StepIntervention{StepRunID: "sr-implement-1", By: "ana", Text: "no question"})
		f.env.SignalWorkflow(AnswerSignal, StepIntervention{StepRunID: "sr-earlier-1", By: "ana", Text: "ignored", QuestionID: "q0"})
		f.env.SignalWorkflow(AnswerSignal, StepIntervention{StepRunID: "sr-implement-1", By: "ana", Text: "8080", QuestionID: "q1"})
	}, 10*time.Minute)
	out := f.run(t, loopSnapshot(t, loopDefinition))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if len(f.sessions) == 0 || !f.sessions[0].Step.AskHuman {
		t.Errorf("the session was not offered ask-human: %+v", f.sessions)
	}
	want := activities.Intervention{RunID: testRun.RunID, StepRunID: "sr-implement-1", Action: domain.InterventionAnswer, By: "ana", Text: "8080", QuestionID: "q1"}
	if len(f.interventions) != 1 || f.interventions[0] != want {
		t.Errorf("interventions = %+v", f.interventions)
	}
}

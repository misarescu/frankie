package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/worker"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/workflow/activities"
	"github.com/misarescu/frankie/roles"
)

const refineDefinition = `
name: refine
version: 1
steps:
  - id: refine
    agent: {role: refiner}
`

func refineSnapshot(t *testing.T) snapshot.Snapshot {
	t.Helper()
	w, err := config.ParseWorkflow([]byte(refineDefinition))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := config.ResolveRefinementRoles(w, roles.Get)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Snapshot{Workflow: w, Roles: rs, Source: "built-in", BaseSHA: "base"}
}

// refineFixture is the Interpret fixture plus the refinement's own Activities.
type refineFixture struct {
	*interpretFixture
	started bool
	outcome *activities.RefinementOutcome
	verdict string // what FinishRefinement answers
}

func newRefineFixture(t *testing.T, opts ...func(*interpretFixture)) *refineFixture {
	t.Helper()
	f := &refineFixture{interpretFixture: newInterpretFixture(t, opts...)}
	var a *activities.Activities
	f.env.OnActivity(a.StartRefinement, mock.Anything, testInput, mock.Anything).Return(func(context.Context, activities.IssueRunInput, snapshot.Snapshot) (activities.StartRunResult, error) {
		f.started = true
		return activities.StartRunResult{RunID: testRun.RunID, Branch: testRun.Branch}, nil
	})
	f.env.OnActivity(a.FinishRefinement, mock.Anything, mock.Anything).Return(func(_ context.Context, out activities.RefinementOutcome) (string, error) {
		f.outcome = &out
		return f.verdict, nil
	})
	return f
}

func (f *refineFixture) run(t *testing.T, snap snapshot.Snapshot) RefineResult {
	t.Helper()
	f.env.ExecuteWorkflow(Refine, RefineInput{Issue: testInput, Snapshot: snap})
	if !f.env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := f.env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var out RefineResult
	if err := f.env.GetWorkflowResult(&out); err != nil {
		t.Fatal(err)
	}
	if !f.started || f.outcome == nil || out.RunID != testRun.RunID {
		t.Fatalf("started %v, outcome %+v, result %+v", f.started, f.outcome, out)
	}
	return out
}

// D66: the refiner runs as a read-only agent step, can be asked and steered,
// and its result goes to FinishRefinement.
func TestRefineRunsTheRefiner(t *testing.T) {
	f := newRefineFixture(t)
	out := f.run(t, refineSnapshot(t))
	if !out.Succeeded || out.Reason != "" {
		t.Fatalf("result = %+v", out)
	}
	if f.stepIDs() != "refine/1" || len(f.sessions) != 1 {
		t.Fatalf("steps %s, sessions %d", f.stepIDs(), len(f.sessions))
	}
	s := f.sessions[0].Step
	if s.Role != "refiner" || s.Contract != config.ContractRefinedSpec || !s.AskHuman || s.SystemPrompt == "" {
		t.Errorf("session step = %+v", s)
	}
	if !f.outcome.Succeeded || f.outcome.Result == "" || f.outcome.IssueID != testInput.IssueID || f.outcome.RunID != testRun.RunID {
		t.Errorf("outcome = %+v", f.outcome)
	}
	if ok, seen := f.ended["sr-refine-1"]; !seen || !ok {
		t.Error("the refine step was not ended as succeeded")
	}
}

func TestRefineFailures(t *testing.T) {
	t.Run("the session fails", func(t *testing.T) {
		f := newRefineFixture(t, func(f *interpretFixture) { f.sessionStatus = "failed" })
		f.verdict = "agent session failed: stopped"
		out := f.run(t, refineSnapshot(t))
		if out.Succeeded || f.outcome.Succeeded || !strings.Contains(f.outcome.Reason, "stopped") || out.Reason != f.verdict {
			t.Fatalf("result %+v, outcome %+v", out, f.outcome)
		}
	})
	t.Run("the result is not a refined spec", func(t *testing.T) {
		f := newRefineFixture(t)
		f.verdict = "the refiner's result is not a refined spec: unknown field"
		if out := f.run(t, refineSnapshot(t)); out.Succeeded || out.Reason != f.verdict {
			t.Fatalf("result = %+v", out)
		}
	})
	t.Run("an invalid configuration runs nothing", func(t *testing.T) {
		f := newRefineFixture(t)
		f.verdict = "x"
		f.run(t, snapshot.Snapshot{Error: ".factory/roles/refiner.yaml: bad"})
		if len(f.sessions) != 0 || f.outcome.Succeeded || f.outcome.Reason != ".factory/roles/refiner.yaml: bad" {
			t.Fatalf("sessions %d, outcome %+v", len(f.sessions), f.outcome)
		}
	})
}

func TestRefineReplaysRecordedHistories(t *testing.T) {
	r := worker.NewWorkflowReplayer()
	r.RegisterWorkflow(Refine)
	if err := r.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/refine_proposal_recorded.json"); err != nil {
		t.Fatal(err)
	}
}

package workflow

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/workflow/activities"
)

// writersDefinition runs a coder and a second writer in one group (D122).
const writersDefinition = `
name: e2e
version: 1
steps:
  - id: build
    parallel:
      - id: implement
        agent: {role: coder}
      - id: e2e-tests
        agent: {role: coder}
        inputs: [spec]
  - id: keeps-tests
    gate: {type: preserve, step: e2e-tests, onFail: {goto: implement, maxLoops: 2}}
  - id: checks
    gate: {type: command, onFail: {goto: implement, maxLoops: 2}}
  - id: open
    action: change-request.open
`

// session returns the recorded session of a step's attempt.
func (f *interpretFixture) session(t *testing.T, stepRunID string) activities.StartRunResult {
	t.Helper()
	for _, s := range f.sessions {
		if s.StepRunID == stepRunID {
			return s
		}
	}
	t.Fatalf("no session %s in %v", stepRunID, f.sessions)
	return activities.StartRunResult{}
}

// M7 (D122): the writers of a group run at the same time, the second on its
// own branch, which is then merged into the issue's branch; later steps check
// the merged commit.
func TestInterpretWritersRunAtTheSameTime(t *testing.T) {
	f := newInterpretFixture(t, func(f *interpretFixture) { f.sessionTime = time.Hour })
	start := f.env.Now()
	out := f.run(t, loopSnapshot(t, writersDefinition))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if took := f.env.Now().Sub(start); took >= 2*time.Hour {
		t.Errorf("the run took %s: the writers ran one after another", took)
	}
	coder, writer := f.session(t, "sr-implement-1"), f.session(t, "sr-e2e-tests-1")
	if coder.Branch != testRun.Branch || writer.Branch != testRun.Branch+"--e2e-tests" {
		t.Errorf("branches = %s and %s", coder.Branch, writer.Branch)
	}
	if got := f.stepIDs(); !strings.HasSuffix(got, "build-merge/1 keeps-tests/1 checks/1 open/1") ||
		!strings.Contains(got, "implement/1") || !strings.Contains(got, "e2e-tests/1") {
		t.Errorf("steps = %s", got)
	}
	if len(f.updates) != 1 || f.updates[0].Branch != testRun.Branch || f.updates[0].From != writer.Branch ||
		f.updates[0].HeadSHA != "sha1" || f.updates[0].StepID != "build-merge" || f.updates[0].StepRunID != "sr-build-merge-1" {
		t.Errorf("updates = %+v; want the side branch merged into the issue's at the coder's commit", f.updates)
	}
	// The preserve gate checks what the writer pushed, from the base commit,
	// against the merged head; the command gate checks the merged head.
	if len(f.preserved) != 1 || f.preserved[0].From != "base" || f.preserved[0].To != "side-sr-e2e-tests-1" || f.preserved[0].Head != "merged-1" {
		t.Errorf("preserved = %+v", f.preserved)
	}
	if len(f.gates) != 1 || f.gates[0].CommitSHA != "merged-1" {
		t.Errorf("gates = %+v", f.gates)
	}
	if !f.ended["sr-implement-1"] || !f.ended["sr-e2e-tests-1"] {
		t.Errorf("ended = %v", f.ended)
	}
}

// A loop back to a branch runs only that branch: the coder's goes on the
// issue's branch with nothing to merge; the side writer's is merged again,
// its pushed range starting where its branch was.
func TestInterpretWritersLoopBackToABranch(t *testing.T) {
	t.Run("the coder", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.gate = func(req activities.GateRequest) activities.GateOutcome {
			return activities.GateOutcome{Passed: req.CommitSHA != "merged-1", Summary: "mise run test exited 1"}
		}
		if out := f.run(t, loopSnapshot(t, writersDefinition)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); !strings.HasSuffix(got, "build-merge/1 keeps-tests/1 checks/1 implement/2 keeps-tests/2 checks/2 open/1") {
			t.Errorf("steps = %s", got)
		}
		if s := f.session(t, "sr-implement-2"); s.Branch != testRun.Branch || len(f.updates) != 1 {
			t.Errorf("rerun on %s with %d updates", s.Branch, len(f.updates))
		}
		if len(f.gates) != 2 || f.gates[1].CommitSHA != "sha2" {
			t.Errorf("gates = %+v", f.gates)
		}
	})
	t.Run("the side writer", func(t *testing.T) {
		const def = `
name: e2e
version: 1
steps:
  - id: build
    parallel:
      - id: implement
        agent: {role: coder}
      - id: e2e-tests
        agent: {role: coder}
  - id: checks
    gate: {type: command, onFail: {goto: e2e-tests, maxLoops: 1}}
  - id: keeps-tests
    gate: {type: preserve, step: e2e-tests}
`
		f := newInterpretFixture(t)
		f.gate = func(req activities.GateRequest) activities.GateOutcome {
			return activities.GateOutcome{Passed: req.CommitSHA != "merged-1", Summary: "e2e exited 1"}
		}
		if out := f.run(t, loopSnapshot(t, def)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if s := f.session(t, "sr-e2e-tests-2"); s.Branch != testRun.Branch+"--e2e-tests" {
			t.Errorf("rerun on %s", s.Branch)
		}
		if len(f.updates) != 2 || f.updates[1].HeadSHA != "merged-1" || f.updates[1].StepRunID != "sr-build-merge-2" {
			t.Errorf("updates = %+v; want the side branch merged again into the merged head", f.updates)
		}
		if len(f.preserved) != 1 || f.preserved[0].From != "side-sr-e2e-tests-1" || f.preserved[0].To != "side-sr-e2e-tests-2" || f.preserved[0].Head != "merged-2" {
			t.Errorf("preserved = %+v", f.preserved)
		}
	})
}

// A writer that fails cancels the others and fails the run; a conflict when
// merging fails it with the files.
func TestInterpretWritersFail(t *testing.T) {
	t.Run("a writer fails", func(t *testing.T) {
		f := newInterpretFixture(t, func(f *interpretFixture) { f.sessionTime = time.Hour })
		f.statusFor = func(run activities.StartRunResult) string {
			if run.Step.StepID == "e2e-tests" {
				return "failed"
			}
			return ""
		}
		out := f.run(t, loopSnapshot(t, writersDefinition))
		if out.Succeeded || !strings.Contains(out.Reason, "branch e2e-tests: agent session failed") {
			t.Fatalf("result = %+v", out)
		}
		if f.ended["sr-e2e-tests-1"] || len(f.updates) != 0 || slices.ContainsFunc(f.steps, func(s activities.StepStart) bool { return s.StepID == "checks" }) {
			t.Errorf("ended %v, updates %v, steps %s", f.ended, f.updates, f.stepIDs())
		}
	})
	t.Run("the side branch conflicts", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.update = func(activities.BranchUpdateRequest) activities.BranchUpdateResult {
			return activities.BranchUpdateResult{Outcome: activities.BranchConflict, Conflicts: []string{"api/README.md"}}
		}
		out := f.run(t, loopSnapshot(t, writersDefinition))
		if out.Succeeded || !strings.Contains(out.Reason, "conflicts with "+testRun.Branch+" in api/README.md") {
			t.Fatalf("result = %+v", out)
		}
	})
}

// Steers and aborts reach the writer they address while both run (D52):
// neither session takes the other's.
func TestInterpretWritersTakeTheirOwnSignals(t *testing.T) {
	f := newInterpretFixture(t, func(f *interpretFixture) { f.sessionTime = time.Hour })
	for _, id := range []string{"sr-e2e-tests-1", "sr-implement-1"} {
		f.env.RegisterDelayedCallback(func() {
			f.env.SignalWorkflow(SteerSignal, StepIntervention{StepRunID: id, By: "mi", Text: "steer " + id})
		}, 10*time.Minute)
	}
	if out := f.run(t, loopSnapshot(t, writersDefinition)); !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	got := map[string]string{}
	for _, in := range f.interventions {
		got[in.StepRunID] = in.Text
	}
	if len(f.interventions) != 2 || got["sr-e2e-tests-1"] != "steer sr-e2e-tests-1" || got["sr-implement-1"] != "steer sr-implement-1" {
		t.Errorf("interventions = %+v", f.interventions)
	}
}

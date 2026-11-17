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
	// Each writer learns the roles of the others.
	if !slices.Equal(coder.Step.Alongside, []string{"coder"}) || !slices.Equal(writer.Step.Alongside, []string{"coder"}) {
		t.Errorf("alongside = %v and %v", coder.Step.Alongside, writer.Step.Alongside)
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

// disputeDefinition is build-e2e's shape: the side writer gets feedback.
const disputeDefinition = `
name: e2e
version: 1
steps:
  - id: build
    parallel:
      - id: implement
        agent: {role: coder}
      - id: e2e-tests
        agent: {role: coder}
        inputs: [spec, feedback]
  - id: keeps-tests
    gate: {type: preserve, step: e2e-tests, onFail: {goto: implement, maxLoops: 2}}
  - id: e2e
    gate: {type: command, onFail: {goto: implement, maxLoops: 2}}
  - id: open
    action: change-request.open
`

// failE2EOnce fails the e2e gate on the first merged head only.
func failE2EOnce(req activities.GateRequest) activities.GateOutcome {
	if req.CommitSHA == "merged-1" {
		return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "mise run e2e:local exited 1",
			Feedback: "4 of 152 requests failed: gc-77 Similar books ranked by shared tags"}
	}
	return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "all checks passed"}
}

// M7 (D128): a coder sent back that changes nothing finds the failure in the
// other writer's work: that writer runs with the coder's report, its fix is
// merged, and the run checks the result again.
func TestInterpretWritersDispute(t *testing.T) {
	f := newInterpretFixture(t)
	f.gate = failE2EOnce
	f.unchanged = func(run activities.StartRunResult) bool { return run.StepRunID == "sr-implement-2" }
	if out := f.run(t, loopSnapshot(t, disputeDefinition)); !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); !strings.HasSuffix(got, "build-merge/1 keeps-tests/1 e2e/1 implement/2 e2e-tests/2 build-merge/2 keeps-tests/2 e2e/2 open/1") {
		t.Errorf("steps = %s", got)
	}
	if !f.ended["sr-implement-2"] || !f.ended["sr-e2e-tests-2"] || len(f.opened) != 0 || !slices.Equal(f.loops, []string{"e2e=1"}) {
		t.Errorf("ended %v, opened %+v, loops %v", f.ended, f.opened, f.loops)
	}
	writer := f.session(t, "sr-e2e-tests-2")
	if writer.Branch != testRun.Branch+"--e2e-tests" {
		t.Errorf("writer ran on %s", writer.Branch)
	}
	for _, want := range []string{"The `e2e` check failed on the change with your work merged in: mise run e2e:local exited 1",
		"gc-77 Similar books ranked by shared tags", "It went back to the coder, which changed nothing",
		"Nothing to change: report of sr-implement-2", "a person then decides"} {
		if !strings.Contains(writer.Step.Feedback, want) {
			t.Errorf("writer feedback lacks %q:\n%s", want, writer.Step.Feedback)
		}
	}
	// The fix is merged into the head the gate failed, and the writer's
	// whole branch stays protected.
	if len(f.updates) != 2 || f.updates[1].HeadSHA != "merged-1" || f.updates[1].From != writer.Branch {
		t.Errorf("updates = %+v", f.updates)
	}
	if len(f.preserved) != 2 || f.preserved[1].From != "base" || f.preserved[1].To != "side-sr-e2e-tests-2" || f.preserved[1].Head != "merged-2" {
		t.Errorf("preserved = %+v", f.preserved)
	}
	if len(f.gates) != 2 || f.gates[1].CommitSHA != "merged-2" {
		t.Errorf("gates = %+v", f.gates)
	}
}

// When the writer stands by its work too, or the writer sent back is the
// side one, a person decides on the gate with every report.
func TestInterpretWritersDisputeEscalates(t *testing.T) {
	t.Run("both writers change nothing, approved", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.gate = failE2EOnce
		f.unchanged = func(run activities.StartRunResult) bool {
			return run.StepRunID == "sr-implement-2" || run.StepRunID == "sr-e2e-tests-2"
		}
		f.decide(t, 3*time.Hour, 0, true, "the spec is ambiguous; ship it")
		if out := f.run(t, loopSnapshot(t, disputeDefinition)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); !strings.HasSuffix(got, "e2e/1 implement/2 e2e-tests/2 e2e-escalation/1 open/1") {
			t.Errorf("steps = %s", got)
		}
		if len(f.opened) != 1 || f.opened[0].CommitSHA != "merged-1" || len(f.updates) != 1 {
			t.Fatalf("opened %+v, updates %+v", f.opened, f.updates)
		}
		for _, want := range []string{"`implement` (coder) and `e2e-tests` (coder) changed nothing",
			"Nothing to change: report of sr-implement-2", "Nothing to change: report of sr-e2e-tests-2"} {
			if !strings.Contains(f.opened[0].Reason, want) {
				t.Errorf("reason lacks %q:\n%s", want, f.opened[0].Reason)
			}
		}
	})
	t.Run("the side writer sent back changes nothing, rejected", func(t *testing.T) {
		def := strings.Replace(disputeDefinition, "type: command, onFail: {goto: implement", "type: command, onFail: {goto: e2e-tests", 1)
		f := newInterpretFixture(t)
		f.gate = failE2EOnce
		f.unchanged = func(run activities.StartRunResult) bool { return run.StepRunID == "sr-e2e-tests-2" }
		f.decide(t, 3*time.Hour, 0, false, "")
		out := f.run(t, loopSnapshot(t, def))
		if out.Succeeded || !strings.Contains(out.Reason, "sent back, `e2e-tests` (coder) changed nothing; escalated to a human, rejected by web") {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); !strings.HasSuffix(got, "e2e/1 e2e-tests/2 e2e-escalation/1") {
			t.Errorf("steps = %s", got)
		}
	})
}

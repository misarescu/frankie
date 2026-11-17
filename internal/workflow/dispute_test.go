package workflow

import (
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/workflow/activities"
)

// failChecksOnce fails the checks gate on the first commit only.
func failChecksOnce(req activities.GateRequest) activities.GateOutcome {
	if req.StepID == "checks" && req.CommitSHA == "sha1" {
		return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "mise run test exited 1", Feedback: "--- FAIL: TestRatings\n"}
	}
	return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "all checks passed"}
}

// M7 (D128): a coder a failed gate sent back that changes nothing disputes
// the failure; without another writer to send it to, a person decides on the
// gate. Approving continues after it, on the commit it checked.
func TestInterpretDisputeEscalates(t *testing.T) {
	coderUnchanged := func(run activities.StartRunResult) bool { return run.StepRunID == "sr-implement-2" }
	t.Run("approved", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.gate, f.unchanged = failChecksOnce, coderUnchanged
		f.decide(t, time.Hour, 0, true, "the test is flaky")
		out := f.run(t, loopSnapshot(t, loopDefinition))
		if !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); got != "implement/1 checks/1 implement/2 checks-escalation/1 style/1 open/1" {
			t.Errorf("steps = %s", got)
		}
		if !f.ended["sr-implement-2"] {
			t.Errorf("the disputing session's step failed: %v", f.ended)
		}
		if len(f.opened) != 1 || f.opened[0].StepID != "checks-escalation" || f.opened[0].CommitSHA != "sha1" {
			t.Fatalf("opened = %+v", f.opened)
		}
		for _, want := range []string{"The `checks` gate failed: mise run test exited 1.", "`implement` (coder) changed nothing",
			"Nothing to change: report of sr-implement-2", "continue after the gate"} {
			if !strings.Contains(f.opened[0].Reason, want) {
				t.Errorf("reason lacks %q:\n%s", want, f.opened[0].Reason)
			}
		}
		if len(f.gates) != 2 || f.gates[1].StepID != "style" || f.gates[1].CommitSHA != "sha1" {
			t.Errorf("gates = %+v", f.gates)
		}
	})
	t.Run("rejected", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.gate, f.unchanged = failChecksOnce, coderUnchanged
		f.decide(t, time.Hour, 0, false, "fix it")
		out := f.run(t, loopSnapshot(t, loopDefinition))
		if out.Succeeded || !strings.Contains(out.Reason, "step implement: gate failed: mise run test exited 1; sent back, `implement` (coder) changed nothing; escalated to a human, rejected by web: fix it") {
			t.Fatalf("result = %+v", out)
		}
	})
	t.Run("a group branch", func(t *testing.T) {
		const def = `
name: grouped
version: 1
steps:
  - id: build
    parallel:
      - id: implement
        agent: {role: coder}
  - id: checks
    gate: {type: command, onFail: {goto: implement, maxLoops: 2}}
  - id: open
    action: change-request.open
`
		f := newInterpretFixture(t)
		f.gate, f.unchanged = failChecksOnce, coderUnchanged
		f.decide(t, time.Hour, 0, true, "")
		if out := f.run(t, loopSnapshot(t, def)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); got != "implement/1 checks/1 implement/2 checks-escalation/1 open/1" {
			t.Errorf("steps = %s", got)
		}
	})
}

// Changing nothing is still a failure where no failed gate sent the change
// back, and in runs recorded before D128.
func TestInterpretUndisputedNoChanges(t *testing.T) {
	t.Run("sent back by the merge queue", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.queue = func(req activities.EnqueueRequest) QueueOutcome {
			if req.Attempt == 1 {
				return QueueOutcome{Kind: QueueOutcomeConflict, Reason: "conflicts with main", Conflicts: []string{"go.mod"}}
			}
			return QueueOutcome{Kind: QueueOutcomeMerged, MergeSHA: "m3rge"}
		}
		f.unchanged = func(run activities.StartRunResult) bool { return run.StepRunID == "sr-implement-2" }
		out := f.run(t, defaultBuildSnapshot(t))
		if out.Succeeded || !strings.Contains(out.Reason, "step implement: agent session made no changes") || len(f.opened) != 0 {
			t.Fatalf("result = %+v, opened %+v", out, f.opened)
		}
	})
	t.Run("a run recorded before", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.env.OnGetVersion("disputed-changes", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
		f.gate = failChecksOnce
		f.unchanged = func(run activities.StartRunResult) bool { return run.StepRunID == "sr-implement-2" }
		out := f.run(t, loopSnapshot(t, loopDefinition))
		if out.Succeeded || out.Reason != "step implement: agent session made no changes" || f.ended["sr-implement-2"] {
			t.Fatalf("result = %+v", out)
		}
	})
}

func TestClip(t *testing.T) {
	if got := clip("  short report \n"); got != "short report" {
		t.Errorf("clip = %q", got)
	}
	long := strings.Repeat("line of the report\n", 500)
	got := clip(long)
	if len(got) > maxReport+len("\n[...]") || !strings.HasSuffix(got, "report\n[...]") {
		t.Errorf("clip kept %d bytes ending %q", len(got), got[len(got)-20:])
	}
	// Without line breaks it cuts at a rune boundary.
	got = clip(strings.Repeat("é", maxReport))
	if !strings.HasSuffix(got, "é\n[...]") {
		t.Errorf("clip cut a rune: %q", got[len(got)-10:])
	}
}

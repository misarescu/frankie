package workflow

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/guard"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// approvalDefinition asks a human after the change request is open, so the
// person can read the diff on the provider first.
const approvalDefinition = `
name: gated
version: 1
steps:
  - id: implement
    agent: {role: coder}
  - id: open
    action: change-request.open
  - id: approve
    gate: {type: human, timeout: 1h}
`

// decide signals a decision on the n-th human gate the run opened, after delay.
func (f *interpretFixture) decide(t *testing.T, delay time.Duration, n int, approved bool, comment string) {
	t.Helper()
	f.env.RegisterDelayedCallback(func() {
		if len(f.opened) <= n {
			t.Errorf("no human gate #%d is open", n)
			return
		}
		f.env.SignalWorkflow(HumanDecisionSignal, HumanDecision{StepRunID: f.opened[n].StepRunID, Approved: approved, By: "web", Comment: comment})
	}, delay)
}

// requestChanges sends the n-th opened human gate back to the coder (D92).
func (f *interpretFixture) requestChanges(t *testing.T, delay time.Duration, n int, comment string) {
	t.Helper()
	f.env.RegisterDelayedCallback(func() {
		if len(f.opened) <= n {
			t.Errorf("no human gate #%d is open", n)
			return
		}
		f.env.SignalWorkflow(HumanDecisionSignal, HumanDecision{StepRunID: f.opened[n].StepRunID, By: "ana", Comment: comment, Action: DecisionRequestChanges})
	}, delay)
}

// FR-WF-3: a human gate waits for the decision signal.
func TestHumanGateApproved(t *testing.T) {
	f := newInterpretFixture(t)
	f.decide(t, 10*time.Minute, 0, true, "diff looks right")
	out := f.run(t, loopSnapshot(t, approvalDefinition))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 open/1 approve/1" {
		t.Errorf("steps = %s", got)
	}
	o := f.opened[0]
	if o.StepID != "approve" || o.ChangeRequestURL != "https://github.com/acme/app/pull/9" || o.CommitSHA != "sha1" ||
		!strings.Contains(o.Reason, "`approve` gate") {
		t.Errorf("opened = %+v", o)
	}
	if len(f.closed) != 1 || f.closed[0] != (activities.HumanGateClose{StepRunID: o.StepRunID, Status: domain.ApprovalApproved, By: "web", Comment: "diff looks right"}) {
		t.Errorf("closed = %+v", f.closed)
	}
}

func TestHumanGateRejectedFailsTheRun(t *testing.T) {
	f := newInterpretFixture(t)
	f.decide(t, time.Minute, 0, false, "touches the network policy")
	out := f.run(t, loopSnapshot(t, approvalDefinition))
	if out.Succeeded || out.Reason != "step approve: rejected by web: touches the network policy" {
		t.Fatalf("result = %+v", out)
	}
	if f.closed[0].Status != domain.ApprovalRejected {
		t.Errorf("closed = %+v", f.closed)
	}
}

func TestHumanGateTimeout(t *testing.T) {
	t.Run("fail", func(t *testing.T) {
		f := newInterpretFixture(t)
		out := f.run(t, loopSnapshot(t, approvalDefinition))
		if out.Succeeded || out.Reason != "step approve: no human decision within 1h0m0s" || f.closed[0].Status != domain.ApprovalTimedOut {
			t.Fatalf("result = %+v, closed %+v", out, f.closed)
		}
	})
	t.Run("extend", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.decide(t, 150*time.Minute, 0, true, "")
		out := f.run(t, loopSnapshot(t, strings.Replace(approvalDefinition, "timeout: 1h}", "timeout: 1h, onTimeout: extend}", 1)))
		if !out.Succeeded || len(f.extended) != 2 || f.closed[0].Status != domain.ApprovalApproved {
			t.Fatalf("result = %+v, extended %v, closed %+v", out, f.extended, f.closed)
		}
	})
}

// A decision meant for another gate (stale, or sent twice) does not decide this one.
func TestHumanGateIgnoresOtherGatesDecisions(t *testing.T) {
	f := newInterpretFixture(t)
	f.env.RegisterDelayedCallback(func() {
		f.env.SignalWorkflow(HumanDecisionSignal, HumanDecision{StepRunID: "some-earlier-gate", Approved: true, By: "web"})
	}, time.Minute)
	f.decide(t, 2*time.Minute, 0, false, "no")
	out := f.run(t, loopSnapshot(t, approvalDefinition))
	if out.Succeeded || !strings.Contains(out.Reason, "rejected by web") {
		t.Fatalf("result = %+v", out)
	}
}

// onFail: escalate asks a human; approving accepts the failed gate and continues.
func TestEscalationApprovedContinues(t *testing.T) {
	f := newInterpretFixture(t)
	f.gate = func(req activities.GateRequest) activities.GateOutcome {
		return activities.GateOutcome{Passed: req.StepID != "checks", Summary: "mise run test exited 1"}
	}
	f.decide(t, time.Minute, 0, true, "flaky test, known")
	out := f.run(t, loopSnapshot(t, strings.Replace(loopDefinition, "onFail: {goto: implement, maxLoops: 2}", "onFail: escalate", 1)))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 checks/1 checks-escalation/1 style/1 open/1" {
		t.Errorf("steps = %s", got)
	}
}

func TestEscalationRejectedFails(t *testing.T) {
	f := newInterpretFixture(t)
	f.gate = func(req activities.GateRequest) activities.GateOutcome {
		return activities.GateOutcome{Passed: req.StepID != "checks", Summary: "mise run test exited 1"}
	}
	f.decide(t, time.Minute, 0, false, "")
	out := f.run(t, loopSnapshot(t, strings.Replace(loopDefinition, "onFail: {goto: implement, maxLoops: 2}", "onFail: escalate", 1)))
	if out.Succeeded || out.Reason != "step checks: gate failed: mise run test exited 1; escalated to a human, rejected by web" {
		t.Fatalf("result = %+v", out)
	}
}

// Invariant 6: a change to protected paths, or to tests, waits for a human
// after the change request opened, even in a workflow without human gates.
func TestProtectedChangesForceAHumanGate(t *testing.T) {
	report := guard.Report{Protected: []guard.ProtectedChange{{Path: "mise.toml", Pattern: "mise.toml"}},
		Tests: []string{"deleted test file internal/server/routes_test.go"}}
	t.Run("approved", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.protect = func(activities.GuardRequest) guard.Report { return report }
		f.decide(t, time.Minute, 0, true, "toolchain bump is intended")
		out := f.run(t, defaultBuildSnapshot(t))
		if !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		// M6 (D88): the forced gate comes before the merge queue.
		if got := f.stepIDs(); got != "implement/1 checks/1 code-reviewer/1 review/1 open-change-request/1 protected-changes/1 merge/1" {
			t.Errorf("steps = %s", got)
		}
		if len(f.guarded) != 1 || len(f.enqueued) != 1 {
			t.Errorf("guarded %d times, enqueued %d times; want once each", len(f.guarded), len(f.enqueued))
		}
		o := f.opened[0]
		if o.StepID != "protected-changes" || !strings.Contains(o.Reason, "mise.toml") || !strings.Contains(o.Reason, "deleted test file") ||
			o.ChangeRequestURL == "" || o.CommitSHA != "sha1" {
			t.Errorf("opened = %+v", o)
		}
		g := f.guarded[0]
		if g.BaseSHA != "base" || g.HeadSHA != "sha1" || !slices.Equal(g.Patterns, config.DefaultProtectedPaths) {
			t.Errorf("guard request = %+v", g)
		}
	})
	t.Run("rejected", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.protect = func(activities.GuardRequest) guard.Report { return report }
		f.decide(t, time.Minute, 0, false, "do not touch the toolchain")
		out := f.run(t, defaultBuildSnapshot(t))
		if out.Succeeded || out.Reason != "step protected-changes: rejected by web: do not touch the toolchain" {
			t.Fatalf("result = %+v", out)
		}
		if len(f.enqueued) != 0 {
			t.Errorf("a rejected protected change was enqueued: %+v", f.enqueued)
		}
		// M6 (D92): rejecting before the merge closes the change request.
		if len(f.closedCR) != 1 || f.closedCR[0] != "Rejected in Frankie by web: do not touch the toolchain" {
			t.Errorf("closed = %v", f.closedCR)
		}
	})
	t.Run("the snapshot's patterns are used", func(t *testing.T) {
		f := newInterpretFixture(t)
		snap := defaultBuildSnapshot(t)
		snap.ProtectedPaths = config.ProtectedPatterns([]string{"cmd/runner/**"})
		f.run(t, snap)
		if len(f.guarded) != 1 || !slices.Contains(f.guarded[0].Patterns, "cmd/runner/**") || len(f.opened) != 0 {
			t.Errorf("guarded %+v, opened %+v", f.guarded, f.opened)
		}
	})
	t.Run("a failed run is not inspected", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.push = func(int) bool { return false }
		f.protect = func(activities.GuardRequest) guard.Report { return report }
		if out := f.run(t, defaultBuildSnapshot(t)); out.Succeeded || len(f.guarded) != 0 {
			t.Errorf("result = %+v, guarded %+v", out, f.guarded)
		}
	})
}

// M6 (D92): a project's merge approval asks a person before the merge queue;
// approving merges, requesting changes sends the change back to the coder,
// rejecting closes the change request.
func TestMergeApproval(t *testing.T) {
	snap := func(t *testing.T) snapshot.Snapshot {
		s := defaultBuildSnapshot(t)
		s.MergeApproval = true
		return s
	}
	t.Run("approved", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.decide(t, time.Minute, 0, true, "")
		out := f.run(t, snap(t))
		if !out.Merged || len(f.opened) != 1 || f.opened[0].StepID != "merge-approval" || !f.opened[0].CanRequestChanges ||
			f.opened[0].ChangeRequestURL == "" {
			t.Fatalf("result %+v, opened %+v", out, f.opened)
		}
		if got := f.stepIDs(); got != "implement/1 checks/1 code-reviewer/1 review/1 open-change-request/1 merge-approval/1 merge/1" {
			t.Errorf("steps = %s", got)
		}
	})
	t.Run("changes requested", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.requestChanges(t, time.Minute, 0, "keep the old route as well")
		f.decide(t, 3*time.Hour, 1, true, "")
		out := f.run(t, snap(t))
		if !out.Merged {
			t.Fatalf("result = %+v", out)
		}
		want := "implement/1 checks/1 code-reviewer/1 review/1 open-change-request/1 merge-approval/1 " +
			"implement/2 checks/2 code-reviewer/2 review/2 open-change-request/2 merge-approval/2 merge/1"
		if got := f.stepIDs(); got != want {
			t.Errorf("steps = %s\nwant    %s", got, want)
		}
		if fb := f.sessions[2].Step.Feedback; !strings.Contains(fb, "ana reviewed your change request") || !strings.Contains(fb, "keep the old route as well") {
			t.Errorf("coder feedback = %q", fb)
		}
		if f.left != 0 || strings.Join(f.loops, ",") != "merge=1" || len(f.closedCR) != 0 {
			t.Errorf("left %d, loops %v, closed %v", f.left, f.loops, f.closedCR)
		}
	})
	t.Run("rejected", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.decide(t, time.Minute, 0, false, "not needed")
		out := f.run(t, snap(t))
		if out.Succeeded || out.Reason != "step merge-approval: rejected by web: not needed" || len(f.enqueued) != 0 {
			t.Fatalf("result %+v, enqueued %d", out, len(f.enqueued))
		}
		if len(f.closedCR) != 1 || f.closedCR[0] != "Rejected in Frankie by web: not needed" {
			t.Errorf("closed = %v", f.closedCR)
		}
	})
	t.Run("an approved protected change is not asked twice", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.protect = func(activities.GuardRequest) guard.Report {
			return guard.Report{Protected: []guard.ProtectedChange{{Path: "mise.toml", Pattern: "mise.toml"}}}
		}
		f.decide(t, time.Minute, 0, true, "")
		if out := f.run(t, snap(t)); !out.Merged || len(f.opened) != 1 || f.opened[0].StepID != "protected-changes" || !f.opened[0].CanRequestChanges {
			t.Fatalf("result %+v, opened %+v", out, f.opened)
		}
	})
	t.Run("off: straight to the queue", func(t *testing.T) {
		f := newInterpretFixture(t)
		if out := f.run(t, defaultBuildSnapshot(t)); !out.Merged || len(f.opened) != 0 {
			t.Fatalf("result %+v, opened %+v", out, f.opened)
		}
	})
	t.Run("a definition's own human gate cannot be sent back", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.requestChanges(t, time.Minute, 0, "hmm")
		out := f.run(t, loopSnapshot(t, approvalDefinition))
		if out.Succeeded || !strings.Contains(out.Reason, "rejected by ana: hmm") || f.opened[0].CanRequestChanges {
			t.Fatalf("result %+v, opened %+v", out, f.opened)
		}
	})
}

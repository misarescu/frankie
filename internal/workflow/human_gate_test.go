package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
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

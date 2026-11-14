package workflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/guard"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/workflow/activities"
	"github.com/misarescu/frankie/roles"
	"github.com/misarescu/frankie/workflows"
)

// loopDefinition is a definition the M3 interpreter runs end to end.
const loopDefinition = `
name: fast
version: 2
steps:
  - id: implement
    agent: {role: coder, model: anthropic/claude-opus-5-5}
    timeout: 45m
  - id: checks
    gate: {type: command, run: ["mise run test"], onFail: {goto: implement, maxLoops: 2}}
  - id: style
    gate: {type: command, run: ["mise run lint"], policy: advisory}
  - id: open
    action: change-request.open
`

func loopSnapshot(t *testing.T, def string) snapshot.Snapshot {
	t.Helper()
	w, err := config.ParseWorkflow([]byte(def))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := config.ResolveRoles(w, roles.Get)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Snapshot{Workflow: w, Roles: rs, Source: ".factory/workflows/fast.yaml", BaseSHA: "base"}
}

// interpretFixture records what the interpreter asked its Activities to do.
type interpretFixture struct {
	env      *testsuite.TestWorkflowEnvironment
	steps    []activities.StepStart
	ended    map[string]bool // step run ID -> succeeded
	sessions []activities.StartRunResult
	gates    []activities.GateRequest
	loops    []string
	finished *activities.FinishInput
	// gate decides each gate's outcome; nil passes everything.
	gate func(req activities.GateRequest) activities.GateOutcome
	// push decides whether the n-th agent session pushes (default true).
	push func(n int) bool
	head string // the branch head after the last push
	// verdict decides an agent-verdict gate; nil passes.
	verdict  func(req activities.VerdictGateRequest) activities.GateOutcome
	verdicts []activities.VerdictGateRequest
	guarded  []activities.GuardRequest
	// protect decides what the change inspection finds; nil finds nothing.
	protect  func(req activities.GuardRequest) guard.Report
	opened   []activities.HumanGateRequest
	closed   []activities.HumanGateClose
	extended []string
	// sessionTime is how long each agent session takes; sessionStatus how it
	// ends (default succeeded).
	sessionTime   time.Duration
	sessionStatus string
	interventions []activities.Intervention
}

func newInterpretFixture(t *testing.T, opts ...func(*interpretFixture)) *interpretFixture {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	f := &interpretFixture{env: s.NewTestWorkflowEnvironment(), ended: map[string]bool{}}
	for _, o := range opts {
		o(f)
	}
	a := &activities.Activities{}
	f.env.RegisterActivity(a)
	f.env.OnActivity(a.StartWorkflowRun, mock.Anything, testInput, mock.Anything).Return(
		activities.StartRunResult{RunID: testRun.RunID, Branch: testRun.Branch}, nil)
	f.env.OnActivity(a.StartStep, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.StepStart) (string, error) {
		f.steps = append(f.steps, in)
		return fmt.Sprintf("sr-%s-%d", in.StepID, in.Attempt), nil
	})
	f.env.OnActivity(a.FinishStep, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.StepEnd) error {
		f.ended[in.StepRunID] = in.Succeeded
		return nil
	})
	f.env.OnActivity(a.EnsureToolchainImage, mock.Anything, testInput).Return(toolchainRef, nil)
	f.env.OnActivity(a.LaunchAgentPod, mock.Anything, testInput, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult) (activities.AgentPod, error) {
		return activities.AgentPod{Name: "pod-" + run.StepRunID, IP: "10.0.0.1"}, nil
	})
	f.env.OnActivity(a.RunAgentSession, mock.Anything, testInput, mock.Anything, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult, _ activities.AgentPod) (activities.SessionResult, error) {
		f.sessions = append(f.sessions, run)
		n := len(f.sessions)
		if run.Step.Contract != "" { // a reviewer checks out the head and pushes nothing
			return activities.SessionResult{Status: "succeeded", Branch: run.Branch, CommitSHA: f.head,
				Result: `{"verdict":"pass","findings":[]}`}, nil
		}
		if f.sessionStatus != "" {
			return activities.SessionResult{Status: f.sessionStatus, Reason: "stopped"}, nil
		}
		pushed := f.push == nil || f.push(n)
		if pushed {
			f.head = fmt.Sprintf("sha%d", n)
		}
		return activities.SessionResult{Status: "succeeded", Branch: run.Branch, CommitSHA: fmt.Sprintf("sha%d", n), Pushed: pushed}, nil
	}).After(f.sessionTime)
	f.env.OnActivity(a.InterveneSession, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _ activities.AgentPod, in activities.Intervention) (bool, error) {
		f.interventions = append(f.interventions, in)
		return true, nil
	})
	f.env.OnActivity(a.RunVerdictGate, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.VerdictGateRequest) (activities.GateOutcome, error) {
		f.verdicts = append(f.verdicts, req)
		if f.verdict != nil {
			return f.verdict(req), nil
		}
		return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "code-reviewer: pass"}, nil
	})
	f.env.OnActivity(a.DeleteAgentPod, mock.Anything, mock.Anything).Return(nil)
	f.env.OnActivity(a.RunGate, mock.Anything, testInput, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, req activities.GateRequest) (activities.GateOutcome, error) {
		f.gates = append(f.gates, req)
		if f.gate != nil {
			return f.gate(req), nil
		}
		return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "all checks passed"}, nil
	})
	f.env.OnActivity(a.RecordLoop, mock.Anything, testRun.RunID, mock.Anything, mock.Anything).Return(func(_ context.Context, _, gate string, loops int) error {
		f.loops = append(f.loops, fmt.Sprintf("%s=%d", gate, loops))
		return nil
	})
	f.env.OnActivity(a.DetectProtectedChanges, mock.Anything, testInput, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, req activities.GuardRequest) (guard.Report, error) {
		f.guarded = append(f.guarded, req)
		if f.protect != nil {
			return f.protect(req), nil
		}
		return guard.Report{}, nil
	})
	f.env.OnActivity(a.OpenHumanGate, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.HumanGateRequest) error {
		f.opened = append(f.opened, req)
		return nil
	})
	f.env.OnActivity(a.CloseHumanGate, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.HumanGateClose) error {
		f.closed = append(f.closed, in)
		return nil
	})
	f.env.OnActivity(a.ExtendHumanGate, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, id string, _ time.Time) error {
		f.extended = append(f.extended, id)
		return nil
	})
	f.env.OnActivity(a.OpenChangeRequest, mock.Anything, testInput, mock.Anything, mock.Anything).Return("https://github.com/acme/app/pull/9", nil)
	f.env.OnActivity(a.FinishRun, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.FinishInput) error {
		f.finished = &in
		return nil
	})
	return f
}

func (f *interpretFixture) run(t *testing.T, snap snapshot.Snapshot) IssueRunResult {
	t.Helper()
	f.env.ExecuteWorkflow(Interpret, InterpretInput{Issue: testInput, Snapshot: snap})
	if !f.env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := f.env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var out IssueRunResult
	if err := f.env.GetWorkflowResult(&out); err != nil {
		t.Fatal(err)
	}
	if f.finished == nil || f.finished.Succeeded != out.Succeeded || f.finished.Reason != out.Reason || f.finished.StepRunID != "" {
		t.Fatalf("FinishRun = %+v, result %+v", f.finished, out)
	}
	return out
}

func (f *interpretFixture) stepIDs() string {
	var ids []string
	for _, s := range f.steps {
		ids = append(ids, fmt.Sprintf("%s/%d", s.StepID, s.Attempt))
	}
	return strings.Join(ids, " ")
}

func TestInterpretRunsTheDefinition(t *testing.T) {
	f := newInterpretFixture(t)
	out := f.run(t, loopSnapshot(t, loopDefinition))
	if !out.Succeeded || out.ChangeRequestURL != "https://github.com/acme/app/pull/9" {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 checks/1 style/1 open/1" {
		t.Errorf("steps = %s", got)
	}
	if !f.ended["sr-implement-1"] || !f.ended["sr-open-1"] {
		t.Errorf("ended = %v", f.ended)
	}

	t.Run("agent step gets its role, model and timeout from the snapshot", func(t *testing.T) {
		s := f.sessions[0]
		if s.Step == nil || s.Step.StepID != "implement" || s.Step.Role != "coder" || s.Step.Model != "claude-opus-5-5" ||
			s.Step.Timeout.Minutes() != 45 || !strings.Contains(s.Step.SystemPrompt, "coding agent") || s.ToolchainImage != toolchainRef {
			t.Errorf("session run = %+v step %+v", s, s.Step)
		}
	})
	t.Run("gates check the pushed commit with the definition's commands", func(t *testing.T) {
		if len(f.gates) != 2 || f.gates[0].CommitSHA != "sha1" || f.gates[0].Commands[0] != "mise run test" || f.gates[1].StepID != "style" {
			t.Errorf("gates = %+v", f.gates)
		}
	})
}

// M3 acceptance: a failed gate loops back to implement with what it found.
func TestInterpretLoopsBackWithFeedback(t *testing.T) {
	f := newInterpretFixture(t)
	f.gate = func(req activities.GateRequest) activities.GateOutcome {
		if req.StepID == "checks" && req.CommitSHA == "sha1" {
			return activities.GateOutcome{Summary: "mise run test exited 1", Feedback: "`mise run test` exited 1.\n--- FAIL: TestSearch"}
		}
		return activities.GateOutcome{Passed: true}
	}
	out := f.run(t, loopSnapshot(t, loopDefinition))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 checks/1 implement/2 checks/2 style/1 open/1" {
		t.Errorf("steps = %s", got)
	}
	if len(f.loops) != 1 || f.loops[0] != "checks=1" {
		t.Errorf("loops recorded = %v", f.loops)
	}
	if fb := f.sessions[0].Step.Feedback; fb != "" {
		t.Errorf("first attempt got feedback %q", fb)
	}
	if fb := f.sessions[1].Step.Feedback; !strings.Contains(fb, "`checks` check failed") || !strings.Contains(fb, "TestSearch") {
		t.Errorf("second attempt feedback = %q", fb)
	}
	if f.gates[1].CommitSHA != "sha2" {
		t.Errorf("the second gate checked %s, want the new commit", f.gates[1].CommitSHA)
	}
}

// FR-WF-4: a loop that used up maxLoops escalates to a human; nobody deciding
// within the default timeout fails the run.
func TestInterpretEscalatesAtMaxLoops(t *testing.T) {
	f := newInterpretFixture(t)
	f.gate = func(req activities.GateRequest) activities.GateOutcome {
		return activities.GateOutcome{Passed: req.StepID != "checks", Summary: "mise run test exited 1"}
	}
	out := f.run(t, loopSnapshot(t, loopDefinition))
	if out.Succeeded || out.Reason != "step checks: gate failed: mise run test exited 1 (looped back to implement 2 times, the limit); "+
		"escalated to a human, no human decision within 72h0m0s" {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 checks/1 implement/2 checks/2 implement/3 checks/3 checks-escalation/1" {
		t.Errorf("steps = %s", got)
	}
	if len(f.opened) != 1 || !strings.Contains(f.opened[0].Reason, "Approve to accept the change anyway") || f.opened[0].CommitSHA != "sha3" ||
		len(f.closed) != 1 || f.closed[0].Status != domain.ApprovalTimedOut {
		t.Errorf("opened %+v, closed %+v", f.opened, f.closed)
	}
	if strings.Join(f.loops, ",") != "checks=1,checks=2" {
		t.Errorf("loops = %v", f.loops)
	}
}

func TestInterpretFailures(t *testing.T) {
	t.Run("a failing gate with onFail fail ends the run", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.gate = func(activities.GateRequest) activities.GateOutcome {
			return activities.GateOutcome{Summary: "mise run test exited 2"}
		}
		out := f.run(t, loopSnapshot(t, strings.Replace(loopDefinition, "onFail: {goto: implement, maxLoops: 2}", "onFail: fail", 1)))
		if out.Succeeded || out.Reason != "step checks: gate failed: mise run test exited 2" || f.stepIDs() != "implement/1 checks/1" {
			t.Fatalf("result = %+v, steps %s", out, f.stepIDs())
		}
	})
	t.Run("an agent that pushes nothing fails before any gate", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.push = func(int) bool { return false }
		out := f.run(t, loopSnapshot(t, loopDefinition))
		if out.Succeeded || out.Reason != "step implement: agent session made no changes" || len(f.gates) != 0 || f.ended["sr-implement-1"] {
			t.Fatalf("result = %+v, gates %v", out, f.gates)
		}
	})
	t.Run("an invalid configuration fails the run without steps", func(t *testing.T) {
		f := newInterpretFixture(t)
		const problem = "snapshot: invalid project configuration: workflow \"nope\" is neither in .factory/workflows nor built in"
		out := f.run(t, snapshot.Snapshot{Error: problem})
		if out.Succeeded || out.Reason != problem || len(f.steps) != 0 {
			t.Fatalf("result = %+v, steps %s", out, f.stepIDs())
		}
	})
}

func TestCheckSupported(t *testing.T) {
	if err := CheckSupported(loopSnapshot(t, loopDefinition).Workflow); err != nil {
		t.Fatalf("loop definition: %v", err)
	}
	def, err := workflows.Get(workflows.Default)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSupported(def); err != nil {
		t.Errorf("default-build: %v", err)
	}
	escalating := loopSnapshot(t, strings.Replace(loopDefinition, "onFail: {goto: implement, maxLoops: 2}", "onFail: escalate", 1))
	human := loopSnapshot(t, strings.Replace(loopDefinition, "  - id: open\n", "  - id: approve\n    gate: {type: human}\n  - id: open\n", 1))
	for _, w := range []config.Workflow{escalating.Workflow, human.Workflow} {
		if err := CheckSupported(w); err != nil {
			t.Errorf("%s: %v", w.Name, err)
		}
	}
	unsupported := config.Workflow{Name: "x", Steps: []config.Step{{ID: "ship", Action: "release.create"}}}
	if err := CheckSupported(unsupported); err == nil {
		t.Error("an unimplemented action is supported")
	}
}

// interpretHistories are recorded histories of Interpret (record_test.go), oldest
// first. Each must keep replaying (invariant 3, FR-WF-6); a failure means a
// change broke determinism and needs workflow.GetVersion.
var interpretHistories = []struct{ file, what string }{
	{"testdata/interpret_loop_then_success.json", "M3: command gate fails, loop back to implement, change request"},
	{"testdata/interpret_loops_exhausted.json", "M3: command gate fails until maxLoops is used up"},
	{"testdata/interpret_default_build_review_loop.json", "M3: default-build, reviewer fails, loop back to implement"},
	{"testdata/interpret_human_gate_approved.json", "M3: human gate after the change request, approved by signal"},
	{"testdata/interpret_loops_exhausted_escalated.json", "M3: escalate-exhausted-loops version, escalation approved"},
	{"testdata/interpret_protected_changes_approved.json", "M3: protected-changes version, forced human gate approved"},
	{"testdata/interpret_steer_then_abort.json", "M4: step-interventions version, a steer then an abort fail the run"},
}

func TestInterpretReplaysRecordedHistories(t *testing.T) {
	for _, h := range interpretHistories {
		t.Run(h.what, func(t *testing.T) {
			r := worker.NewWorkflowReplayer()
			r.RegisterWorkflow(Interpret)
			if err := r.ReplayWorkflowHistoryFromJSONFile(nil, h.file); err != nil {
				t.Fatalf("replay %s: %v", h.file, err)
			}
		})
	}
}

func defaultBuildSnapshot(t *testing.T) snapshot.Snapshot {
	t.Helper()
	w, err := workflows.Get(workflows.Default)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := config.ResolveRoles(w, roles.Get)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Snapshot{Workflow: w, Roles: rs, Source: "built-in", BaseSHA: "base"}
}

// M3 acceptance: default-build runs end to end, the reviewer reading the
// commit the checks passed.
func TestInterpretDefaultBuild(t *testing.T) {
	f := newInterpretFixture(t)
	out := f.run(t, defaultBuildSnapshot(t))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 checks/1 code-reviewer/1 review/1 open-change-request/1" {
		t.Errorf("steps = %s", got)
	}
	rev := f.sessions[1].Step
	if rev.StepID != "code-reviewer" || rev.Contract != "review-verdict" || !slices.Contains(rev.AllowedTools, "bash:readonly") || rev.Feedback != "" {
		t.Errorf("reviewer step = %+v", rev)
	}
	if coder := f.sessions[0].Step; coder.Contract != "" || len(coder.AllowedTools) != 0 {
		t.Errorf("coder step = %+v", coder)
	}
	if len(f.verdicts) != 1 {
		t.Fatalf("verdict gates = %d", len(f.verdicts))
	}
	v := f.verdicts[0]
	if v.StepID != "review" || v.Require != "all" || v.CommitSHA != "sha1" || len(v.Reviews) != 1 ||
		v.Reviews[0].CommitSHA != "sha1" || !strings.Contains(v.Reviews[0].Result, `"verdict":"pass"`) {
		t.Errorf("verdict request = %+v", v)
	}
	if !f.ended["sr-code-reviewer-1"] {
		t.Errorf("reviewer step not ended as succeeded: %v", f.ended)
	}
	if len(f.guarded) != 1 || len(f.opened) != 0 {
		t.Errorf("a clean change is inspected once and needs no human: guarded %+v, opened %+v", f.guarded, f.opened)
	}
}

// M3 acceptance: a reviewer-triggered loop back to implement.
func TestInterpretReviewLoopsBackToImplement(t *testing.T) {
	f := newInterpretFixture(t)
	f.verdict = func(req activities.VerdictGateRequest) activities.GateOutcome {
		if req.CommitSHA == "sha1" {
			return activities.GateOutcome{Summary: "code-reviewer: fail (1 blocker)",
				Feedback: "Review by code-reviewer:\n- [blocker] internal/server/version.go:12: the encode error is dropped\n"}
		}
		return activities.GateOutcome{Passed: true}
	}
	out := f.run(t, defaultBuildSnapshot(t))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	want := "implement/1 checks/1 code-reviewer/1 review/1 implement/2 checks/2 code-reviewer/2 review/2 open-change-request/1"
	if got := f.stepIDs(); got != want {
		t.Errorf("steps = %s\nwant    %s", got, want)
	}
	if strings.Join(f.loops, ",") != "review=1" {
		t.Errorf("loops = %v", f.loops)
	}
	second := f.sessions[2].Step
	if second.StepID != "implement" || !strings.Contains(second.Feedback, "`review` check failed") || !strings.Contains(second.Feedback, "encode error is dropped") {
		t.Errorf("second implement step = %+v", second)
	}
	if f.verdicts[1].CommitSHA != "sha3" {
		t.Errorf("second review judged %s, want the new commit", f.verdicts[1].CommitSHA)
	}
}

func TestInterpretInconclusiveReviewFailsTheRun(t *testing.T) {
	f := newInterpretFixture(t)
	f.verdict = func(activities.VerdictGateRequest) activities.GateOutcome {
		return activities.GateOutcome{Inconclusive: true, Summary: "code-reviewer returned no valid verdict (the final message holds no JSON verdict)"}
	}
	out := f.run(t, defaultBuildSnapshot(t))
	if out.Succeeded || !strings.HasPrefix(out.Reason, "step review: gate inconclusive: code-reviewer returned no valid verdict") || len(f.loops) != 0 {
		t.Fatalf("result = %+v, loops %v", out, f.loops)
	}
}

// A loop back to a branch re-runs only that branch, then continues after its group.
func TestInterpretGotoIntoABranch(t *testing.T) {
	const def = `
name: split
version: 1
steps:
  - parallel:
      - id: implement
        agent: {role: coder}
      - id: docs
        agent: {role: coder}
        inputs: [spec]
  - id: checks
    gate: {type: command, onFail: {goto: implement, maxLoops: 1}}
  - id: open
    action: change-request.open
`
	f := newInterpretFixture(t)
	f.gate = func(req activities.GateRequest) activities.GateOutcome {
		return activities.GateOutcome{Passed: req.CommitSHA != "sha2", Summary: "mise run test exited 1"}
	}
	out := f.run(t, loopSnapshot(t, def))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 docs/1 checks/1 implement/2 checks/2 open/1" {
		t.Errorf("steps = %s", got)
	}
}

// D53: every agent session gets the snapshot's issue budget.
func TestAgentStepsCarryTheIssueBudget(t *testing.T) {
	f := newInterpretFixture(t)
	snap := loopSnapshot(t, loopDefinition)
	snap.IssueBudgetUSD = 7.5
	if out := f.run(t, snap); !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	for _, s := range f.sessions {
		if s.Step.IssueBudgetUSD != 7.5 {
			t.Errorf("session %s: issue budget %v", s.Step.StepID, s.Step.IssueBudgetUSD)
		}
	}
}

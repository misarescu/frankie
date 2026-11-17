package workflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
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
	// enqueued records the enqueue requests; queue answers each with an
	// outcome signal an hour later (nil: merged). stale, when set, is
	// signaled first, for an earlier execution of the step.
	enqueued []activities.EnqueueRequest
	queue    func(req activities.EnqueueRequest) QueueOutcome
	stale    *QueueOutcome
	left     int // LeaveQueue calls
	// closedCR records CloseChangeRequest comments.
	closedCR []string
	// findings is what an investigating session answers; findingsFor, when
	// set, answers per investigation (from 1).
	findings    string
	findingsFor func(n int) string
	// preserved records preserve gate requests; preserve decides them (nil passes).
	preserved []activities.PreserveRequest
	preserve  func(req activities.PreserveRequest) activities.GateOutcome
	// mu guards what Activities record: a group of writers runs them at
	// the same time (D122).
	mu sync.Mutex
	// mainSessions counts the sessions on the issue's branch, which name
	// their commits sha<n>; a side writer's commit is side-<step run>.
	mainSessions int
	// statusFor, when set, says how a session ends ("" succeeds).
	statusFor func(run activities.StartRunResult) string
	// updates records branch updates; update decides them (nil: updated
	// to merged-<n>).
	updates []activities.BranchUpdateRequest
	update  func(req activities.BranchUpdateRequest) activities.BranchUpdateResult
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
		f.mu.Lock()
		defer f.mu.Unlock()
		f.steps = append(f.steps, in)
		return fmt.Sprintf("sr-%s-%d", in.StepID, in.Attempt), nil
	})
	f.env.OnActivity(a.FinishStep, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.StepEnd) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.ended[in.StepRunID] = in.Succeeded
		return nil
	})
	f.env.OnActivity(a.EnsureToolchainImage, mock.Anything, testInput).Return(toolchainRef, nil)
	f.env.OnActivity(a.EnsureStepImage, mock.Anything, testInput, testRun.RunID, mock.Anything).Return(toolchainRef, nil)
	f.env.OnActivity(a.LaunchAgentPod, mock.Anything, testInput, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult) (activities.AgentPod, error) {
		return activities.AgentPod{Name: "pod-" + run.StepRunID, IP: "10.0.0.1"}, nil
	})
	f.env.OnActivity(a.RunAgentSession, mock.Anything, testInput, mock.Anything, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult, _ activities.AgentPod) (activities.SessionResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sessions = append(f.sessions, run)
		if f.statusFor != nil {
			if st := f.statusFor(run); st != "" {
				return activities.SessionResult{Status: st, Reason: "stopped"}, nil
			}
		}
		if f.sessionStatus != "" {
			return activities.SessionResult{Status: f.sessionStatus, Reason: "stopped"}, nil
		}
		if run.Branch != testRun.Branch { // a side writer (D122)
			return activities.SessionResult{Status: "succeeded", Branch: run.Branch, CommitSHA: "side-" + run.StepRunID, Pushed: true}, nil
		}
		f.mainSessions++
		n := f.mainSessions
		if run.Step.Contract == config.ContractBugFindings { // an investigator reads and pushes nothing
			result := f.findings
			if f.findingsFor != nil {
				investigations := 0
				for _, s := range f.sessions {
					if s.Step.Contract == config.ContractBugFindings {
						investigations++
					}
				}
				result = f.findingsFor(investigations)
			}
			return activities.SessionResult{Status: "succeeded", Branch: run.Branch, Result: result}, nil
		}
		if run.Step.Contract != "" { // a reviewer checks out the head and pushes nothing
			return activities.SessionResult{Status: "succeeded", Branch: run.Branch, CommitSHA: f.head,
				Result: `{"verdict":"pass","findings":[]}`}, nil
		}
		pushed := f.push == nil || f.push(n)
		if pushed {
			f.head = fmt.Sprintf("sha%d", n)
		}
		return activities.SessionResult{Status: "succeeded", Branch: run.Branch, CommitSHA: fmt.Sprintf("sha%d", n), Pushed: pushed}, nil
	}).After(f.sessionTime)
	f.env.OnActivity(a.UpdateBranch, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.BranchUpdateRequest) (activities.BranchUpdateResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.updates = append(f.updates, req)
		if f.update != nil {
			return f.update(req), nil
		}
		f.head = fmt.Sprintf("merged-%d", len(f.updates))
		return activities.BranchUpdateResult{Outcome: activities.BranchUpdated, HeadSHA: f.head}, nil
	})
	f.env.OnActivity(a.InterveneSession, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _ activities.AgentPod, in activities.Intervention) (bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
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
	f.env.OnActivity(a.EnqueueChangeRequest, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.EnqueueRequest) error {
		f.enqueued = append(f.enqueued, req)
		out := QueueOutcome{Kind: QueueOutcomeMerged, MergeSHA: "m3rge"}
		if f.queue != nil {
			out = f.queue(req)
		}
		out.StepID, out.Attempt = req.StepID, req.Attempt
		if f.stale != nil {
			stale := *f.stale
			f.env.RegisterDelayedCallback(func() { f.env.SignalWorkflow(MergeQueueOutcomeSignal, stale) }, 30*time.Minute)
		}
		f.env.RegisterDelayedCallback(func() { f.env.SignalWorkflow(MergeQueueOutcomeSignal, out) }, time.Hour)
		return nil
	})
	f.env.OnActivity(a.RecordFindings, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.FindingsRequest) (activities.FindingsResult, error) {
		found, err := domain.ParseBugFindings([]byte(req.Result))
		if err != nil {
			return activities.FindingsResult{Error: err.Error()}, nil
		}
		return activities.FindingsResult{Findings: found}, nil
	})
	f.env.OnActivity(a.CheckPreserved, mock.Anything, testInput, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, req activities.PreserveRequest) (activities.GateOutcome, error) {
		f.preserved = append(f.preserved, req)
		if f.preserve != nil {
			return f.preserve(req), nil
		}
		return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true}, nil
	})
	f.env.OnActivity(a.CloseChangeRequest, mock.Anything, testInput, mock.Anything, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, _, comment string) error {
		f.closedCR = append(f.closedCR, comment)
		return nil
	})
	f.env.OnActivity(a.LeaveQueue, mock.Anything, testInput.IssueID).Return(func(context.Context, string) error {
		f.left++
		return nil
	})
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
	// M7 (D126): until the interpreter runs it, a preview gate fails the run
	// at start rather than midway.
	preview := config.Workflow{Name: "p", Steps: []config.Step{{ID: "preview", Gate: &config.Gate{Type: domain.GatePreview}}}}
	if err := CheckSupported(preview); err == nil || !strings.Contains(err.Error(), "the preview gate is not supported yet") {
		t.Errorf("preview gate: %v", err)
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
	{"testdata/interpret_question_answered.json", "M4: step-answers version, a question answered by signal"},
	{"testdata/interpret_default_build_merged.json", "M6: merge-queue version, protected change approved, then merged by the queue"},
	{"testdata/interpret_queue_conflict_then_merged.json", "M6: merge-queue-loop version, a conflict sent back to the coder, then merged"},
	{"testdata/interpret_merge_approval_changes_requested.json", "M6: merge-approval version, changes requested, then approved and merged"},
	{"testdata/interpret_image_build_events.json", "M6: image-build-events version, agent steps resolve their image as themselves"},
	{"testdata/interpret_investigate_testable.json", "M6: bug-findings version, a testable bug is investigated, reproduced and fixed"},
	{"testdata/interpret_bug_testable.json", "M6: bug-gates version, a test that must fail, then a fix that keeps it"},
	{"testdata/interpret_concurrent_writers.json", "M7: concurrent-writers version, two writers at once, the side branch merged, a loop back to the coder"},
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
	if got := f.stepIDs(); got != "implement/1 checks/1 code-reviewer/1 review/1 open-change-request/1 merge/1" {
		t.Errorf("steps = %s", got)
	}
	if !out.Merged || f.finished == nil || !f.finished.Merged || !f.ended["sr-merge-1"] {
		t.Errorf("result %+v, finished %+v: the queue merged, so the issue is merged", out, f.finished)
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
	want := "implement/1 checks/1 code-reviewer/1 review/1 implement/2 checks/2 code-reviewer/2 review/2 open-change-request/1 merge/1"
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

// M6 (D88): the run enqueues the commit its gates passed, with the
// definition's blocking command gates and the project's merge method, and
// waits for its own outcome; anything but a merge fails the run.
func TestInterpretEnqueue(t *testing.T) {
	t.Run("what the queue gets", func(t *testing.T) {
		f := newInterpretFixture(t)
		snap := defaultBuildSnapshot(t)
		snap.MergeMethod = domain.MergeRebase
		snap.CILogs = domain.CILogLimits{Checks: 1, Lines: 200} // M7 (D120)
		if out := f.run(t, snap); !out.Succeeded || !out.Merged {
			t.Fatalf("result = %+v", out)
		}
		req := f.enqueued[0]
		if req.StepID != "merge" || req.Attempt != 1 || req.HeadSHA != "sha1" || req.Branch != testRun.Branch || req.Method != domain.MergeRebase ||
			req.CILogs != snap.CILogs || req.StepRunID != "sr-merge-1" || req.RunID != testRun.RunID || req.WorkflowID == "" {
			t.Errorf("enqueue = %+v", req)
		}
		if len(req.Gates) != 1 || req.Gates[0].StepID != "checks" || strings.Join(req.Gates[0].Commands, ";") != "mise run lint;mise run test" {
			t.Errorf("gates = %+v; want the blocking command gate only", req.Gates)
		}
	})
	t.Run("squash unless the snapshot says otherwise", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.run(t, defaultBuildSnapshot(t))
		if f.enqueued[0].Method != domain.MergeSquash {
			t.Errorf("method = %q", f.enqueued[0].Method)
		}
	})
	t.Run("a stale outcome is ignored", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.stale = &QueueOutcome{StepID: "merge", Attempt: 0, Kind: QueueOutcomeConflict, Reason: "old"}
		if out := f.run(t, defaultBuildSnapshot(t)); !out.Succeeded || !out.Merged {
			t.Fatalf("result = %+v", out)
		}
	})
	t.Run("without onFail a conflict fails the run", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.queue = func(activities.EnqueueRequest) QueueOutcome {
			return QueueOutcome{Kind: QueueOutcomeConflict, Reason: "agent/gc-1/add-search conflicts with main", Conflicts: []string{"a.go"}}
		}
		snap := defaultBuildSnapshot(t)
		snap.Workflow.Steps[4].OnFail = &config.OnFail{Action: config.OnFailFail}
		out := f.run(t, snap)
		if out.Succeeded || out.Merged || out.Reason != "step merge: merge queue: conflict: agent/gc-1/add-search conflicts with main" {
			t.Fatalf("result = %+v", out)
		}
		if f.ended["sr-merge-1"] || f.finished.Merged || f.left != 0 {
			t.Errorf("merge step ended %v, finished %+v, left %d", f.ended, f.finished, f.left)
		}
	})
}

// M6 (D89) done-when: a change request that conflicts goes back to the coder
// with both sides, is checked and reviewed again, and merges on its second
// time through the queue.
func TestInterpretQueueSendsBackToTheCoder(t *testing.T) {
	f := newInterpretFixture(t)
	f.queue = func(req activities.EnqueueRequest) QueueOutcome {
		if req.Attempt == 1 {
			return QueueOutcome{Kind: QueueOutcomeConflict, Reason: "conflicts", Conflicts: []string{"web/App.jsx", "go.mod"},
				Feedback: "### web/App.jsx\n+import Shelves"}
		}
		return QueueOutcome{Kind: QueueOutcomeMerged, MergeSHA: "m3rge"}
	}
	out := f.run(t, defaultBuildSnapshot(t))
	if !out.Succeeded || !out.Merged {
		t.Fatalf("result = %+v", out)
	}
	want := "implement/1 checks/1 code-reviewer/1 review/1 open-change-request/1 merge/1 implement/2 checks/2 code-reviewer/2 review/2 open-change-request/2 merge/2"
	if got := f.stepIDs(); got != want {
		t.Errorf("steps = %s\nwant    %s", got, want)
	}
	fb := f.sessions[2].Step.Feedback
	for _, s := range []string{"conflicts with `main` in `web/App.jsx`, `go.mod`", "git merge origin/main", "keep both", "+import Shelves"} {
		if !strings.Contains(fb, s) {
			t.Errorf("coder feedback lacks %q:\n%s", s, fb)
		}
	}
	if f.left != 1 || strings.Join(f.loops, ",") != "merge=1" || len(f.enqueued) != 2 || f.enqueued[1].HeadSHA != "sha3" {
		t.Errorf("left %d, loops %v, enqueued %+v", f.left, f.loops, f.enqueued)
	}
}

func TestInterpretQueueSendBackLimits(t *testing.T) {
	t.Run("a red update goes back with the check's feedback", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.queue = func(req activities.EnqueueRequest) QueueOutcome {
			if req.Attempt == 1 {
				return QueueOutcome{Kind: QueueOutcomeRed, Reason: "the `checks` check failed", Feedback: "FAIL TestShelves", HeadSHA: "upd"}
			}
			return QueueOutcome{Kind: QueueOutcomeMerged}
		}
		if out := f.run(t, defaultBuildSnapshot(t)); !out.Merged {
			t.Fatalf("result = %+v", out)
		}
		if fb := f.sessions[2].Step.Feedback; !strings.Contains(fb, "now at upd") || !strings.Contains(fb, "FAIL TestShelves") {
			t.Errorf("feedback = %s", fb)
		}
	})
	t.Run("maxLoops ends it", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.queue = func(activities.EnqueueRequest) QueueOutcome {
			return QueueOutcome{Kind: QueueOutcomeConflict, Reason: "conflicts"}
		}
		out := f.run(t, defaultBuildSnapshot(t))
		if out.Succeeded || !strings.Contains(out.Reason, "sent back to implement 2 times, the limit") || len(f.enqueued) != 3 {
			t.Fatalf("result = %+v, enqueued %d", out, len(f.enqueued))
		}
	})
	t.Run("other outcomes do not loop", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.queue = func(activities.EnqueueRequest) QueueOutcome {
			return QueueOutcome{Kind: QueueOutcomeRefused, Reason: "branch protection"}
		}
		out := f.run(t, defaultBuildSnapshot(t))
		if out.Succeeded || out.Reason != "step merge: merge queue: refused: branch protection" || f.left != 0 {
			t.Fatalf("result = %+v", out)
		}
	})
}

// investigateDefinition investigates, reproduces only a testable bug, fixes.
const investigateDefinition = `
name: bugs
version: 1
steps:
  - id: investigate
    agent: {role: bug-investigator}
  - id: reproduce
    agent: {role: coder}
    when: {step: investigate, field: testable, equals: true}
  - id: fix
    agent: {role: coder}
  - id: open
    action: change-request.open
`

func investigateSnapshot(t *testing.T) snapshot.Snapshot {
	t.Helper()
	w, err := config.ParseWorkflow([]byte(investigateDefinition))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Snapshot{Workflow: w, BaseSHA: "base", Roles: map[string]config.Role{
		"bug-investigator": {Name: "bug-investigator", Version: 1, SystemPrompt: "find it", OutputContract: config.ContractBugFindings},
		"coder":            {Name: "coder", Version: 1, SystemPrompt: "code"},
	}}
}

const testFindings = `{"root_cause":"stats sum shelf rows","evidence":[{"file":"db/queries/shelf_books.sql","line":40,"note":"SUM"}],
"reproduce":"two shelves","testable":%t,"test_plan":"storage test","why_untestable":"timing","confidence":"high"}`

// M6 (D94): an investigating step's findings decide which steps run and
// reach the later steps' prompts and the change request.
func TestInterpretInvestigates(t *testing.T) {
	for _, testable := range []bool{true, false} {
		t.Run(fmt.Sprintf("testable=%v", testable), func(t *testing.T) {
			f := newInterpretFixture(t, func(f *interpretFixture) { f.findings = fmt.Sprintf(testFindings, testable) })
			out := f.run(t, investigateSnapshot(t))
			if !out.Succeeded {
				t.Fatalf("result = %+v", out)
			}
			want := "investigate/1 reproduce/1 fix/1 open/1"
			if !testable {
				want = "investigate/1 fix/1 open/1"
			}
			if got := f.stepIDs(); got != want {
				t.Errorf("steps = %s, want %s", got, want)
			}
			fix := f.sessions[len(f.sessions)-1].Step
			if !strings.Contains(fix.Findings, "**Root cause** (high confidence): stats sum shelf rows") {
				t.Errorf("fix step findings = %q", fix.Findings)
			}
			if inv := f.sessions[0].Step; inv.Contract != config.ContractBugFindings || inv.Findings != "" {
				t.Errorf("investigate step = %+v", inv)
			}
		})
	}
	t.Run("findings that are not valid fail the run", func(t *testing.T) {
		f := newInterpretFixture(t, func(f *interpretFixture) { f.findings = "I am not sure." })
		out := f.run(t, investigateSnapshot(t))
		if out.Succeeded || !strings.Contains(out.Reason, "step investigate: the investigation returned no valid findings") || len(f.sessions) != 1 {
			t.Fatalf("result = %+v, sessions %d", out, len(f.sessions))
		}
	})
}

// bugDefinition is the bug-fix shape (D95): a person decides on an untestable
// bug, a testable one gets a test that must fail, and the fix must keep it.
const bugDefinition = `
name: bugs
version: 1
steps:
  - id: investigate
    agent: {role: bug-investigator}
  - id: decide
    gate: {type: human, sendBack: {goto: investigate, maxLoops: 2}}
    when: {step: investigate, field: testable, equals: false}
  - id: reproduce
    agent: {role: coder}
    when: {step: investigate, field: testable, equals: true}
  - id: shows-bug
    gate: {type: command, run: ["mise run test"], expect: fail, onFail: {goto: reproduce, maxLoops: 2}}
    when: {step: investigate, field: testable, equals: true}
  - id: fix
    agent: {role: coder}
  - id: keeps-test
    gate: {type: preserve, step: reproduce, onFail: {goto: fix, maxLoops: 2}}
    when: {step: investigate, field: testable, equals: true}
  - id: checks
    gate: {type: command, onFail: {goto: fix, maxLoops: 2}}
  - id: open
    action: change-request.open
`

func bugSnapshot(t *testing.T) snapshot.Snapshot {
	t.Helper()
	s := investigateSnapshot(t)
	w, err := config.ParseWorkflow([]byte(bugDefinition))
	if err != nil {
		t.Fatal(err)
	}
	s.Workflow = w
	return s
}

// M6 (D95): a testable bug gets a test that must fail before the fix, and the
// fix must leave that test as it was.
func TestInterpretBugGates(t *testing.T) {
	t.Run("testable", func(t *testing.T) {
		f := newInterpretFixture(t, func(f *interpretFixture) { f.findings = fmt.Sprintf(testFindings, true) })
		f.gate = func(req activities.GateRequest) activities.GateOutcome {
			// The new test fails before the fix, as it must; checks pass after it.
			return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true}
		}
		out := f.run(t, bugSnapshot(t))
		if !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); got != "investigate/1 reproduce/1 shows-bug/1 fix/1 keeps-test/1 checks/1 open/1" {
			t.Errorf("steps = %s", got)
		}
		if g := f.gates[0]; g.StepID != "shows-bug" || !g.ExpectFail || g.CommitSHA != "sha2" || f.gates[1].ExpectFail {
			t.Errorf("gates = %+v", f.gates)
		}
		if len(f.preserved) != 1 || f.preserved[0] != (activities.PreserveRequest{RunID: testRun.RunID, StepRunID: "sr-keeps-test-1",
			Step: "reproduce", From: "base", To: "sha2", Head: "sha3"}) {
			t.Errorf("preserve = %+v", f.preserved)
		}
	})
	t.Run("the fix touched the test", func(t *testing.T) {
		f := newInterpretFixture(t, func(f *interpretFixture) { f.findings = fmt.Sprintf(testFindings, true) })
		f.preserve = func(req activities.PreserveRequest) activities.GateOutcome {
			if req.Head == "sha3" {
				return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "changed what reproduce wrote: a_test.go (modified)", Feedback: "Restore a_test.go"}
			}
			return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true}
		}
		if out := f.run(t, bugSnapshot(t)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); !strings.Contains(got, "keeps-test/1 fix/2 keeps-test/2 checks/1") {
			t.Errorf("steps = %s", got)
		}
		if fb := f.sessions[3].Step.Feedback; !strings.Contains(fb, "Restore a_test.go") {
			t.Errorf("second fix feedback = %q", fb)
		}
	})
	t.Run("the test does not fail", func(t *testing.T) {
		f := newInterpretFixture(t, func(f *interpretFixture) { f.findings = fmt.Sprintf(testFindings, true) })
		f.gate = func(req activities.GateRequest) activities.GateOutcome {
			if req.ExpectFail && req.CommitSHA == "sha2" {
				return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "every command passed, but this gate expects them to fail", Feedback: "make it fail"}
			}
			return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true}
		}
		if out := f.run(t, bugSnapshot(t)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); !strings.HasPrefix(got, "investigate/1 reproduce/1 shows-bug/1 reproduce/2 shows-bug/2 fix/1") {
			t.Errorf("steps = %s", got)
		}
	})
	t.Run("untestable: a person sends the investigation back, then approves", func(t *testing.T) {
		f := newInterpretFixture(t, func(f *interpretFixture) { f.findings = fmt.Sprintf(testFindings, false) })
		f.requestChanges(t, time.Minute, 0, "look at the cache layer too")
		f.decide(t, 2*time.Hour, 1, true, "")
		if out := f.run(t, bugSnapshot(t)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); got != "investigate/1 decide/1 investigate/2 decide/2 fix/1 checks/1 open/1" {
			t.Errorf("steps = %s", got)
		}
		if !f.opened[0].CanRequestChanges || !strings.Contains(f.opened[0].Reason, "**Root cause**") {
			t.Errorf("decision asked = %+v", f.opened[0])
		}
		if fb := f.sessions[1].Step.Feedback; !strings.Contains(fb, "ana sent your work back at the `decide` gate") || !strings.Contains(fb, "cache layer") {
			t.Errorf("second investigation feedback = %q", fb)
		}
	})
}

// The merge queue re-checks the fixed change, never a gate that must fail.
func TestQueueGatesSkipGatesExpectingFailure(t *testing.T) {
	it := &interpreter{in: InterpretInput{Snapshot: bugSnapshot(t)}}
	gates := it.queueGates()
	if len(gates) != 1 || gates[0].StepID != "checks" {
		t.Errorf("queue gates = %+v", gates)
	}
}

// M6 (D96) done-when, as the interpreter runs it: the built-in bug-fix
// workflow investigates, shows the bug with a test that fails, fixes it
// keeping the test, has it reviewed and merges it.
func TestInterpretBuiltInBugFix(t *testing.T) {
	w, err := workflows.Get("bug-fix")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := config.ResolveRoles(w, roles.Get)
	if err != nil {
		t.Fatal(err)
	}
	f := newInterpretFixture(t, func(f *interpretFixture) { f.findings = fmt.Sprintf(testFindings, true) })
	out := f.run(t, snapshot.Snapshot{Workflow: w, Roles: rs, Source: "built-in", BaseSHA: "base"})
	if !out.Succeeded || !out.Merged {
		t.Fatalf("result = %+v", out)
	}
	want := "investigate/1 reproduce/1 shows-bug/1 fix/1 keeps-test/1 checks/1 bug-reviewer/1 review/1 open-change-request/1 merge/1"
	if got := f.stepIDs(); got != want {
		t.Errorf("steps = %s\nwant    %s", got, want)
	}
	roleOf := func(i int) string { return f.sessions[i].Step.Role }
	if roleOf(0) != "bug-investigator" || roleOf(1) != "bug-reproducer" || roleOf(2) != "coder" || roleOf(3) != "bug-reviewer" {
		t.Errorf("roles = %s %s %s %s", roleOf(0), roleOf(1), roleOf(2), roleOf(3))
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(f.sessions[i].Step.Findings, "stats sum shelf rows") {
			t.Errorf("%s did not get the findings", roleOf(i))
		}
	}
	if !f.gates[0].ExpectFail || f.gates[1].ExpectFail || len(f.enqueued) != 1 || len(f.enqueued[0].Gates) != 1 || f.enqueued[0].Gates[0].StepID != "checks" {
		t.Errorf("gates %+v, enqueued %+v", f.gates, f.enqueued)
	}
}

// M7 (D122): the built-in build-e2e runs the coder and the e2e test writer at
// once, merges the writer's branch, keeps its tests, runs the suite locally as
// a gate before review, and the merge queue re-runs both command gates.
func TestInterpretBuiltInBuildE2E(t *testing.T) {
	w, err := workflows.Get("build-e2e")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := config.ResolveRoles(w, roles.Get)
	if err != nil {
		t.Fatal(err)
	}
	f := newInterpretFixture(t)
	out := f.run(t, snapshot.Snapshot{Workflow: w, Roles: rs, Source: "built-in", BaseSHA: "base"})
	if !out.Succeeded || !out.Merged {
		t.Fatalf("result = %+v", out)
	}
	got := f.stepIDs()
	writers := strings.Fields(got)[:2]
	slices.Sort(writers)
	if want := "build-merge/1 keeps-tests/1 checks/1 e2e/1 code-reviewer/1 review/1 open-change-request/1 merge/1"; !strings.HasSuffix(got, want) ||
		strings.Join(writers, " ") != "e2e-tests/1 implement/1" {
		t.Errorf("steps = %s", got)
	}
	writer := f.session(t, "sr-e2e-tests-1")
	if writer.Step.Role != "e2e-test-writer" || writer.Branch != testRun.Branch+"--e2e-tests" || slices.Contains(writer.Step.Inputs, "diff") {
		t.Errorf("writer session = %+v, step %+v", writer, writer.Step)
	}
	// The coder knows the e2e test writer works alongside it, and so leaves
	// it the suite.
	coder := f.session(t, "sr-implement-1")
	if !slices.Equal(coder.Step.Alongside, []string{"e2e-test-writer"}) || coder.Step.RoleVersion != 2 ||
		!strings.Contains(coder.Step.SystemPrompt, "Leave the end-to-end suite to it") {
		t.Errorf("coder step = %+v", coder.Step)
	}
	if len(f.gates) != 2 || f.gates[1].StepID != "e2e" || strings.Join(f.gates[1].Commands, ";") != "mise run e2e:local" || f.gates[1].CommitSHA != "merged-1" {
		t.Errorf("gates = %+v", f.gates)
	}
	if len(f.enqueued) != 1 || len(f.enqueued[0].Gates) != 2 || f.enqueued[0].Gates[1].StepID != "e2e" {
		t.Errorf("enqueued = %+v; want both command gates re-run by the queue", f.enqueued)
	}
}

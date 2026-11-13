package workflow

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// InterpretInput is the issue and everything the run executes, fixed when the
// run starts (invariant 4, FR-WF-1): the definition is never re-read mid-run.
type InterpretInput struct {
	Issue    activities.IssueRunInput
	Snapshot snapshot.Snapshot
}

// actionOpenChangeRequest is the only action implemented so far (D39).
const actionOpenChangeRequest = "change-request.open"

// CheckSupported reports the parts of a valid definition this interpreter
// cannot run yet, so a project selecting one fails before its run starts work.
// Everything the definition schema accepts runs now except actions arriving
// with later milestones, which validation already rejects.
func CheckSupported(w config.Workflow) error {
	var errs []error
	for i, s := range w.Steps {
		if s.Kind() == config.StepAction && s.Action != actionOpenChangeRequest {
			errs = append(errs, fmt.Errorf("steps[%d]: action %q is not supported yet", i, s.Action))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("workflow %q: %w", w.Name, err)
	}
	return nil
}

// Interpret is the generic workflow (docs/spec/03-workflows-and-gates.md): it
// runs the snapshotted definition's steps in order. A failed blocking gate
// either fails the run or loops back to an earlier step, at most maxLoops
// times, with what the gate found as the agent's feedback. Agent steps
// continue the issue's agent branch (D36), so git history carries the work
// from one step and one loop to the next. A parallel group runs its branches
// one after another (D42); branches whose role returns a review verdict
// review the last pushed commit, read-only, for the group's agent-verdict gate.
func Interpret(ctx workflow.Context, in InterpretInput) (IssueRunResult, error) {
	var a *activities.Activities
	var run activities.StartRunResult
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.StartWorkflowRun, in.Issue, in.Snapshot).Get(ctx, &run); err != nil {
		return IssueRunResult{}, err
	}
	out := IssueRunResult{RunID: run.RunID}
	if in.Snapshot.Error != "" {
		out.Reason = in.Snapshot.Error
	} else {
		it := &interpreter{in: in, run: run, attempts: map[string]int{}, loops: map[string]int{}}
		out.Reason = it.execute(ctx)
		out.Succeeded, out.ChangeRequestURL = out.Reason == "", it.changeRequest
	}

	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
	finish := activities.FinishInput{IssueID: in.Issue.IssueID, RunID: run.RunID, Succeeded: out.Succeeded, Reason: out.Reason}
	if err := workflow.ExecuteActivity(cleanup, a.FinishRun, finish).Get(cleanup, nil); err != nil {
		return out, err
	}
	return out, nil
}

// interpreter is one run's progress through its definition. Its maps are only
// indexed, never ranged over, so execution stays deterministic.
type interpreter struct {
	a        *activities.Activities // nil; names the Activities
	in       InterpretInput
	run      activities.StartRunResult
	attempts map[string]int // step ID -> executions so far
	loops    map[string]int // gate step ID -> loops back taken
	// feedback is what the gate that last looped back found, for the next agent step.
	feedback string
	// session is the last agent step's result: the commit gates check.
	session       activities.SessionResult
	changeRequest string
	// only is the branch a loop back targets: the next parallel group runs
	// just that branch, then the run continues after the group.
	only string
}

// execute runs the steps and returns why the run failed, or "" on success.
func (it *interpreter) execute(ctx workflow.Context) string {
	steps := it.in.Snapshot.Workflow.Steps
	for pc := 0; pc < len(steps); {
		s := steps[pc]
		next, reason := pc+1, ""
		switch s.Kind() {
		case config.StepAgent:
			reason = it.agent(ctx, s)
		case config.StepGate:
			next, reason = it.gate(ctx, pc, s)
		case config.StepAction:
			reason = it.action(ctx, s)
		case config.StepParallel:
			only := it.only
			it.only = ""
			next, reason = it.parallel(ctx, pc, s, only)
		default:
			reason = fmt.Sprintf("%s steps are not supported yet", s.Kind())
		}
		if reason != "" {
			return fmt.Sprintf("step %s: %s", s.ID, reason)
		}
		pc = next
	}
	return it.guardChanges(ctx)
}

// startStep creates the StepRun of the step's next execution.
func (it *interpreter) startStep(ctx workflow.Context, id string, kind domain.StepKind) (string, error) {
	it.attempts[id]++
	var stepRunID string
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.StartStep,
		activities.StepStart{RunID: it.run.RunID, StepID: id, Kind: kind, Attempt: it.attempts[id]}).Get(ctx, &stepRunID)
	return stepRunID, err
}

// finishStep ends a step's execution even when the workflow is being canceled.
func (it *interpreter) finishStep(ctx workflow.Context, stepRunID string, succeeded bool) {
	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
	_ = workflow.ExecuteActivity(cleanup, it.a.FinishStep, activities.StepEnd{StepRunID: stepRunID, Succeeded: succeeded}).Get(cleanup, nil)
}

// agent runs one execution of a coding agent step, which must push.
func (it *interpreter) agent(ctx workflow.Context, s config.Step) string {
	session, stepRunID, reason := it.agentSession(ctx, s)
	if stepRunID == "" {
		return reason
	}
	if reason == "" && !session.Pushed {
		reason = "agent session made no changes"
	}
	it.finishStep(ctx, stepRunID, reason == "")
	if reason == "" {
		it.session, it.feedback = session, ""
	}
	return reason
}

// reviewer runs one execution of a reviewing agent step on the last pushed
// commit; its final message is the verdict.
func (it *interpreter) reviewer(ctx workflow.Context, s config.Step) (activities.Review, string) {
	if it.session.CommitSHA == "" {
		return activities.Review{}, "no agent step has pushed a commit to review"
	}
	session, stepRunID, reason := it.agentSession(ctx, s)
	if stepRunID == "" {
		return activities.Review{}, reason
	}
	it.finishStep(ctx, stepRunID, reason == "")
	return activities.Review{StepID: s.ID, Role: s.Agent.RoleName(), StepRunID: stepRunID, CommitSHA: session.CommitSHA,
		Result: session.Result}, reason
}

// agentSession starts the step's StepRun and runs its session in a pod. It
// returns why the session failed, if it did, and leaves ending the StepRun to
// the caller; an empty stepRunID means no StepRun was created.
func (it *interpreter) agentSession(ctx workflow.Context, s config.Step) (activities.SessionResult, string, string) {
	stepRunID, err := it.startStep(ctx, s.ID, domain.StepAgent)
	if err != nil {
		return activities.SessionResult{}, "", err.Error()
	}
	role := it.in.Snapshot.Roles[s.Agent.RoleName()]
	step := &activities.AgentStep{StepID: s.ID, Role: role.Name, RoleVersion: role.Version, SystemPrompt: role.SystemPrompt,
		Model: config.GatewayModel(cmp.Or(s.Agent.Model, role.DefaultModel)), BudgetUSD: s.BudgetUSD, Timeout: time.Duration(s.Timeout),
		Inputs: s.Inputs, AllowedTools: role.AllowedTools, Contract: role.OutputContract}
	if slices.Contains(s.Inputs, "feedback") && step.Contract == "" {
		step.Feedback = it.feedback
	}
	run := activities.StartRunResult{RunID: it.run.RunID, StepRunID: stepRunID, Branch: it.run.Branch, Step: step}

	err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, toolchainOptions), it.a.EnsureToolchainImage, it.in.Issue).Get(ctx, &run.ToolchainImage)
	if err != nil {
		err = fmt.Errorf("toolchain image: %w", err)
	}
	var pod activities.AgentPod
	if err == nil {
		err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, launchOptions), it.a.LaunchAgentPod, it.in.Issue, run).Get(ctx, &pod)
	}
	var session activities.SessionResult
	if err == nil {
		opts := sessionOptions
		if step.Timeout > 0 {
			opts.StartToCloseTimeout = step.Timeout + 30*time.Minute
		}
		err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), it.a.RunAgentSession, it.in.Issue, run, pod).Get(ctx, &session)
	}
	if pod.Name != "" {
		cleanup, _ := workflow.NewDisconnectedContext(ctx)
		cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
		_ = workflow.ExecuteActivity(cleanup, it.a.DeleteAgentPod, pod.Name).Get(cleanup, nil)
	}

	switch {
	case err != nil:
		return session, stepRunID, "agent step failed: " + err.Error()
	case session.Status != "succeeded":
		return session, stepRunID, "agent session " + session.Status + ": " + session.Reason
	}
	return session, stepRunID, ""
}

// parallel runs a group's branches one after another (D42), or only the branch
// a loop back targets, then judges the group's gate.
func (it *interpreter) parallel(ctx workflow.Context, pc int, s config.Step, only string) (int, string) {
	var reviews []activities.Review
	for _, b := range s.Parallel {
		if only != "" && b.ID != only {
			continue
		}
		if it.in.Snapshot.Roles[b.Agent.RoleName()].OutputContract != "" {
			r, reason := it.reviewer(ctx, b)
			if reason != "" {
				return 0, fmt.Sprintf("branch %s: %s", b.ID, reason)
			}
			reviews = append(reviews, r)
			continue
		}
		if reason := it.agent(ctx, b); reason != "" {
			return 0, fmt.Sprintf("branch %s: %s", b.ID, reason)
		}
	}
	if only != "" || s.Gate == nil {
		return pc + 1, ""
	}
	if s.Gate.Type != domain.GateAgentVerdict {
		return it.gate(ctx, pc, s)
	}
	stepRunID, err := it.startStep(ctx, s.ID, domain.StepGate)
	if err != nil {
		return 0, err.Error()
	}
	var outcome activities.GateOutcome
	err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.RunVerdictGate, activities.VerdictGateRequest{
		RunID: it.run.RunID, StepRunID: stepRunID, StepID: s.ID, Require: string(s.Gate.Require), Policy: s.Gate.Policy,
		CommitSHA: it.session.CommitSHA, Reviews: reviews,
	}).Get(ctx, &outcome)
	if err != nil {
		it.finishStep(ctx, stepRunID, false)
		return 0, "gate could not run: " + err.Error()
	}
	return it.judge(ctx, pc, s, outcome)
}

func (it *interpreter) gate(ctx workflow.Context, pc int, s config.Step) (int, string) {
	g := s.Gate
	switch g.Type {
	case domain.GateHuman:
		return it.humanGate(ctx, pc, s)
	case domain.GateCommand:
	default:
		return 0, fmt.Sprintf("%s gates are not supported here", g.Type)
	}
	if it.session.CommitSHA == "" {
		return 0, "no agent step has pushed a commit to check"
	}
	stepRunID, err := it.startStep(ctx, s.ID, domain.StepGate)
	if err != nil {
		return 0, err.Error()
	}
	opts := gateOptions
	if g.Timeout > 0 {
		// The gate may first build the commit's toolchain and dependency images.
		opts.StartToCloseTimeout = time.Duration(g.Timeout) + 120*time.Minute
	}
	var outcome activities.GateOutcome
	err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), it.a.RunGate, it.in.Issue, activities.GateRequest{
		RunID: it.run.RunID, StepRunID: stepRunID, StepID: s.ID, Commands: g.Run, Timeout: time.Duration(g.Timeout),
		Branch: it.session.Branch, CommitSHA: it.session.CommitSHA,
	}).Get(ctx, &outcome)
	if err != nil {
		it.finishStep(ctx, stepRunID, false)
		return 0, "gate could not run: " + err.Error()
	}
	return it.judge(ctx, pc, s, outcome)
}

// judge continues after a passed or advisory gate, fails the run on an
// inconclusive one, and applies onFail to a failed blocking one.
func (it *interpreter) judge(ctx workflow.Context, pc int, s config.Step, outcome activities.GateOutcome) (int, string) {
	switch {
	case outcome.Passed || s.Gate.Policy == domain.GateAdvisory:
		return pc + 1, ""
	case outcome.Inconclusive:
		return 0, "gate inconclusive: " + outcome.Summary
	}
	return it.onFail(ctx, pc, s, outcome)
}

// onFail applies a failed blocking gate's onFail: fail the run, escalate to a
// person, or loop back. A loop that has used up maxLoops escalates (FR-WF-4).
func (it *interpreter) onFail(ctx workflow.Context, pc int, s config.Step, outcome activities.GateOutcome) (int, string) {
	failed := "gate failed: " + outcome.Summary
	o := s.Gate.OnFail
	switch {
	case o != nil && o.Action == config.OnFailEscalate:
		return it.escalate(ctx, pc, s, outcome.Summary)
	case o == nil || o.Action != config.OnFailGoto:
		return 0, failed
	}
	if it.loops[s.ID] >= o.MaxLoops {
		exhausted := fmt.Sprintf("%s (looped back to %s %d times, the limit)", outcome.Summary, o.Goto, it.loops[s.ID])
		// Runs recorded before escalation existed failed here.
		if workflow.GetVersion(ctx, "escalate-exhausted-loops", workflow.DefaultVersion, 1) >= 1 {
			return it.escalate(ctx, pc, s, exhausted)
		}
		return 0, "gate failed: " + exhausted
	}
	target, branch := it.locate(o.Goto)
	if target < 0 {
		return 0, fmt.Sprintf("onFail goes to %q, which is not a step", o.Goto)
	}
	it.loops[s.ID]++
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.RecordLoop, it.run.RunID, s.ID, it.loops[s.ID]).Get(ctx, nil); err != nil {
		return 0, "recording the loop failed: " + err.Error()
	}
	it.feedback = fmt.Sprintf("The `%s` check failed on your last commit: %s\n\n%s", s.ID, outcome.Summary, outcome.Feedback)
	it.only = branch
	return target, ""
}

// locate finds a step by ID: its top-level index, and the branch ID when the
// step is a branch of a parallel group. -1 when there is no such step.
func (it *interpreter) locate(id string) (int, string) {
	for i, s := range it.in.Snapshot.Workflow.Steps {
		if s.ID == id {
			return i, ""
		}
		for _, b := range s.Parallel {
			if b.ID == id {
				return i, id
			}
		}
	}
	return -1, ""
}

func (it *interpreter) action(ctx workflow.Context, s config.Step) string {
	if s.Action != actionOpenChangeRequest {
		return fmt.Sprintf("action %q is not supported yet", s.Action)
	}
	if !it.session.Pushed {
		return "no agent step has pushed a change to open a change request for"
	}
	stepRunID, err := it.startStep(ctx, s.ID, domain.StepAction)
	if err != nil {
		return err.Error()
	}
	err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.OpenChangeRequest, it.in.Issue, it.run, it.session).Get(ctx, &it.changeRequest)
	it.finishStep(ctx, stepRunID, err == nil)
	if err != nil {
		return "opening the change request failed: " + err.Error()
	}
	return ""
}

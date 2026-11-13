package activities

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/sandbox"
	"github.com/misarescu/frankie/internal/store"
)

// gateStepID is the M2 workflow's single command gate.
const gateStepID = "checks"

const (
	gateLogTailLines = 200
	gateLogTailBytes = 32 << 10
	// What a loop back shows the agent of each failed command's output.
	feedbackTailBytes = 6 << 10
)

// RunCommandGate is IssueRun's command gate: the platform's commands, as the
// run's single `checks` step.
func (a *Activities) RunCommandGate(ctx context.Context, in IssueRunInput, run StartRunResult, session SessionResult) (GateOutcome, error) {
	runID, err := uuid.Parse(run.RunID)
	if err != nil {
		return GateOutcome{}, temporal.NewNonRetryableApplicationError("bad run id", "input", err)
	}
	step, err := a.stepRun(ctx, store.NewStepRun{RunID: runID, StepID: gateStepID, Attempt: 1, Kind: domain.StepGate})
	if err != nil {
		return GateOutcome{}, err
	}
	return a.commandGate(ctx, in, step, GateRequest{RunID: run.RunID, StepRunID: step.ID.String(), StepID: gateStepID,
		Branch: session.Branch, CommitSHA: session.CommitSHA})
}

// RunGate is one execution of a definition's command gate (Interpret), whose
// StepRun StartStep created.
func (a *Activities) RunGate(ctx context.Context, in IssueRunInput, req GateRequest) (GateOutcome, error) {
	id, err := uuid.Parse(req.StepRunID)
	if err != nil {
		return GateOutcome{}, temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil {
		return GateOutcome{}, err
	}
	return a.commandGate(ctx, in, step, req)
}

// commandGate re-runs the gate commands from the pushed commit in a fresh pod
// (invariant 2: the agent's own report is never evidence) and records the
// verdict. The verdict comes from the exit codes the kubelet recorded for each
// command's container, never from the pod's output. Idempotent: the pod and
// result are keyed by the step run, and a retry adopts what exists.
func (a *Activities) commandGate(ctx context.Context, in IssueRunInput, step domain.StepRun, req GateRequest) (GateOutcome, error) {
	out := GateOutcome{StepRunID: step.ID.String()}
	if step.Status.Terminal() {
		// An earlier attempt finished the gate; report what it recorded.
		return a.recordedOutcome(ctx, step.RunID, step)
	}
	if _, err := a.Store.SetStepRunCommit(ctx, step.ID, req.CommitSHA); err != nil {
		return out, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, step.ID, domain.ExecPending, domain.ExecRunning); err != nil && step.Status != domain.ExecRunning {
		return out, err
	}

	// The gate runs in the toolchain the commit itself declares.
	image, err := a.toolchainImage(ctx, in.Repo, req.CommitSHA)
	if err != nil {
		return out, fmt.Errorf("toolchain image for %s: %w", req.CommitSHA, err)
	}
	image = cmp.Or(image, a.Agent.RunnerImage)
	commands := req.Commands
	if len(commands) == 0 {
		commands = a.Gate.Commands
	}
	if len(commands) == 0 {
		commands = DefaultGateCommands
	}
	deadline := cmp.Or(req.Timeout, a.Gate.Deadline)
	// Services also come from the commit under test; a broken declaration
	// there fails the gate, it is the agent's change.
	services, err := a.projectServices(ctx, in.Repo, req.CommitSHA)
	if errors.Is(err, errInvalidProjectConfig) {
		return a.finishGate(ctx, step, domain.CommandGateDetails{Image: image, Error: err.Error()})
	}
	if err != nil {
		return out, err
	}
	token, err := a.Signer.Issue(gitproxy.Grant{Repo: in.Repo, IssueKey: in.IssueKey, StepRunID: step.ID.String(), ReadOnly: true},
		deadline+10*time.Minute)
	if err != nil {
		return out, err
	}
	pod, err := sandbox.GatePod(sandbox.GatePodSpec{
		Name: stepPodName(in.IssueKey, req.StepID, step.ID.String()), Namespace: a.Agent.Namespace, Image: image,
		RuntimeClassName: a.Agent.RuntimeClass, ServiceAccountName: a.Agent.ServiceAccount,
		RunID: req.RunID, StepRunID: step.ID.String(), IssueKey: in.IssueKey,
		Limits: a.Gate.Limits, WorkspaceSize: a.Gate.WorkspaceSize, Deadline: deadline,
		EgressProxyURL: a.Agent.EgressProxyURL, NoProxy: a.Agent.NoProxy,
		CloneURL: strings.TrimSuffix(a.Agent.GitProxyURL, "/") + "/git/" + in.Repo + ".git", Token: token,
		Branch: req.Branch, CommitSHA: req.CommitSHA, Commands: commands, Services: services, Env: a.Agent.Env,
	})
	if err != nil {
		return out, temporal.NewNonRetryableApplicationError("invalid gate pod spec", "config", err)
	}

	pods := a.Kube.CoreV1().Pods(a.Agent.Namespace)
	if _, err := pods.Create(ctx, pod, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return out, fmt.Errorf("create gate pod: %w", err)
	}
	if _, err := a.Store.SetStepRunPod(ctx, step.ID, pod.Name); err != nil {
		return out, err
	}
	finished, err := a.waitForGatePod(ctx, pod.Name)
	if err != nil {
		return out, err
	}

	details := a.gateDetails(ctx, finished, image, commands)
	out, err = a.finishGate(ctx, step, details)
	if err != nil {
		return out, err
	}
	if err := pods.Delete(context.WithoutCancel(ctx), pod.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		a.Log.Warn("gate pod not deleted", "pod", pod.Name, "error", err)
	}
	return out, nil
}

// finishGate records the verdict and ends the gate's StepRun.
func (a *Activities) finishGate(ctx context.Context, step domain.StepRun, details domain.CommandGateDetails) (GateOutcome, error) {
	out := GateOutcome{StepRunID: step.ID.String()}
	verdict, status := domain.GateFail, domain.ExecFailed
	if details.Passed() {
		verdict, status = domain.GatePass, domain.ExecSucceeded
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return out, fmt.Errorf("encode gate details: %w", err)
	}
	if _, err := a.Store.RecordGateResult(ctx, store.NewGateResult{StepRunID: step.ID, Type: domain.GateCommand,
		Policy: domain.GateBlocking, Verdict: verdict, Details: raw}); err != nil {
		return out, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, step.ID, domain.ExecRunning, status); err != nil {
		return out, err
	}
	out.Passed, out.Summary, out.Feedback = details.Passed(), summarize(details), feedback(details)
	return out, nil
}

// recordedOutcome rebuilds the outcome of a gate an earlier attempt finished.
func (a *Activities) recordedOutcome(ctx context.Context, runID uuid.UUID, step domain.StepRun) (GateOutcome, error) {
	results, err := a.Store.ListGateResultsForRun(ctx, runID)
	if err != nil {
		return GateOutcome{}, err
	}
	for _, r := range results {
		if r.StepRunID != step.ID {
			continue
		}
		var d domain.CommandGateDetails
		if err := json.Unmarshal(r.Details, &d); err != nil {
			return GateOutcome{}, fmt.Errorf("decode gate details: %w", err)
		}
		return GateOutcome{StepRunID: step.ID.String(), Passed: r.Verdict == domain.GatePass, Summary: summarize(d), Feedback: feedback(d)}, nil
	}
	return GateOutcome{}, fmt.Errorf("gate step %s is %s but has no result", step.ID, step.Status)
}

// waitForGatePod waits until the pod has run to completion.
func (a *Activities) waitForGatePod(ctx context.Context, name string) (*corev1.Pod, error) {
	pods := a.Kube.CoreV1().Pods(a.Agent.Namespace)
	for {
		p, err := pods.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get gate pod: %w", err)
		}
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			return p, nil
		}
		for _, s := range append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
			if w := s.State.Waiting; w != nil && (w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull" || w.Reason == "CreateContainerConfigError") {
				return nil, temporal.NewNonRetryableApplicationError(w.Reason+": "+w.Message, "pod", nil)
			}
		}
		activity.RecordHeartbeat(ctx, p.Status.Phase)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// gateDetails reads each command's exit code from the pod status and the tail of
// its log. Logs are for humans and later agents; they never decide the verdict.
func (a *Activities) gateDetails(ctx context.Context, p *corev1.Pod, image string, commands []string) domain.CommandGateDetails {
	d := domain.CommandGateDetails{Image: image}
	status := map[string]corev1.ContainerStatus{}
	for _, s := range p.Status.InitContainerStatuses {
		status[s.Name] = s
	}
	if c, ok := status[sandbox.GateCloneContainer]; !ok || c.State.Terminated == nil || c.State.Terminated.ExitCode != 0 {
		d.Error = "checking out the commit failed: " + a.logTail(ctx, p.Name, sandbox.GateCloneContainer)
	}
	if p.Status.Reason == "DeadlineExceeded" {
		d.Error = cmp.Or(d.Error, "the gate pod exceeded its deadline")
	}
	for i, cmd := range commands {
		name := sandbox.GateCommandContainer(i)
		r := domain.CommandResult{Command: cmd}
		if s, ok := status[name]; ok && s.State.Terminated != nil {
			code := int(s.State.Terminated.ExitCode)
			r.ExitCode, r.Reason = &code, s.State.Terminated.Reason
			r.LogTail = a.logTail(ctx, p.Name, name)
		} else {
			r.Reason = "not run"
		}
		d.Commands = append(d.Commands, r)
	}
	return d
}

func (a *Activities) logTail(ctx context.Context, pod, container string) string {
	raw, err := a.Kube.CoreV1().Pods(a.Agent.Namespace).GetLogs(pod, &corev1.PodLogOptions{
		Container: container, TailLines: ptr(int64(gateLogTailLines)), LimitBytes: ptr(int64(gateLogTailBytes)),
	}).Do(ctx).Raw()
	if err != nil {
		return "(log unavailable: " + err.Error() + ")"
	}
	return string(raw)
}

// summarize is the one-line reason a run reports, e.g. "mise run test exited 1".
func summarize(d domain.CommandGateDetails) string {
	if d.Error != "" {
		return strings.SplitN(d.Error, "\n", 2)[0]
	}
	var failed []string
	for _, c := range d.Commands {
		switch {
		case c.ExitCode == nil:
			failed = append(failed, c.Command+" did not run")
		case *c.ExitCode != 0:
			failed = append(failed, fmt.Sprintf("%s exited %d", c.Command, *c.ExitCode))
		}
	}
	if len(failed) == 0 {
		return "all checks passed"
	}
	return strings.Join(failed, "; ")
}

// feedback is what an agent sent back by a failed gate needs: which commands
// failed and how their output ended. Empty when the gate passed.
func feedback(d domain.CommandGateDetails) string {
	if d.Passed() {
		return ""
	}
	var b strings.Builder
	if d.Error != "" {
		fmt.Fprintf(&b, "The checks could not run: %s\n", tail(d.Error, feedbackTailBytes))
	}
	for _, c := range d.Commands {
		if c.ExitCode == nil || *c.ExitCode == 0 {
			continue
		}
		fmt.Fprintf(&b, "`%s` exited %d. The end of its output:\n```\n%s\n```\n", c.Command, *c.ExitCode, strings.TrimRight(tail(c.LogTail, feedbackTailBytes), "\n"))
	}
	return b.String()
}

// tail keeps the last n bytes of s, cut at a line start when there is one.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return s
}

func ptr[T any](v T) *T { return &v }

package activities

import (
	"cmp"
	"context"
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
	"github.com/misarescu/frankie/internal/sandbox"
)

// podName is stable per step run, so a retried launch finds its pod.
func podName(in IssueRunInput, run StartRunResult) string {
	return fmt.Sprintf("%s-implement-%s", in.IssueKey, strings.ReplaceAll(run.StepRunID, "-", "")[24:])
}

// agentPodSpec is the sandboxed pod for the run's agent step. It runs in the
// repo's toolchain image when the workflow resolved one, else the base image,
// with the services the repo declares.
func (a *Activities) agentPodSpec(in IssueRunInput, run StartRunResult, services []sandbox.Service) (*corev1.Pod, error) {
	return sandbox.AgentPod(sandbox.PodSpec{
		Name: podName(in, run), Namespace: a.Agent.Namespace, Image: cmp.Or(run.ToolchainImage, a.Agent.RunnerImage),
		RuntimeClassName: a.Agent.RuntimeClass, ServiceAccountName: a.Agent.ServiceAccount,
		RunID: run.RunID, StepRunID: run.StepRunID, IssueKey: in.IssueKey,
		Limits: a.Agent.Limits, WorkspaceSize: a.Agent.WorkspaceSize, Deadline: a.Agent.PodDeadline,
		EgressProxyURL: a.Agent.EgressProxyURL, NoProxy: a.Agent.NoProxy, Services: services,
	})
}

// LaunchAgentPod creates the sandboxed agent pod and waits for its IP.
func (a *Activities) LaunchAgentPod(ctx context.Context, in IssueRunInput, run StartRunResult) (AgentPod, error) {
	// The agent clones the base branch, so its services come from there too.
	services, err := a.projectServices(ctx, in.Repo, in.BaseBranch)
	if errors.Is(err, errInvalidProjectConfig) {
		return AgentPod{}, temporal.NewNonRetryableApplicationError(err.Error(), "config", err)
	}
	if err != nil {
		return AgentPod{}, err
	}
	spec, err := a.agentPodSpec(in, run, services)
	if err != nil {
		return AgentPod{}, temporal.NewNonRetryableApplicationError("invalid agent pod spec", "config", err)
	}
	pods := a.Kube.CoreV1().Pods(a.Agent.Namespace)
	if _, err := pods.Create(ctx, spec, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return AgentPod{}, fmt.Errorf("create agent pod: %w", err)
	}
	for {
		p, err := pods.Get(ctx, spec.Name, metav1.GetOptions{})
		if err != nil {
			return AgentPod{}, fmt.Errorf("get agent pod: %w", err)
		}
		if p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" && podReady(p) {
			stepID, _ := uuid.Parse(run.StepRunID)
			if _, err := a.Store.SetStepRunPod(ctx, stepID, p.Name); err != nil {
				return AgentPod{}, err
			}
			if _, err := a.Store.TransitionStepRun(ctx, stepID, domain.ExecPending, domain.ExecRunning); err != nil {
				return AgentPod{}, err
			}
			return AgentPod{Name: p.Name, IP: p.Status.PodIP}, nil
		}
		if p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded {
			return AgentPod{Name: p.Name}, fmt.Errorf("agent pod ended before the session: %s", p.Status.Phase)
		}
		for _, s := range p.Status.ContainerStatuses {
			if w := s.State.Waiting; w != nil && (w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull" || w.Reason == "CreateContainerConfigError") {
				return AgentPod{Name: p.Name}, temporal.NewNonRetryableApplicationError(w.Reason+": "+w.Message, "pod", nil)
			}
		}
		activity.RecordHeartbeat(ctx, p.Status.Phase)
		select {
		case <-ctx.Done():
			return AgentPod{Name: p.Name}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.ContainerStatuses {
		if !c.Ready {
			return false
		}
	}
	return len(p.Status.ContainerStatuses) > 0
}

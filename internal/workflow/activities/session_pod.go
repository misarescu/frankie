package activities

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// sessionPodCheck is how often a session checks that its agent pod still runs
// (D102).
const sessionPodCheck = 15 * time.Second

// agentPodEndedError says a session's agent pod can no longer run its runner.
type agentPodEndedError struct {
	pod, why string
}

func (e *agentPodEndedError) Error() string {
	return fmt.Sprintf("agent pod %s %s", e.pod, e.why)
}

// watchAgentPod returns an *agentPodEndedError as soon as the agent pod is gone,
// being deleted, or ended (D102): the runner serves until it is stopped, so a
// pod that ended during a session lost its runner, and the session's stream
// would otherwise wait for events that never come. It checks at once, then
// every interval, and returns nil when ctx ends. A failed read is retried at
// the next check: only the pod's own state ends a session.
func watchAgentPod(ctx context.Context, pods typedcorev1.PodInterface, name string, every time.Duration) error {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if why := agentPodEnded(ctx, pods, name); why != "" {
			return &agentPodEndedError{pod: name, why: why}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// agentPodEnded says why the pod no longer runs its runner, or "" while it may.
func agentPodEnded(ctx context.Context, pods typedcorev1.PodInterface, name string) string {
	p, err := pods.Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return "is gone"
	case err != nil:
		return ""
	case p.DeletionTimestamp != nil:
		return "is being deleted"
	case p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded:
		why := "ended: " + string(p.Status.Phase)
		for _, c := range p.Status.ContainerStatuses {
			if term := c.State.Terminated; term != nil {
				why += fmt.Sprintf(", %s exited %d (%s)", c.Name, term.ExitCode, term.Reason)
			}
		}
		if p.Status.Reason != "" {
			why += ", " + p.Status.Reason
		}
		return why
	}
	return ""
}

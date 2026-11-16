package activities

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func agentPodWith(phase corev1.PodPhase, mutate ...func(*corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "gc-1-implement-abc", Namespace: "frankie-agents"},
		Status:     corev1.PodStatus{Phase: phase},
	}
	for _, m := range mutate {
		m(p)
	}
	return p
}

// D102: a session ends as soon as its agent pod can no longer run the runner.
func TestAgentPodEnded(t *testing.T) {
	now := metav1.Now()
	tests := []struct {
		name string
		pod  *corev1.Pod // nil: no such pod
		want string      // "" while the pod may still run its runner
	}{
		{"running", agentPodWith(corev1.PodRunning), ""},
		{"pending", agentPodWith(corev1.PodPending), ""},
		{"gone", nil, "is gone"},
		{"being deleted", agentPodWith(corev1.PodRunning, func(p *corev1.Pod) { p.DeletionTimestamp = &now }), "is being deleted"},
		{"failed, sandbox lost", agentPodWith(corev1.PodFailed, func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "runner",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 255, Reason: "Error"}}}}
		}), "ended: Failed, runner exited 255 (Error)"},
		{"evicted", agentPodWith(corev1.PodFailed, func(p *corev1.Pod) { p.Status.Reason = "Evicted" }), "ended: Failed, Evicted"},
		{"succeeded", agentPodWith(corev1.PodSucceeded), "ended: Succeeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kube := fake.NewClientset()
			if tt.pod != nil {
				kube = fake.NewClientset(tt.pod)
			}
			got := agentPodEnded(t.Context(), kube.CoreV1().Pods("frankie-agents"), "gc-1-implement-abc")
			if got != tt.want {
				t.Errorf("ended = %q, want %q", got, tt.want)
			}
		})
	}
}

// D102: the watch notices a pod that fails during the session, and stops
// quietly when the session ends first.
func TestWatchAgentPod(t *testing.T) {
	kube := fake.NewClientset(agentPodWith(corev1.PodRunning))
	pods := kube.CoreV1().Pods("frankie-agents")

	done := make(chan error, 1)
	go func() { done <- watchAgentPod(t.Context(), pods, "gc-1-implement-abc", 10*time.Millisecond) }()
	select {
	case err := <-done:
		t.Fatalf("watch ended while the pod runs: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	failed := agentPodWith(corev1.PodFailed)
	if _, err := pods.UpdateStatus(t.Context(), failed, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var ended *agentPodEndedError
		if !errors.As(err, &ended) || !strings.Contains(err.Error(), "gc-1-implement-abc ended: Failed") {
			t.Errorf("watch = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watch missed the failed pod")
	}

	ctx, cancel := context.WithCancel(t.Context())
	quiet := make(chan error, 1)
	go func() {
		quiet <- watchAgentPod(ctx, fake.NewClientset(agentPodWith(corev1.PodRunning)).CoreV1().Pods("frankie-agents"),
			"gc-1-implement-abc", 10*time.Millisecond)
	}()
	cancel()
	select {
	case err := <-quiet:
		if err != nil {
			t.Errorf("a finished session's watch = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watch outlived its session")
	}
}

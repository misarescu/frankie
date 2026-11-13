package activities

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeleteAgentPod removes the pod; a missing pod is fine.
func (a *Activities) DeleteAgentPod(ctx context.Context, name string) error {
	err := a.Kube.CoreV1().Pods(a.Agent.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

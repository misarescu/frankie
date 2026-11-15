package images

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// CollectedAtAnnotation is set on the registry Deployment's pod template to
// restart it for a collection (D84). The registry's init container collects
// its garbage before it serves; the annotation's value is when, so the time of
// the last collection survives control plane restarts.
const CollectedAtAnnotation = "frankie.dev/collected-at"

// KubeRegistryHost is the registry as a Kubernetes Deployment, with builds
// as Jobs in BuildNamespace.
type KubeRegistryHost struct {
	Kube           kubernetes.Interface
	Namespace      string // the registry's
	Deployment     string
	BuildNamespace string
	// Timeout bounds the wait for the restarted registry; zero means 5 minutes.
	Timeout time.Duration
	// PollInterval is how often the rollout is checked; zero means 2s.
	PollInterval time.Duration
}

// LastCollected implements RegistryHost.
func (h KubeRegistryHost) LastCollected(ctx context.Context) (time.Time, error) {
	d, err := h.Kube.AppsV1().Deployments(h.Namespace).Get(ctx, h.Deployment, metav1.GetOptions{})
	if err != nil {
		return time.Time{}, fmt.Errorf("registry deployment: %w", err)
	}
	v, ok := d.Spec.Template.Annotations[CollectedAtAnnotation]
	if !ok {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		// A mangled value means no collection on record: collect.
		return time.Time{}, nil
	}
	return at, nil
}

// BuildsActive implements RegistryHost: any build Job that has not finished.
func (h KubeRegistryHost) BuildsActive(ctx context.Context) (bool, error) {
	jobs, err := h.Kube.BatchV1().Jobs(h.BuildNamespace).List(ctx, metav1.ListOptions{LabelSelector: RoleLabel + "=" + RoleBuild})
	if err != nil {
		return false, fmt.Errorf("list build jobs: %w", err)
	}
	for i := range jobs.Items {
		if done, _ := jobState(&jobs.Items[i]); !done {
			return true, nil
		}
	}
	return false, nil
}

// Restart implements RegistryHost: it sets the annotation, which rolls the
// Deployment (Recreate: the old pod stops before the new one collects), and
// waits until the new pod is available.
func (h KubeRegistryHost) Restart(ctx context.Context, at time.Time) error {
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{
		"annotations": map[string]string{CollectedAtAnnotation: at.UTC().Format(time.RFC3339)},
	}}}})
	if err != nil {
		return err
	}
	deps := h.Kube.AppsV1().Deployments(h.Namespace)
	d, err := deps.Patch(ctx, h.Deployment, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("restart registry: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(h.Timeout, 5*time.Minute))
	defer cancel()
	generation := d.Generation
	for {
		d, err = deps.Get(ctx, h.Deployment, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("restart registry: %w", err)
		}
		if rolledOut(d, generation) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("restart registry: not available again: %w", ctx.Err())
		case <-time.After(cmp.Or(h.PollInterval, 2*time.Second)):
		}
	}
}

// rolledOut reports whether d runs generation with all its replicas updated
// and available, and no old ones left.
func rolledOut(d *appsv1.Deployment, generation int64) bool {
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	s := d.Status
	return s.ObservedGeneration >= generation && s.UpdatedReplicas == want && s.AvailableReplicas == want && s.Replicas == want
}

package images

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// KubeBuilder builds toolchain images with a one-off BuildKit Job per image
// (docs/spec/09: no standing BuildKit service on the concrete profile).
type KubeBuilder struct {
	Kube   kubernetes.Interface
	Config BuildConfig
	// PollInterval is how often the Job is checked; zero means 3s.
	PollInterval time.Duration
	// Heartbeat, when set, is called on every poll so a Temporal Activity stays alive.
	Heartbeat func(ctx context.Context, details ...any)
}

// Build implements Builder.
func (b KubeBuilder) Build(ctx context.Context, r BuildRequest) error {
	cm, job, err := BuildResources(b.Config, r)
	if err != nil {
		return err
	}
	jobs := b.Kube.BatchV1().Jobs(b.Config.Namespace)
	cms := b.Kube.CoreV1().ConfigMaps(b.Config.Namespace)
	interval := b.PollInterval
	if interval == 0 {
		interval = 3 * time.Second
	}

	if _, err := cms.Create(ctx, cm, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create build context: %w", err)
	}
	live, err := jobs.Create(ctx, job, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Another request, or an earlier attempt of this one, started the build.
		live, err = jobs.Get(ctx, job.Name, metav1.GetOptions{})
	}
	if err != nil {
		return fmt.Errorf("start build job: %w", err)
	}
	if err := b.adopt(ctx, cm.Name, live); err != nil {
		return err
	}

	done, failure := jobState(live)
	for !done {
		if b.Heartbeat != nil {
			b.Heartbeat(ctx, job.Name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		if live, err = jobs.Get(ctx, job.Name, metav1.GetOptions{}); err != nil {
			return fmt.Errorf("get build job: %w", err)
		}
		done, failure = jobState(live)
	}
	if failure != "" {
		// Remove the failed Job so the next request builds again instead of
		// finding the same failure.
		_ = b.cleanup(context.WithoutCancel(ctx), job.Name)
		return fmt.Errorf("build job %s failed: %s", job.Name, failure)
	}
	return nil
}

// jobState reports whether the Job finished and, if it failed, why.
func jobState(j *batchv1.Job) (done bool, failure string) {
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete:
			return true, ""
		case batchv1.JobFailed:
			return true, fmt.Sprintf("%s: %s", c.Reason, c.Message)
		}
	}
	return false, ""
}

// adopt makes the Job own its ConfigMap, so the context is garbage collected
// with it.
func (b KubeBuilder) adopt(ctx context.Context, cmName string, job *batchv1.Job) error {
	cms := b.Kube.CoreV1().ConfigMaps(b.Config.Namespace)
	cm, err := cms.Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get build context: %w", err)
	}
	if len(cm.OwnerReferences) > 0 {
		return nil
	}
	cm.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID,
	}}
	if _, err := cms.Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("adopt build context: %w", err)
	}
	return nil
}

// cleanup removes a failed build so a later request builds again.
func (b KubeBuilder) cleanup(ctx context.Context, name string) error {
	policy := metav1.DeletePropagationBackground
	err := b.Kube.BatchV1().Jobs(b.Config.Namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

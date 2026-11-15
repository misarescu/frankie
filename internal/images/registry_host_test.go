package images

import (
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func registryDeployment(annotations map[string]string) *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "frankie-registry", Namespace: "frankie", Generation: 1},
		Spec: appsv1.DeploymentSpec{Replicas: &one, Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Annotations: annotations},
		}},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
}

func newRegistryHost(objs ...runtime.Object) (KubeRegistryHost, *fake.Clientset) {
	kube := fake.NewClientset(objs...)
	return KubeRegistryHost{Kube: kube, Namespace: "frankie", Deployment: "frankie-registry", BuildNamespace: "frankie-build",
		PollInterval: time.Millisecond, Timeout: 5 * time.Second}, kube
}

func TestKubeRegistryHostLastCollected(t *testing.T) {
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		annotations map[string]string
		want        time.Time
	}{
		{"never", nil, time.Time{}},
		{"recorded", map[string]string{CollectedAtAnnotation: at.Format(time.RFC3339)}, at},
		{"mangled means never", map[string]string{CollectedAtAnnotation: "yesterday"}, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newRegistryHost(registryDeployment(tt.annotations))
			got, err := h.LastCollected(t.Context())
			if err != nil || !got.Equal(tt.want) {
				t.Fatalf("LastCollected = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	h, _ := newRegistryHost()
	if _, err := h.LastCollected(t.Context()); err == nil {
		t.Error("a missing registry deployment was not an error")
	}
}

func buildJob(name string, conditions ...batchv1.JobConditionType) *batchv1.Job {
	j := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "frankie-build", Labels: map[string]string{RoleLabel: RoleBuild}}}
	for _, c := range conditions {
		j.Status.Conditions = append(j.Status.Conditions, batchv1.JobCondition{Type: c, Status: corev1.ConditionTrue})
	}
	return j
}

func TestKubeRegistryHostBuildsActive(t *testing.T) {
	tests := []struct {
		name string
		jobs []runtime.Object
		want bool
	}{
		{"no jobs", nil, false},
		{"finished jobs only", []runtime.Object{buildJob("a", batchv1.JobComplete), buildJob("b", batchv1.JobFailed)}, false},
		{"one running", []runtime.Object{buildJob("a", batchv1.JobComplete), buildJob("c")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newRegistryHost(tt.jobs...)
			got, err := h.BuildsActive(t.Context())
			if err != nil || got != tt.want {
				t.Fatalf("BuildsActive = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

// M6 (D84): a restart sets the collection time on the pod template and waits
// until the new pod is available.
func TestKubeRegistryHostRestart(t *testing.T) {
	h, kube := newRegistryHost(registryDeployment(nil))
	gets := 0
	kube.PrependReactor("get", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		obj, err := kube.Tracker().Get(appsv1.SchemeGroupVersion.WithResource("deployments"), "frankie", "frankie-registry")
		if err != nil {
			return true, nil, err
		}
		d := obj.(*appsv1.Deployment).DeepCopy()
		// The rollout finishes on the third look.
		d.Generation = 2
		d.Status.ObservedGeneration = 2
		if gets < 3 {
			d.Status.UpdatedReplicas, d.Status.Replicas = 0, 2
		}
		return true, d, nil
	})
	kube.PrependReactor("patch", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		p := a.(k8stesting.PatchAction)
		if !strings.Contains(string(p.GetPatch()), `"`+CollectedAtAnnotation+`":"2026-11-15T12:00:00Z"`) {
			t.Errorf("patch = %s", p.GetPatch())
		}
		d := registryDeployment(map[string]string{CollectedAtAnnotation: "2026-11-15T12:00:00Z"})
		d.Generation = 2
		return true, d, nil
	})
	if err := h.Restart(t.Context(), time.Date(2026, 11, 15, 13, 0, 0, 0, time.FixedZone("CET", 3600))); err != nil {
		t.Fatal(err)
	}
	if gets != 3 {
		t.Errorf("rollout checked %d times, want until available (3)", gets)
	}
}

func TestKubeRegistryHostRestartTimesOut(t *testing.T) {
	d := registryDeployment(nil)
	d.Status.AvailableReplicas = 0
	h, _ := newRegistryHost(d)
	h.Timeout = 20 * time.Millisecond
	if err := h.Restart(t.Context(), time.Now()); err == nil {
		t.Fatal("a registry that never came back was not an error")
	}
}

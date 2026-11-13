package images

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// finishJobs makes every created Job finish the way a cluster would.
func finishJobs(kube *fake.Clientset, cond batchv1.JobConditionType, msg string) {
	kube.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		j := a.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		j.Status.Conditions = []batchv1.JobCondition{{Type: cond, Status: corev1.ConditionTrue, Reason: "Test", Message: msg}}
		return false, j, nil
	})
}

func newBuilder(kube *fake.Clientset) KubeBuilder {
	return KubeBuilder{Kube: kube, Config: testConfig(), PollInterval: time.Millisecond}
}

func TestKubeBuilderSuccess(t *testing.T) {
	kube := fake.NewClientset()
	finishJobs(kube, batchv1.JobComplete, "")
	if err := newBuilder(kube).Build(context.Background(), testRequest()); err != nil {
		t.Fatal(err)
	}
	jobs, _ := kube.BatchV1().Jobs("frankie-build").List(context.Background(), metav1.ListOptions{})
	cms, _ := kube.CoreV1().ConfigMaps("frankie-build").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 1 || len(cms.Items) != 1 {
		t.Fatalf("jobs = %d, configmaps = %d; want 1 and 1", len(jobs.Items), len(cms.Items))
	}
	if refs := cms.Items[0].OwnerReferences; len(refs) != 1 || refs[0].Kind != "Job" || refs[0].Name != jobs.Items[0].Name {
		t.Errorf("build context is not owned by the job: %v", refs)
	}
}

func TestKubeBuilderSharesAnExistingBuild(t *testing.T) {
	kube := fake.NewClientset()
	finishJobs(kube, batchv1.JobComplete, "")
	b := newBuilder(kube)
	for range 2 {
		if err := b.Build(context.Background(), testRequest()); err != nil {
			t.Fatal(err)
		}
	}
	jobs, _ := kube.BatchV1().Jobs("frankie-build").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 1 {
		t.Fatalf("jobs = %d, want the second request to join the first", len(jobs.Items))
	}
}

func TestKubeBuilderFailureIsReportedAndCleanedUp(t *testing.T) {
	kube := fake.NewClientset()
	finishJobs(kube, batchv1.JobFailed, "mise install exited 1")
	err := newBuilder(kube).Build(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "mise install exited 1") {
		t.Fatalf("err = %v, want the job's failure message", err)
	}
	jobs, _ := kube.BatchV1().Jobs("frankie-build").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 0 {
		t.Fatalf("failed job left behind, so a retry would find the same failure")
	}
}

func TestKubeBuilderWaitsForCompletionAndStopsOnCancel(t *testing.T) {
	kube := fake.NewClientset() // jobs never finish
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	beats := 0
	b := newBuilder(kube)
	b.Heartbeat = func(context.Context, ...any) { beats++ }
	if err := b.Build(ctx, testRequest()); err == nil {
		t.Fatal("expected the context error")
	}
	if beats == 0 {
		t.Error("no heartbeats while waiting")
	}
}

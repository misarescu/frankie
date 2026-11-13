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

const builtDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// finishJobs makes every created Job finish the way a cluster would. A
// completed Job leaves its pod, whose build container's termination message
// is buildctl's metadata file (result).
func finishJobs(kube *fake.Clientset, cond batchv1.JobConditionType, msg string) {
	finishJobsWithResult(kube, cond, msg, `{"containerimage.digest":"`+builtDigest+`","image.name":"frankie-registry:5000/toolchains/misarescu/repo"}`)
}

func finishJobsWithResult(kube *fake.Clientset, cond batchv1.JobConditionType, msg, result string) {
	kube.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		j := a.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		j.Status.Conditions = []batchv1.JobCondition{{Type: cond, Status: corev1.ConditionTrue, Reason: "Test", Message: msg}}
		if cond == batchv1.JobComplete {
			_ = kube.Tracker().Add(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: j.Name + "-x", Namespace: j.Namespace, Labels: map[string]string{"batch.kubernetes.io/job-name": j.Name}},
				Status: corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{{Name: buildContainer,
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Message: result}}}}},
			})
		}
		return false, j, nil
	})
}

func newBuilder(kube *fake.Clientset) KubeBuilder {
	return KubeBuilder{Kube: kube, Config: testConfig(), PollInterval: time.Millisecond}
}

func TestKubeBuilderSuccess(t *testing.T) {
	kube := fake.NewClientset()
	finishJobs(kube, batchv1.JobComplete, "")
	digest, err := newBuilder(kube).Build(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if digest != builtDigest {
		t.Errorf("digest = %q, want the one the build container reported", digest)
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
		if digest, err := b.Build(context.Background(), testRequest()); err != nil || digest != builtDigest {
			t.Fatalf("digest %q, err %v", digest, err)
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
	_, err := newBuilder(kube).Build(context.Background(), testRequest())
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
	if _, err := b.Build(ctx, testRequest()); err == nil {
		t.Fatal("expected the context error")
	}
	if beats == 0 {
		t.Error("no heartbeats while waiting")
	}
}

func TestKubeBuilderNeedsAReportedDigest(t *testing.T) {
	for name, result := range map[string]string{
		"no metadata":     "",
		"no image digest": `{"image.name":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			kube := fake.NewClientset()
			finishJobsWithResult(kube, batchv1.JobComplete, "", result)
			if digest, err := newBuilder(kube).Build(context.Background(), testRequest()); err == nil {
				t.Fatalf("digest %q accepted", digest)
			}
		})
	}
}

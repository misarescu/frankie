package images

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const builtDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// finishJobs makes every created Job finish the way a cluster would. A
// completed Job leaves its pod, whose push container's termination message
// is the build's result.
func finishJobs(kube *fake.Clientset, cond batchv1.JobConditionType, msg string) {
	finishJobsWithResult(kube, cond, msg, `{"containerimage.digest":"`+builtDigest+`","image.name":"frankie-registry:5000/toolchains/misarescu/repo"}`)
}

func finishJobsWithResult(kube *fake.Clientset, cond batchv1.JobConditionType, msg, result string) {
	kube.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		j := a.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		finishJobWithResult(kube, j, cond, msg, result)
		return false, j, nil
	})
}

func finishJob(kube *fake.Clientset, j *batchv1.Job, cond batchv1.JobConditionType, msg string) {
	finishJobWithResult(kube, j, cond, msg, `{"containerimage.digest":"`+builtDigest+`"}`)
}

// finishJobWithResult sets the Job's outcome before it is stored.
func finishJobWithResult(kube *fake.Clientset, j *batchv1.Job, cond batchv1.JobConditionType, msg, result string) {
	j.Status.Conditions = []batchv1.JobCondition{{Type: cond, Status: corev1.ConditionTrue, Reason: "Test", Message: msg, LastTransitionTime: metav1.Now()}}
	if cond == batchv1.JobComplete {
		_ = kube.Tracker().Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: j.Name + "-x", Namespace: j.Namespace, Labels: map[string]string{"batch.kubernetes.io/job-name": j.Name}},
			Status: corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{{Name: pushContainer,
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Message: result}}}}},
		})
	}
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
		t.Errorf("digest = %q, want the one the push container reported", digest)
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

var t0 = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// jobGVR is what the fake clientset's tracker files Jobs under.
var jobGVR = batchv1.SchemeGroupVersion.WithResource("jobs")

// failJob marks the tracked Job failed at failedAt.
func failJob(t *testing.T, kube *fake.Clientset, name, msg string, failedAt time.Time) {
	t.Helper()
	obj, err := kube.Tracker().Get(jobGVR, "frankie-build", name)
	if err != nil {
		t.Fatal(err)
	}
	j := obj.(*batchv1.Job).DeepCopy()
	j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
		Reason: "BackoffLimitExceeded", Message: msg, LastTransitionTime: metav1.NewTime(failedAt)}}
	if err := kube.Tracker().Update(jobGVR, j, "frankie-build"); err != nil {
		t.Fatal(err)
	}
}

// numberJobs gives every created Job a UID, as the API server would.
func numberJobs(kube *fake.Clientset) {
	n := 0
	kube.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		n++
		j := a.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		j.UID = types.UID(fmt.Sprintf("job-%d", n))
		return false, j, nil
	})
}

func listJobs(t *testing.T, kube *fake.Clientset) []batchv1.Job {
	t.Helper()
	jobs, err := kube.BatchV1().Jobs("frankie-build").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return jobs.Items
}

// M5.2: a failed build stays, so every request that waited for it sees why.
func TestKubeBuilderKeepsAFailedBuildForItsWaiters(t *testing.T) {
	kube := fake.NewClientset()
	numberJobs(kube)
	gets := 0
	kube.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		if gets++; gets == 2 {
			failJob(t, kube, testRequest().JobName(), "mise install exited 1", t0.Add(time.Minute))
		}
		return false, nil, nil
	})
	b := newBuilder(kube)
	b.Now = func() time.Time { return t0 }

	// Two requests started before the build failed: both report its failure.
	for i := range 2 {
		_, err := b.Build(context.Background(), testRequest())
		if err == nil || !strings.Contains(err.Error(), "mise install exited 1") {
			t.Fatalf("request %d: err = %v, want the job's failure message", i, err)
		}
	}
	if jobs := listJobs(t, kube); len(jobs) != 1 || jobs[0].UID != "job-1" {
		t.Fatalf("%d jobs, want the failed job kept", len(jobs))
	}
}

// M5.2: a request that starts after a build failed builds again.
func TestKubeBuilderRebuildsAfterAnEarlierFailure(t *testing.T) {
	kube := fake.NewClientset()
	finishJobs(kube, batchv1.JobComplete, "")
	numberJobs(kube)
	cm, job, err := BuildResources(testConfig(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	job.UID = "failed-job"
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
		Message: "out of memory", LastTransitionTime: metav1.NewTime(t0)}}
	cm.OwnerReferences = []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID}}
	for _, o := range []runtime.Object{job, cm} {
		if err := kube.Tracker().Add(o); err != nil {
			t.Fatal(err)
		}
	}
	b := newBuilder(kube)
	b.Now = func() time.Time { return t0.Add(5 * time.Second) }

	digest, err := b.Build(context.Background(), testRequest())
	if err != nil || digest != builtDigest {
		t.Fatalf("digest %q, err %v; want a new build", digest, err)
	}
	jobs := listJobs(t, kube)
	if len(jobs) != 1 || jobs[0].UID == "failed-job" {
		t.Fatalf("%d jobs, want only the new build", len(jobs))
	}
	got, err := kube.CoreV1().ConfigMaps("frankie-build").Get(context.Background(), job.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !ownedBy(got, jobs[0].UID) {
		t.Errorf("build context owned by %v, want the new job", got.OwnerReferences)
	}
}

// M5.2: a request whose build was replaced while it waited follows the new
// build instead of failing with "job not found" (the M5.1 acceptance run).
func TestKubeBuilderFollowsAReplacedBuild(t *testing.T) {
	for name, replace := range map[string]func(t *testing.T, kube *fake.Clientset, name string){
		"removed": func(t *testing.T, kube *fake.Clientset, name string) {
			if err := kube.Tracker().Delete(jobGVR, "frankie-build", name); err != nil {
				t.Fatal(err)
			}
		},
		"recreated": func(t *testing.T, kube *fake.Clientset, name string) {
			obj, err := kube.Tracker().Get(jobGVR, "frankie-build", name)
			if err != nil {
				t.Fatal(err)
			}
			// Another request replaced it with a build that completed.
			j := obj.(*batchv1.Job).DeepCopy()
			j.UID = "someone-else"
			finishJob(kube, j, batchv1.JobComplete, "")
			if err := kube.Tracker().Update(jobGVR, j, "frankie-build"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			kube := fake.NewClientset()
			numberJobs(kube)
			creates, gets := 0, 0
			kube.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
				// The first build keeps running; the next one completes.
				if creates++; creates > 1 {
					finishJob(kube, a.(k8stesting.CreateAction).GetObject().(*batchv1.Job), batchv1.JobComplete, "")
				}
				return false, nil, nil
			})
			kube.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
				if gets++; gets == 2 {
					replace(t, kube, testRequest().JobName())
				}
				return false, nil, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			digest, err := newBuilder(kube).Build(ctx, testRequest())
			if err != nil || digest != builtDigest {
				t.Fatalf("digest %q, err %v; want the next build's result", digest, err)
			}
		})
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

// M5.2: a failed build's error carries the end of its log, so the run's
// transcript says why the image did not build: the install's, unless the
// install succeeded and the push failed (D81).
func TestKubeBuilderFailureCarriesTheBuildLog(t *testing.T) {
	installed := []corev1.ContainerStatus{{Name: installContainer,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}}}
	installFailed := []corev1.ContainerStatus{{Name: installContainer,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}}}
	for name, tt := range map[string]struct {
		pod      bool
		statuses []corev1.ContainerStatus
		want     string
	}{
		"install failed":      {true, installFailed, "last lines of the build log (install):\nfake logs"},
		"install not started": {true, nil, "last lines of the build log (install):\nfake logs"},
		"push failed":         {true, installed, "last lines of the build log (push):\nfake logs"},
		"pod gone":            {false, nil, "(no build log: the build pod is gone)"},
	} {
		t.Run(name, func(t *testing.T) {
			kube := fake.NewClientset()
			kube.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
				j := a.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
				finishJob(kube, j, batchv1.JobFailed, "BackoffLimitExceeded")
				if tt.pod {
					_ = kube.Tracker().Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: j.Name + "-x", Namespace: j.Namespace,
						Labels: map[string]string{"batch.kubernetes.io/job-name": j.Name}},
						Status: corev1.PodStatus{InitContainerStatuses: tt.statuses}})
				}
				return false, j, nil
			})
			_, err := newBuilder(kube).Build(context.Background(), testRequest())
			if err == nil || !strings.Contains(err.Error(), "BackoffLimitExceeded") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want the failure and %q", err, tt.want)
			}
		})
	}
}

// progressLog records what a build reports.
type progressLog struct {
	building, built []string
	output          []string
	errs            []error
}

func (p *progressLog) Building(repository, tag string) {
	p.building = append(p.building, repository+":"+tag)
}
func (p *progressLog) Output(text string) { p.output = append(p.output, text) }
func (p *progressLog) Built(repository, tag string, _ time.Duration, err error) {
	p.built = append(p.built, repository+":"+tag)
	p.errs = append(p.errs, err)
}

// M6 (D93): a request waiting for a build gets its log as it grows, each
// container's in turn, never twice.
func TestKubeBuilderStreamsTheBuildLog(t *testing.T) {
	kube := fake.NewClientset()
	finishJobs(kube, batchv1.JobComplete, "")
	p := &progressLog{}
	req := testRequest()
	req.Progress = p
	if _, err := newBuilder(kube).Build(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// The fake API answers "fake logs" for each container.
	if strings.Join(p.output, "|") != "fake logs|fake logs" {
		t.Errorf("output = %q", p.output)
	}

	b := newBuilder(kube)
	jobs := listJobs(t, kube)
	c := &logCursor{seen: map[string]int{}}
	b.streamLog(context.Background(), &jobs[0], c, p)
	b.streamLog(context.Background(), &jobs[0], c, p)
	if len(p.output) != 4 {
		t.Errorf("output after two more reads = %q; each container's log once", p.output)
	}
	cut := &logCursor{seen: map[string]int{}, sent: maxStreamedLog - 4}
	b.streamLog(context.Background(), &jobs[0], cut, p)
	if last := p.output[len(p.output)-1]; !cut.cut || !strings.HasPrefix(last, "fake") || !strings.Contains(last, "the build log is longer") {
		t.Errorf("cut output = %q, cut %v", last, cut.cut)
	}
}

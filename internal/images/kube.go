package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// KubeBuilder builds images with a one-off Job per image, which appends a
// layer to the image it extends (D81; docs/spec/09: no standing build service
// on the concrete profile).
type KubeBuilder struct {
	Kube   kubernetes.Interface
	Config BuildConfig
	// PollInterval is how often the Job is checked; zero means 3s.
	PollInterval time.Duration
	// Heartbeat, when set, is called on every poll so a Temporal Activity stays alive.
	Heartbeat func(ctx context.Context, details ...any)
	// Now is the clock a request's start is taken from; nil means time.Now.
	Now func() time.Time
}

// maxRejoins bounds how often one request follows a build that another
// request replaced while it waited.
const maxRejoins = 3

// errReplaced means the Job a request waited for was removed or replaced.
var errReplaced = errors.New("build job replaced")

// Build implements Builder. The digest comes from the push container's
// termination message, as the kubelet recorded it.
//
// Requests for the same image share one Job (D78). A failed Job is kept, so
// every request that waited for it reports its failure; a request that starts
// after a Job failed replaces it and builds again. A request whose Job was
// replaced while it waited follows the new one.
func (b KubeBuilder) Build(ctx context.Context, r BuildRequest) (string, error) {
	cm, job, err := BuildResources(b.Config, r)
	if err != nil {
		return "", err
	}
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	since := now()
	for range maxRejoins + 1 {
		live, err := b.start(ctx, cm, job, since)
		if err != nil {
			return "", err
		}
		live, err = b.wait(ctx, live, r.Progress)
		if errors.Is(err, errReplaced) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, failure := jobState(live); failure != "" {
			return "", fmt.Errorf("build job %s failed: %s%s", job.Name, failure, b.failureLog(ctx, live))
		}
		return b.reportedDigest(ctx, live)
	}
	return "", fmt.Errorf("build job %s was replaced %d times while this request waited", job.Name, maxRejoins+1)
}

func (b KubeBuilder) interval() time.Duration {
	if b.PollInterval == 0 {
		return 3 * time.Second
	}
	return b.PollInterval
}

// start creates the build's ConfigMap and Job, or joins the Job another
// request created. A Job that failed before since is an earlier request's
// outcome: it is removed and the build starts again.
func (b KubeBuilder) start(ctx context.Context, cm *corev1.ConfigMap, job *batchv1.Job, since time.Time) (*batchv1.Job, error) {
	jobs := b.Kube.BatchV1().Jobs(b.Config.Namespace)
	cms := b.Kube.CoreV1().ConfigMaps(b.Config.Namespace)
	for range maxRejoins + 1 {
		if _, err := cms.Create(ctx, cm.DeepCopy(), metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("create build context: %w", err)
		}
		live, err := jobs.Create(ctx, job.DeepCopy(), metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			// Another request, or an earlier attempt of this one, started the build.
			live, err = jobs.Get(ctx, job.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				continue // removed in between: create it again
			}
		}
		if err != nil {
			return nil, fmt.Errorf("start build job: %w", err)
		}
		if failedAt, failed := failureTime(live); failed && failedAt.Before(since.Truncate(time.Second)) {
			if err := b.remove(ctx, live); err != nil {
				return nil, err
			}
			continue
		}
		if err := b.adopt(ctx, cm, live); err != nil {
			return nil, err
		}
		return live, nil
	}
	return nil, fmt.Errorf("start build job %s: it kept being replaced", job.Name)
}

// wait polls the Job until it finishes. It returns errReplaced when the Job
// is gone or another Job took its name.
func (b KubeBuilder) wait(ctx context.Context, live *batchv1.Job, progress Progress) (*batchv1.Job, error) {
	jobs := b.Kube.BatchV1().Jobs(b.Config.Namespace)
	logs := &logCursor{seen: map[string]int{}}
	for {
		if progress != nil {
			b.streamLog(ctx, live, logs, progress)
		}
		if done, _ := jobState(live); done {
			return live, nil
		}
		if b.Heartbeat != nil {
			b.Heartbeat(ctx, live.Name)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(b.interval()):
		}
		got, err := jobs.Get(ctx, live.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, errReplaced
		}
		if err != nil {
			return nil, fmt.Errorf("get build job: %w", err)
		}
		if got.UID != live.UID {
			return nil, errReplaced
		}
		live = got
	}
}

// failureTime reports whether the Job failed and when.
func failureTime(j *batchv1.Job) (time.Time, bool) {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return c.LastTransitionTime.Time, true
		}
	}
	return time.Time{}, false
}

// Bounds on the part of a failed build's log its error carries: the install
// ends with the failing command's output and the error.
const (
	buildLogTailLines = 40
	buildLogTailBytes = 6 << 10
)

// failureLog is the end of a failed build's log, introduced for the error, so
// a run's transcript says why its image did not build (M5.2). It reads the
// newest pod of the Job, from the install unless that succeeded; a log that
// cannot be read is said to be missing.
func (b KubeBuilder) failureLog(ctx context.Context, job *batchv1.Job) string {
	pods := b.Kube.CoreV1().Pods(b.Config.Namespace)
	newest, ok := b.newestPod(ctx, job)
	if !ok {
		return "\n(no build log: the build pod is gone)"
	}
	container := pushContainer
	if !succeeded(newest.Status.InitContainerStatuses, installContainer) {
		container = installContainer
	}
	raw, err := pods.GetLogs(newest.Name, &corev1.PodLogOptions{
		Container: container, TailLines: ptrTo(int64(buildLogTailLines)), LimitBytes: ptrTo(int64(buildLogTailBytes)),
	}).Do(ctx).Raw()
	if err != nil {
		return "\n(no build log: " + err.Error() + ")"
	}
	tail := strings.TrimRight(string(raw), "\n")
	if tail == "" {
		return "\n(the build log is empty)"
	}
	return "\nlast lines of the build log (" + container + "):\n" + tail
}

// Build logs streamed to a request (D93): at most this much per build.
const maxStreamedLog = 256 << 10

// logCursor is how much of each build container's log a request has seen.
type logCursor struct {
	seen map[string]int // pod/container -> bytes
	sent int
	cut  bool
}

// streamLog sends the build pod's new log output to progress: the install
// container's, then the push container's. Logs only grow, so what is past the
// cursor is new. Reading them is best effort: a build does not fail over its log.
func (b KubeBuilder) streamLog(ctx context.Context, job *batchv1.Job, c *logCursor, progress Progress) {
	if c.cut {
		return
	}
	pod, ok := b.newestPod(ctx, job)
	if !ok {
		return
	}
	for _, container := range []string{installContainer, pushContainer} {
		raw, err := b.Kube.CoreV1().Pods(b.Config.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: container, LimitBytes: ptrTo(int64(maxStreamedLog + 1)),
		}).Do(ctx).Raw()
		if err != nil {
			continue // not started yet, or gone
		}
		key := pod.Name + "/" + container
		if len(raw) <= c.seen[key] {
			continue
		}
		text := string(raw[c.seen[key]:])
		c.seen[key] = len(raw)
		if c.sent+len(text) > maxStreamedLog {
			text = text[:max(maxStreamedLog-c.sent, 0)] + "\n(the build log is longer; its end is shown if the build fails)\n"
			c.cut = true
		}
		c.sent += len(text)
		progress.Output(text)
		if c.cut {
			return
		}
	}
}

// newestPod is the Job's most recently created pod.
func (b KubeBuilder) newestPod(ctx context.Context, job *batchv1.Job) (corev1.Pod, bool) {
	list, err := b.Kube.CoreV1().Pods(b.Config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "batch.kubernetes.io/job-name=" + job.Name})
	if err != nil || len(list.Items) == 0 {
		return corev1.Pod{}, false
	}
	newest := list.Items[0]
	for _, p := range list.Items[1:] {
		if p.CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = p
		}
	}
	return newest, true
}

// succeeded reports whether the named container exited with status 0.
func succeeded(statuses []corev1.ContainerStatus, name string) bool {
	for _, s := range statuses {
		if s.Name == name {
			return s.State.Terminated != nil && s.State.Terminated.ExitCode == 0
		}
	}
	return false
}

// reportedDigest reads containerimage.digest from the termination message of
// the Job's succeeded push container.
func (b KubeBuilder) reportedDigest(ctx context.Context, job *batchv1.Job) (string, error) {
	pods, err := b.Kube.CoreV1().Pods(b.Config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "batch.kubernetes.io/job-name=" + job.Name})
	if err != nil {
		return "", fmt.Errorf("list build pods: %w", err)
	}
	for _, p := range pods.Items {
		if p.Status.Phase != corev1.PodSucceeded {
			continue
		}
		for _, c := range p.Status.ContainerStatuses {
			if c.Name != pushContainer || c.State.Terminated == nil {
				continue
			}
			var meta map[string]any
			if err := json.Unmarshal([]byte(c.State.Terminated.Message), &meta); err != nil {
				return "", fmt.Errorf("build job %s: its result is not JSON: %w", job.Name, err)
			}
			digest, _ := meta["containerimage.digest"].(string)
			if digest == "" {
				return "", fmt.Errorf("build job %s reported no image digest", job.Name)
			}
			return digest, nil
		}
	}
	return "", fmt.Errorf("build job %s: no succeeded build pod to read the digest from", job.Name)
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
// with it. A ConfigMap still owned by a failed Job that was replaced is taken
// over, and one removed in between is created again.
func (b KubeBuilder) adopt(ctx context.Context, cm *corev1.ConfigMap, job *batchv1.Job) error {
	cms := b.Kube.CoreV1().ConfigMaps(b.Config.Namespace)
	owner := []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID}}
	for range maxRejoins + 1 {
		live, err := cms.Get(ctx, cm.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			fresh := cm.DeepCopy()
			fresh.OwnerReferences = owner
			_, err = cms.Create(ctx, fresh, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				continue
			}
			return wrapErr("create build context", err)
		}
		if err != nil {
			return fmt.Errorf("get build context: %w", err)
		}
		if ownedBy(live, job.UID) {
			return nil
		}
		live.OwnerReferences = owner
		_, err = cms.Update(ctx, live, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
			continue
		}
		return wrapErr("adopt build context", err)
	}
	return fmt.Errorf("adopt build context %s: it kept changing", cm.Name)
}

func ownedBy(obj metav1.Object, uid types.UID) bool {
	for _, r := range obj.GetOwnerReferences() {
		if r.UID == uid {
			return true
		}
	}
	return false
}

func wrapErr(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}

// remove deletes a failed build and the context it owns, then waits until the
// Job is gone. Preconditions leave alone a Job, or a ConfigMap, that another
// request created or adopted meanwhile.
func (b KubeBuilder) remove(ctx context.Context, job *batchv1.Job) error {
	jobs := b.Kube.BatchV1().Jobs(b.Config.Namespace)
	cms := b.Kube.CoreV1().ConfigMaps(b.Config.Namespace)
	policy := metav1.DeletePropagationBackground
	err := jobs.Delete(ctx, job.Name, metav1.DeleteOptions{PropagationPolicy: &policy, Preconditions: &metav1.Preconditions{UID: &job.UID}})
	if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
		return fmt.Errorf("remove failed build job: %w", err)
	}
	if cm, err := cms.Get(ctx, job.Name, metav1.GetOptions{}); err == nil && ownedBy(cm, job.UID) {
		err := cms.Delete(ctx, cm.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &cm.UID, ResourceVersion: &cm.ResourceVersion}})
		if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			return fmt.Errorf("remove failed build context: %w", err)
		}
	}
	for {
		live, err := jobs.Get(ctx, job.Name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			return nil
		case err != nil:
			return fmt.Errorf("get build job: %w", err)
		case live.UID != job.UID:
			return nil // another request already started the next build
		}
		if b.Heartbeat != nil {
			b.Heartbeat(ctx, job.Name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(b.interval()):
		}
	}
}

package activities

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/sandbox"
)

const updatedHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// finishUpdatePods ends every created update pod with the exit code and report.
func finishUpdatePods(kube *fake.Clientset, created *[]*corev1.Pod, exit int32, report string) {
	kube.PrependReactor("create", "pods", func(act k8stesting.Action) (bool, runtime.Object, error) {
		p := act.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		*created = append(*created, p.DeepCopy())
		p.Status.Phase = corev1.PodSucceeded
		if exit != 0 {
			p.Status.Phase = corev1.PodFailed
		}
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: sandbox.BranchUpdateContainer,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: exit, Message: report}}}}
		return false, p, nil
	})
}

func (f *queueFixture) update(t *testing.T, exit int32, report string, method domain.MergeMethod) (BranchUpdateResult, string, []*corev1.Pod, error) {
	t.Helper()
	kube := fake.NewClientset()
	var created []*corev1.Pod
	finishUpdatePods(kube, &created, exit, report)
	f.a.Kube = kube
	id := f.step(t, "enqueue-update", domain.StepAction)
	v, err := activityEnv(f.a).ExecuteActivity(f.a.UpdateBranch, BranchUpdateRequest{Issue: f.in, RunID: f.run.RunID, StepRunID: id,
		StepID: "enqueue-update", Branch: f.run.Branch, HeadSHA: queuedHead, Method: method})
	var out BranchUpdateResult
	if err == nil {
		_ = v.Get(&out)
	}
	return out, id, created, err
}

// M6 (D87): the update pod pushes the issue's branch, and what it reports is
// checked against the provider.
func TestUpdateBranch(t *testing.T) {
	f := newQueueActivityFixture(t)
	f.prov.branches[f.run.Branch] = updatedHead // where the pod pushed
	out, id, pods, err := f.update(t, 0, "updated "+updatedHead+" "+queuedBase, domain.MergeRebase)
	if err != nil || out.Outcome != BranchUpdated || out.HeadSHA != updatedHead || out.BaseSHA != queuedBase {
		t.Fatalf("update = %+v, %v", out, err)
	}
	step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(id))
	if step.Status != domain.ExecSucceeded || step.CommitSHA != updatedHead || step.PodName != pods[0].Name {
		t.Errorf("step = %s at %q in %q", step.Status, step.CommitSHA, step.PodName)
	}
	c := pods[0].Spec.Containers[0]
	env := func(k string) string {
		for _, e := range c.Env {
			if e.Name == k {
				return e.Value
			}
		}
		return ""
	}
	if env("UPDATE_MODE") != "rebase" || env("UPDATE_HEAD") != queuedHead || env("UPDATE_BASE") != "main" ||
		env("GIT_COMMITTER_EMAIL") != "agent@frankie.invalid" || pods[0].Spec.Containers[0].Image != f.a.Agent.RunnerImage {
		t.Errorf("pod env %v image %s", c.Env, c.Image)
	}
	// The token may push, but only the issue's branches.
	grant := decodeGrant(t, f.a.Signer, env("GIT_CONFIG_VALUE_0"))
	if grant.ReadOnly || grant.IssueKey != f.in.IssueKey || grant.StepRunID != id {
		t.Errorf("grant = %+v", grant)
	}
}

func TestUpdateBranchOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		exit    int32
		report  string
		want    string
		status  domain.ExecStatus
		wantErr bool
	}{
		{"already up to date", 0, "uptodate " + queuedHead + " " + queuedBase, BranchUpToDate, domain.ExecSucceeded, false},
		{"conflict", 4, "conflict " + queuedBase + " web/App.jsx", BranchConflict, domain.ExecFailed, false},
		{"moved", 3, "moved " + updatedHead, BranchMoved, domain.ExecFailed, false},
		// The pod claims a push the provider does not show.
		{"unverified push", 0, "updated " + updatedHead + " " + queuedBase, "", "", true},
		{"no report", 1, "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueActivityFixture(t)
			out, id, _, err := f.update(t, tt.exit, tt.report, domain.MergeSquash)
			if (err != nil) != tt.wantErr || out.Outcome != tt.want {
				t.Fatalf("update = %+v, %v", out, err)
			}
			if tt.wantErr {
				return
			}
			if step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(id)); step.Status != tt.status {
				t.Errorf("step = %s, want %s", step.Status, tt.status)
			}
		})
	}
}

// decodeGrant reads the grant of the git proxy token in an extraHeader value.
func decodeGrant(t *testing.T, s *gitproxy.Signer, header string) gitproxy.Grant {
	t.Helper()
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Authorization: Basic "))
	g, err := s.Verify(strings.TrimPrefix(string(raw), "x-token:"))
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return g
}

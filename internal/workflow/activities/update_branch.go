package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/sandbox"
)

// BranchUpdateRequest is one update of a queued change request's branch with
// its base branch (D87), as one execution of a step whose StepRun exists.
type BranchUpdateRequest struct {
	Issue     IssueRunInput
	RunID     string
	StepRunID string
	StepID    string
	Branch    string
	HeadSHA   string // the commit the branch must still point to
	Method    domain.MergeMethod
}

// Branch update outcomes.
const (
	BranchUpToDate = "uptodate" // the branch already contains its base
	BranchUpdated  = "updated"  // the base was merged or rebased in and pushed
	BranchMoved    = "moved"    // the branch no longer points to HeadSHA
	BranchConflict = "conflict" // the base cannot be taken in cleanly
)

// BranchUpdateResult is what the update did.
type BranchUpdateResult struct {
	Outcome   string
	HeadSHA   string // the branch's head now
	BaseSHA   string // the base commit the branch contains
	Conflicts []string
}

// updatePod sizes the update pod: it runs only git.
var (
	updateLimits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")}
	updateCPU    = resource.MustParse("250m")
)

const updateDeadline = 10 * time.Minute

// UpdateBranch brings a queued change request's branch up to date with its
// base branch in a fresh pod that runs only git (D87): it merges the base in,
// or rebases onto it for rebase merges, and pushes only if the branch still
// points to HeadSHA. A pushed head is checked against the provider before it
// is reported. The pod is keyed by the StepRun, so a retry adopts it.
func (a *Activities) UpdateBranch(ctx context.Context, req BranchUpdateRequest) (BranchUpdateResult, error) {
	id, err := uuid.Parse(req.StepRunID)
	if err != nil {
		return BranchUpdateResult{}, temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil {
		return BranchUpdateResult{}, err
	}
	if step.Status.Terminal() {
		return BranchUpdateResult{}, temporal.NewNonRetryableApplicationError(
			"the branch update finished but its result was lost; retry the issue", "lost", nil)
	}
	if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecPending, domain.ExecRunning); err != nil && step.Status != domain.ExecRunning {
		return BranchUpdateResult{}, err
	}
	token, err := a.Signer.Issue(gitproxy.Grant{Repo: req.Issue.Repo, IssueKey: req.Issue.IssueKey, StepRunID: req.StepRunID}, updateDeadline+10*time.Minute)
	if err != nil {
		return BranchUpdateResult{}, err
	}
	mode := sandbox.UpdateMerge
	if req.Method == domain.MergeRebase {
		mode = sandbox.UpdateRebase
	}
	pod, err := sandbox.BranchUpdatePod(sandbox.BranchUpdatePodSpec{
		Name: stepPodName(req.Issue.IssueKey, req.StepID, req.StepRunID), Namespace: a.Agent.Namespace, Image: a.Agent.RunnerImage,
		RuntimeClassName: a.Agent.RuntimeClass, ServiceAccountName: a.Agent.ServiceAccount,
		RunID: req.RunID, StepRunID: req.StepRunID, IssueKey: req.Issue.IssueKey,
		Limits: updateLimits, CPURequest: updateCPU, WorkspaceSize: a.Gate.WorkspaceSize, Deadline: updateDeadline,
		EgressProxyURL: a.Agent.EgressProxyURL, NoProxy: a.Agent.NoProxy, NodeSelector: a.Agent.NodeSelector,
		CloneURL: strings.TrimSuffix(a.Agent.GitProxyURL, "/") + "/git/" + req.Issue.Repo + ".git", Token: token,
		Branch: req.Branch, HeadSHA: req.HeadSHA, BaseBranch: req.Issue.BaseBranch, Mode: mode,
		AuthorName: a.Agent.AuthorName, AuthorEmail: a.Agent.AuthorEmail,
	})
	if err != nil {
		return BranchUpdateResult{}, temporal.NewNonRetryableApplicationError("invalid branch update pod spec", "config", err)
	}
	pods := a.Kube.CoreV1().Pods(a.Agent.Namespace)
	if _, err := pods.Create(ctx, pod, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return BranchUpdateResult{}, fmt.Errorf("create branch update pod: %w", err)
	}
	if _, err := a.Store.SetStepRunPod(ctx, id, pod.Name); err != nil {
		return BranchUpdateResult{}, err
	}
	finished, err := a.waitForGatePod(ctx, pod.Name)
	if err != nil {
		return BranchUpdateResult{}, err
	}
	update, err := a.branchUpdate(ctx, finished)
	if err != nil {
		return BranchUpdateResult{}, err
	}
	if update.Outcome == BranchUpdated {
		// The report came from a pod running git on the agent's branch; the
		// provider says what the branch is.
		head, found, err := a.GitHub.BranchHead(ctx, req.Issue.Repo, req.Branch)
		if err != nil {
			return BranchUpdateResult{}, err
		}
		if !found || head != update.HeadSHA {
			return BranchUpdateResult{}, fmt.Errorf("branch update reported %s but %s is at %q", update.HeadSHA, req.Branch, head)
		}
	}
	if update.HeadSHA != "" {
		if _, err := a.Store.SetStepRunCommit(ctx, id, update.HeadSHA); err != nil {
			return BranchUpdateResult{}, err
		}
	}
	status := domain.ExecSucceeded
	if update.Outcome == BranchMoved || update.Outcome == BranchConflict {
		status = domain.ExecFailed
	}
	if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecRunning, status); err != nil {
		return BranchUpdateResult{}, err
	}
	if err := pods.Delete(context.WithoutCancel(ctx), pod.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		a.Log.Warn("branch update pod not deleted", "pod", pod.Name, "error", err)
	}
	return update, nil
}

// branchUpdate reads the update container's exit code and report.
func (a *Activities) branchUpdate(ctx context.Context, p *corev1.Pod) (BranchUpdateResult, error) {
	for _, s := range p.Status.ContainerStatuses {
		if s.Name != sandbox.BranchUpdateContainer || s.State.Terminated == nil {
			continue
		}
		u, err := sandbox.ParseBranchUpdate(s.State.Terminated.ExitCode, s.State.Terminated.Message)
		if err != nil {
			return BranchUpdateResult{}, fmt.Errorf("%w: %s", err, a.logTail(ctx, p.Name, sandbox.BranchUpdateContainer))
		}
		return BranchUpdateResult{Outcome: u.Outcome, HeadSHA: u.HeadSHA, BaseSHA: u.BaseSHA, Conflicts: u.Conflicts}, nil
	}
	if p.Status.Reason == "DeadlineExceeded" {
		return BranchUpdateResult{}, fmt.Errorf("branch update pod %s exceeded its deadline", p.Name)
	}
	return BranchUpdateResult{}, fmt.Errorf("branch update pod %s ended without a result", p.Name)
}

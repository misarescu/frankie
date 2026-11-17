package activities

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/e2e"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/sandbox"
)

// releaseIssueKey labels release verification pods and grants: they belong to
// a release, not to one issue.
const releaseIssueKey = "release"

// e2eLogLimit bounds how much of a verification pod's log is read: the suite's
// output and its JUnit results.
const e2eLogLimit = 4 << 20

// SuiteVerifier runs a project's end-to-end suite against a stage (verify:
// e2e-suite, D116): the e2e command of `.factory/config.yaml` at the release's
// commit, in that commit's toolchain image, in a sandboxed gate pod that checks
// the commit out from the base branch with a read-only token. The suite's exit
// code, which the kubelet records, decides the verdict (invariant 2); its JUnit
// results, read back from the pod's log, name the failing tests and the issues
// they are tagged with. The stage's host joins the project's egress hosts for
// the pod.
type SuiteVerifier struct {
	A *Activities
}

// Verify implements Verifier. Idempotent: the pod is named after the
// deployment, and a retry adopts it.
func (v SuiteVerifier) Verify(ctx context.Context, req VerifyRequest) (Verification, error) {
	a := v.A
	if req.Kind != config.VerifyE2ESuite {
		return Verification{}, temporal.NewNonRetryableApplicationError("unknown verification "+req.Kind, "config", nil)
	}
	depID, err := uuid.Parse(req.DeploymentID)
	if err != nil {
		return Verification{}, temporal.NewNonRetryableApplicationError("bad deployment id", "input", err)
	}
	stage, err := url.Parse(req.URL)
	if err != nil || stage.Hostname() == "" {
		return Verification{}, temporal.NewNonRetryableApplicationError("stage "+req.Stage+" has no url", "config", err)
	}
	project, err := a.Store.GetProjectByName(ctx, req.Repo)
	if err != nil {
		return Verification{}, err
	}
	cfg, err := a.projectConfig(ctx, req.Repo, req.SHA)
	if errors.Is(err, errInvalidProjectConfig) {
		return Verification{Inconclusive: true, Summary: err.Error()}, nil
	}
	if err != nil {
		return Verification{}, err
	}
	if cfg.E2E == nil {
		return Verification{Inconclusive: true, Summary: "the release's " + config.ProjectConfigPath + " has no e2e.command"}, nil
	}

	// A release is on the base branch; a preview's head on its agent branch.
	issueKey, runID, preview := cmp.Or(req.IssueKey, releaseIssueKey), cmp.Or(req.RunID, req.ReleaseID), req.Branch != ""
	in := IssueRunInput{Repo: req.Repo, IssueKey: issueKey, BaseBranch: cmp.Or(req.BaseBranch, "main"),
		EgressAllow: append(append([]string{}, cfg.Egress.Allow...), stage.Hostname())}
	image, err := a.toolchainImage(ctx, in, req.SHA, nil)
	if errors.Is(err, errInvalidProjectConfig) {
		return Verification{Inconclusive: true, Summary: err.Error()}, nil
	}
	if err != nil {
		return Verification{}, fmt.Errorf("toolchain image for %s: %w", req.SHA, err)
	}
	image = cmp.Or(image, a.Agent.RunnerImage)
	deadline := cmp.Or(a.Gate.Deadline, 30*time.Minute)
	token, err := a.Signer.Issue(gitproxy.Grant{Repo: req.Repo, IssueKey: issueKey, StepRunID: req.DeploymentID, ReadOnly: true}, deadline+10*time.Minute)
	if err != nil {
		return Verification{}, err
	}
	proxy, err := a.egressProxyURL(in, deadline+10*time.Minute)
	if err != nil {
		return Verification{}, err
	}
	env := map[string]string{}
	for k, val := range a.Agent.Env {
		env[k] = val
	}
	env["E2E_BASE_URL"] = strings.TrimSuffix(req.URL, "/")
	env["E2E_RESULTS"] = path.Join(sandbox.GateRepoDir, cfg.E2E.ResultsPath())
	env["E2E_STAGE"] = req.Stage
	env["E2E_COMMIT"] = req.SHA

	name := "e2e-" + req.Stage + "-" + strings.ReplaceAll(depID.String(), "-", "")[20:]
	pod, err := sandbox.GatePod(sandbox.GatePodSpec{
		Name: name, Namespace: a.Agent.Namespace, Image: image,
		RuntimeClassName: a.Agent.RuntimeClass, ServiceAccountName: a.Agent.ServiceAccount,
		RunID: runID, StepRunID: req.DeploymentID, IssueKey: issueKey,
		Limits: a.Gate.Limits, CPURequest: a.Gate.CPURequest, WorkspaceSize: a.Gate.WorkspaceSize, Deadline: deadline,
		EgressProxyURL: proxy, NoProxy: a.Agent.NoProxy, NodeSelector: a.Agent.NodeSelector, Tolerations: a.Agent.Tolerations,
		GuestMemoryHeadroom: a.Agent.GuestMemoryHeadroom,
		CloneURL:            strings.TrimSuffix(a.Agent.GitProxyURL, "/") + "/git/" + req.Repo + ".git", Token: token,
		Branch: cmp.Or(req.Branch, in.BaseBranch), Released: !preview, Verification: true, CommitSHA: req.SHA,
		Commands: []string{e2e.Script(cfg.E2E.Command, "E2E_RESULTS")}, Env: env,
	})
	if err != nil {
		return Verification{}, temporal.NewNonRetryableApplicationError("invalid verification pod spec: "+err.Error(), "config", err)
	}
	pods := a.Kube.CoreV1().Pods(a.Agent.Namespace)
	if _, err := pods.Create(ctx, pod, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return Verification{}, fmt.Errorf("create verification pod: %w", err)
	}
	finished, err := a.waitForGatePod(ctx, pod.Name)
	if err != nil {
		return Verification{}, err
	}
	out := a.suiteVerdict(ctx, finished, project.KeyPrefix, cfg.E2E.Command)
	if err := pods.Delete(context.WithoutCancel(ctx), pod.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		a.Log.Warn("verification pod not deleted", "pod", pod.Name, "error", err)
	}
	return out, nil
}

// suiteVerdict reads a finished verification pod: the suite's exit code
// decides; its results name the failures.
func (a *Activities) suiteVerdict(ctx context.Context, p *corev1.Pod, keyPrefix, command string) Verification {
	status := map[string]corev1.ContainerStatus{}
	for _, s := range p.Status.InitContainerStatuses {
		status[s.Name] = s
	}
	if c, ok := status[sandbox.GateCloneContainer]; !ok || c.State.Terminated == nil || c.State.Terminated.ExitCode != 0 {
		return Verification{Inconclusive: true, Summary: "checking out the release failed: " + lastLine(a.logTail(ctx, p.Name, sandbox.GateCloneContainer))}
	}
	suite, ok := status[sandbox.GateCommandContainer(0)]
	if !ok || suite.State.Terminated == nil {
		reason := "the e2e suite did not run"
		if p.Status.Reason == "DeadlineExceeded" {
			reason = "the e2e suite exceeded its deadline"
		}
		return Verification{Inconclusive: true, Summary: reason}
	}
	code := suite.State.Terminated.ExitCode
	log, err := a.Kube.CoreV1().Pods(a.Agent.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{
		Container: sandbox.GateCommandContainer(0), LimitBytes: ptr(int64(e2eLogLimit)),
	}).Do(ctx).Raw()
	if err != nil {
		log = []byte("(log unavailable: " + err.Error() + ")")
	}
	return verdict(code, log, keyPrefix, command)
}

// verdict judges a suite from its exit code and its log (D116): exit 0 passes;
// otherwise the JUnit results name the failures, and a run whose failing
// tests all errored, or that wrote no results, is inconclusive.
func verdict(code int32, log []byte, keyPrefix, command string) Verification {
	raw, found := e2e.Results(log)
	var cases []e2e.Case
	parseErr := ""
	if found {
		var err error
		if cases, err = e2e.ParseJUnit(raw); err != nil {
			parseErr = err.Error()
		}
	}
	total, failed, errored := 0, 0, 0
	var failures []TestFailure
	for _, c := range cases {
		if c.Outcome == e2e.Skipped {
			continue
		}
		total++
		if c.Outcome == e2e.Passed {
			continue
		}
		if c.Outcome == e2e.Failed {
			failed++
		} else {
			errored++
		}
		failures = append(failures, TestFailure{Test: c.Title(), IssueKeys: e2e.IssueKeys(keyPrefix, c.Suite, c.Name), Message: c.Message})
	}
	if code == 0 {
		if total == 0 {
			return Verification{Passed: true, Summary: "the e2e suite passed"}
		}
		return Verification{Passed: true, Summary: fmt.Sprintf("%d tests passed", total)}
	}
	switch {
	case !found:
		return Verification{Inconclusive: true, Summary: fmt.Sprintf("`%s` exited %d without writing its results: %s", command, code, lastLine(e2e.Before(log)))}
	case parseErr != "":
		return Verification{Inconclusive: true, Summary: fmt.Sprintf("`%s` exited %d and its results could not be read: %s", command, code, parseErr)}
	case failed == 0 && errored > 0:
		return Verification{Inconclusive: true, Failures: failures,
			Summary: fmt.Sprintf("%d of %d tests could not run (e.g. %s: %s)", errored, total, failures[0].Test, failures[0].Message)}
	case failed == 0:
		return Verification{Summary: fmt.Sprintf("`%s` exited %d with no failing test", command, code)}
	}
	return Verification{Failures: failures, Summary: fmt.Sprintf("%d of %d tests failed", failed+errored, total)}
}

// lastLine is the last non-empty line of s, for a one-line reason.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

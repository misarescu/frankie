package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gateway"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/sandbox"
	"github.com/misarescu/frankie/internal/store"
)

// AgentConfig describes how agent steps run.
type AgentConfig struct {
	Namespace      string
	RunnerImage    string
	RuntimeClass   string
	ServiceAccount string
	EgressProxyURL string
	NoProxy        []string
	Limits         corev1.ResourceList
	WorkspaceSize  resource.Quantity
	PodDeadline    time.Duration
	GitProxyURL    string // as agents reach it, e.g. http://frankie-controlplane.frankie.svc:8081
	GatewayURL     string // as agents reach it, e.g. http://frankie-llm-gateway.frankie.svc:4000
	Model          string
	BudgetUSD      float64
	SessionTimeout time.Duration
	IdleTimeout    time.Duration
	AuthorName     string
	AuthorEmail    string
}

// Activities do the workflow's I/O. Every Activity is idempotent.
type Activities struct {
	Store   *store.Store
	Kube    kubernetes.Interface
	Gateway *gateway.Client
	Signer  *gitproxy.Signer
	GitHub  *github.Client
	Agent   AgentConfig
	Log     *slog.Logger
}

// hardcodedWorkflow is the snapshot recorded on M1 runs (invariant 4).
var hardcodedWorkflow = json.RawMessage(`{"name":"m1-issue-run","version":"1","steps":[{"id":"implement","kind":"agent"},{"id":"open-change-request","kind":"action"}]}`)

// StartRun creates the Run and its implement StepRun and marks the issue running.
func (a *Activities) StartRun(ctx context.Context, in IssueRunInput) (StartRunResult, error) {
	issueID, err := uuid.Parse(in.IssueID)
	if err != nil {
		return StartRunResult{}, temporal.NewNonRetryableApplicationError("bad issue id", "input", err)
	}
	// One Run per Temporal execution: a retried attempt finds the Run it created.
	// The workflow ID is reused by reruns of the same issue, so it cannot be the key.
	exec := activity.GetInfo(ctx).WorkflowExecution
	run, err := a.Store.GetRunByTemporalRunID(ctx, exec.RunID)
	if errors.Is(err, store.ErrNotFound) {
		run, err = a.Store.CreateRun(ctx, store.NewRun{IssueID: issueID, WorkflowSnapshot: hardcodedWorkflow,
			WorkflowVersion: "m1-1", TemporalWorkflowID: exec.ID, TemporalRunID: exec.RunID})
	}
	if err != nil {
		return StartRunResult{}, err
	}
	if _, err := a.Store.TransitionIssue(ctx, issueID, domain.IssueReady, domain.IssueRunning); err != nil {
		return StartRunResult{}, err
	}
	if _, err := a.Store.TransitionRun(ctx, run.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		return StartRunResult{}, err
	}
	step, err := a.Store.CreateStepRun(ctx, store.NewStepRun{RunID: run.ID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if errors.Is(err, store.ErrConflict) {
		var steps []domain.StepRun
		if steps, err = a.Store.ListStepRuns(ctx, run.ID); err == nil && len(steps) > 0 {
			step = steps[0]
		}
	}
	if err != nil {
		return StartRunResult{}, err
	}
	return StartRunResult{RunID: run.ID.String(), StepRunID: step.ID.String(), Branch: BranchName(in.IssueKey, in.Title)}, nil
}

// podName is stable per step run, so a retried launch finds its pod.
func podName(in IssueRunInput, run StartRunResult) string {
	return fmt.Sprintf("%s-implement-%s", in.IssueKey, strings.ReplaceAll(run.StepRunID, "-", "")[24:])
}

// LaunchAgentPod creates the sandboxed agent pod and waits for its IP.
func (a *Activities) LaunchAgentPod(ctx context.Context, in IssueRunInput, run StartRunResult) (AgentPod, error) {
	spec, err := sandbox.AgentPod(sandbox.PodSpec{
		Name: podName(in, run), Namespace: a.Agent.Namespace, Image: a.Agent.RunnerImage,
		RuntimeClassName: a.Agent.RuntimeClass, ServiceAccountName: a.Agent.ServiceAccount,
		RunID: run.RunID, StepRunID: run.StepRunID, IssueKey: in.IssueKey,
		Limits: a.Agent.Limits, WorkspaceSize: a.Agent.WorkspaceSize, Deadline: a.Agent.PodDeadline,
		EgressProxyURL: a.Agent.EgressProxyURL, NoProxy: a.Agent.NoProxy,
	})
	if err != nil {
		return AgentPod{}, temporal.NewNonRetryableApplicationError("invalid agent pod spec", "config", err)
	}
	pods := a.Kube.CoreV1().Pods(a.Agent.Namespace)
	if _, err := pods.Create(ctx, spec, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return AgentPod{}, fmt.Errorf("create agent pod: %w", err)
	}
	for {
		p, err := pods.Get(ctx, spec.Name, metav1.GetOptions{})
		if err != nil {
			return AgentPod{}, fmt.Errorf("get agent pod: %w", err)
		}
		if p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" && podReady(p) {
			stepID, _ := uuid.Parse(run.StepRunID)
			if _, err := a.Store.SetStepRunPod(ctx, stepID, p.Name); err != nil {
				return AgentPod{}, err
			}
			if _, err := a.Store.TransitionStepRun(ctx, stepID, domain.ExecPending, domain.ExecRunning); err != nil {
				return AgentPod{}, err
			}
			return AgentPod{Name: p.Name, IP: p.Status.PodIP}, nil
		}
		if p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded {
			return AgentPod{Name: p.Name}, fmt.Errorf("agent pod ended before the session: %s", p.Status.Phase)
		}
		for _, s := range p.Status.ContainerStatuses {
			if w := s.State.Waiting; w != nil && (w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull" || w.Reason == "CreateContainerConfigError") {
				return AgentPod{Name: p.Name}, temporal.NewNonRetryableApplicationError(w.Reason+": "+w.Message, "pod", nil)
			}
		}
		activity.RecordHeartbeat(ctx, p.Status.Phase)
		select {
		case <-ctx.Done():
			return AgentPod{Name: p.Name}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.ContainerStatuses {
		if !c.Ready {
			return false
		}
	}
	return len(p.Status.ContainerStatuses) > 0
}

// DeleteAgentPod removes the pod; a missing pod is fine.
func (a *Activities) DeleteAgentPod(ctx context.Context, name string) error {
	err := a.Kube.CoreV1().Pods(a.Agent.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// sessionProgress is the heartbeat: how far the event stream was persisted.
type sessionProgress struct {
	Started bool
	LastSeq int64
}

// RunAgentSession starts the harness through the runner, persists every event,
// and returns the done event. On a retry it resumes the stream after the last
// persisted event instead of starting the agent again.
func (a *Activities) RunAgentSession(ctx context.Context, in IssueRunInput, run StartRunResult, pod AgentPod) (SessionResult, error) {
	var progress sessionProgress
	if activity.HasHeartbeatDetails(ctx) {
		_ = activity.GetHeartbeatDetails(ctx, &progress)
	}
	runID, _ := uuid.Parse(run.RunID)
	stepID, _ := uuid.Parse(run.StepRunID)

	conn, err := grpc.NewClient(net.JoinHostPort(pod.IP, fmt.Sprint(sandbox.RunnerPort)), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return SessionResult{}, err
	}
	defer func() { _ = conn.Close() }()
	runner := runnerv1.NewRunnerServiceClient(conn)

	var vkey string
	if !progress.Started {
		// Credentials go to the runner over gRPC, never into the pod spec or the
		// workflow history.
		// Aliases are unique in the gateway; each attempt mints its own key.
		vkey, err = a.Gateway.CreateKey(ctx, gateway.KeyRequest{RunID: run.RunID,
			Alias:  fmt.Sprintf("run-%s-attempt-%d", run.RunID, activity.GetInfo(ctx).Attempt),
			Models: []string{a.Agent.Model}, MaxBudgetUSD: a.Agent.BudgetUSD, TTL: a.Agent.SessionTimeout + 10*time.Minute})
		if err != nil {
			return SessionResult{}, err
		}
		started := false
		defer func() {
			if !started { // the session never got the key; do not leave it valid
				if err := a.Gateway.DeleteKey(context.WithoutCancel(ctx), vkey); err != nil {
					a.Log.Warn("delete unused virtual key", "run", run.RunID, "error", err)
				}
			}
		}()
		token, err := a.Signer.Issue(gitproxy.Grant{Repo: in.Repo, IssueKey: in.IssueKey, StepRunID: run.StepRunID}, a.Agent.SessionTimeout+10*time.Minute)
		if err != nil {
			return SessionResult{}, err
		}
		_, err = runner.Start(ctx, &runnerv1.StartRequest{
			StepRunId: run.StepRunID, Harness: runnerv1.Harness_HARNESS_CLAUDE_CODE, Model: a.Agent.Model,
			SystemPrompt: SystemPrompt(),
			Env: map[string]string{
				"ANTHROPIC_BASE_URL":   a.Agent.GatewayURL,
				"ANTHROPIC_AUTH_TOKEN": vkey,
				// Background tasks must stay on a model the key allows.
				"ANTHROPIC_DEFAULT_HAIKU_MODEL": a.Agent.Model,
				"ANTHROPIC_SMALL_FAST_MODEL":    a.Agent.Model,
			},
			Timeout:     durationpb.New(a.Agent.SessionTimeout),
			IdleTimeout: durationpb.New(a.Agent.IdleTimeout),
			Workspace: &runnerv1.Workspace{
				CloneUrl: strings.TrimSuffix(a.Agent.GitProxyURL, "/") + "/git/" + in.Repo + ".git", Token: token,
				BaseRef: in.BaseBranch, Branch: run.Branch, CommitMessage: in.IssueKey + ": " + in.Title,
				AuthorName: a.Agent.AuthorName, AuthorEmail: a.Agent.AuthorEmail,
			},
		})
		// Only "already started" means an earlier attempt got this far; any other
		// failure (e.g. the workspace clone) is the real error.
		if err != nil && !isPrecondition(err, "already started") {
			return SessionResult{}, fmt.Errorf("runner start: %w", err)
		}
		if _, err := runner.Prompt(ctx, &runnerv1.PromptRequest{Text: Prompt(in, run.Branch)}); err != nil && !isPrecondition(err, "already prompted") {
			return SessionResult{}, fmt.Errorf("runner prompt: %w", err)
		}
		started = true
		progress.Started = true
		activity.RecordHeartbeat(ctx, progress)
	}

	stream, err := runner.Events(ctx, &runnerv1.EventsRequest{AfterSeq: progress.LastSeq})
	if err != nil {
		return SessionResult{}, err
	}
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return SessionResult{}, errors.New("runner stream ended without a done event")
		}
		if err != nil {
			return SessionResult{}, fmt.Errorf("runner events: %w", err)
		}
		e := resp.GetEvent()
		typ := domainEventType(e.GetType())
		if _, err := a.Store.AppendEvent(ctx, store.NewEvent{RunID: &runID, StepRunID: &stepID, Ts: e.GetTime().AsTime(), Type: typ, Payload: e.GetPayload()}); err != nil {
			return SessionResult{}, err
		}
		progress.LastSeq = e.GetSeq()
		activity.RecordHeartbeat(ctx, progress)
		if typ != domain.EventDone {
			continue
		}
		var done struct {
			Status, Reason, Result, Branch string
			CommitSHA                      string `json:"commit_sha"`
			Pushed                         bool
		}
		if err := json.Unmarshal(e.GetPayload(), &done); err != nil {
			return SessionResult{}, temporal.NewNonRetryableApplicationError("bad done payload", "runner", err)
		}
		if vkey != "" {
			if err := a.Gateway.DeleteKey(ctx, vkey); err != nil {
				a.Log.Warn("delete virtual key", "run", run.RunID, "error", err)
			}
		}
		if _, err := a.Store.SetStepRunCommit(ctx, stepID, done.CommitSHA); err != nil && done.CommitSHA != "" {
			return SessionResult{}, err
		}
		return SessionResult{Status: done.Status, Reason: done.Reason, Result: done.Result, Branch: done.Branch, CommitSHA: done.CommitSHA, Pushed: done.Pushed}, nil
	}
}

// isPrecondition reports a FailedPrecondition whose message contains phrase.
func isPrecondition(err error, phrase string) bool {
	st := status.Convert(err)
	return st.Code() == codes.FailedPrecondition && strings.Contains(st.Message(), phrase)
}

var domainEventTypes = map[runnerv1.EventType]domain.EventType{
	runnerv1.EventType_EVENT_TYPE_ASSISTANT_MESSAGE: domain.EventAssistantMessage,
	runnerv1.EventType_EVENT_TYPE_TOOL_CALL:         domain.EventToolCall,
	runnerv1.EventType_EVENT_TYPE_TOOL_RESULT:       domain.EventToolResult,
	runnerv1.EventType_EVENT_TYPE_FILE_DIFF:         domain.EventFileDiff,
	runnerv1.EventType_EVENT_TYPE_COMMAND_OUTPUT:    domain.EventCommandOutput,
	runnerv1.EventType_EVENT_TYPE_TOKEN_USAGE:       domain.EventTokenUsage,
	runnerv1.EventType_EVENT_TYPE_ERROR:             domain.EventError,
	runnerv1.EventType_EVENT_TYPE_DONE:              domain.EventDone,
}

func domainEventType(t runnerv1.EventType) domain.EventType {
	if d, ok := domainEventTypes[t]; ok {
		return d
	}
	return domain.EventError
}

// OpenChangeRequest opens (or finds) the PR for the pushed branch and records it.
func (a *Activities) OpenChangeRequest(ctx context.Context, in IssueRunInput, run StartRunResult, s SessionResult) (string, error) {
	pr, err := a.GitHub.OpenPullRequest(ctx, in.Repo, s.Branch, in.BaseBranch, in.IssueKey+": "+in.Title, PRBody(in, s))
	if err != nil {
		return "", err
	}
	issueID, _ := uuid.Parse(in.IssueID)
	runID, _ := uuid.Parse(run.RunID)
	head := s.CommitSHA
	if pr.Head != "" {
		head = pr.Head
	}
	if _, err := a.Store.UpsertChangeRequest(ctx, store.NewChangeRequest{IssueID: issueID, RunID: runID, Repo: in.Repo,
		Number: pr.Number, ProviderRef: pr.URL, Branch: s.Branch, HeadSHA: head}); err != nil {
		return "", err
	}
	return pr.URL, nil
}

// FinishRun records the outcome on the step, the run and the issue. M1 has no
// merge queue, so a successful issue waits for a human on its PR.
func (a *Activities) FinishRun(ctx context.Context, in FinishInput) error {
	issueID, _ := uuid.Parse(in.IssueID)
	runID, _ := uuid.Parse(in.RunID)
	stepID, _ := uuid.Parse(in.StepRunID)
	stepStatus, runStatus, issueStatus := domain.ExecFailed, domain.ExecFailed, domain.IssueFailed
	if in.Succeeded {
		stepStatus, runStatus, issueStatus = domain.ExecSucceeded, domain.ExecSucceeded, domain.IssueAwaitingHuman
	}
	if step, err := a.Store.GetStepRun(ctx, stepID); err == nil && !step.Status.Terminal() {
		if _, err := a.Store.TransitionStepRun(ctx, stepID, step.Status, stepStatus); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
			return err
		}
	}
	if _, err := a.Store.TransitionRun(ctx, runID, domain.ExecRunning, runStatus); err != nil {
		return err
	}
	if _, err := a.Store.TransitionIssue(ctx, issueID, domain.IssueRunning, issueStatus); err != nil {
		return err
	}
	if !in.Succeeded {
		a.Log.Warn("run failed", "run", in.RunID, "reason", in.Reason)
		// The transcript must say why the run ended, not only that it did.
		payload, _ := json.Marshal(map[string]string{"message": "run failed", "detail": in.Reason})
		if _, err := a.Store.AppendEvent(ctx, store.NewEvent{RunID: &runID, StepRunID: &stepID, Type: domain.EventError, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
}

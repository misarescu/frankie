package activities

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gateway"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/runner/keepalive"
	"github.com/misarescu/frankie/internal/sandbox"
	"github.com/misarescu/frankie/internal/store"
)

// sessionProgress is the heartbeat: how far the event stream was persisted.
type sessionProgress struct {
	Started bool
	LastSeq int64
	// KeyAlias names the session's virtual key, and KeyBudget is its cap; a
	// retried attempt reads the key's spend by its alias (D53). The key
	// itself never goes into heartbeat details.
	KeyAlias  string      `json:",omitempty"`
	KeyBudget float64     `json:",omitempty"`
	Tokens    tokenTotals `json:",omitempty"`
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

	conn, runner, err := a.runnerConn(pod)
	if err != nil {
		return SessionResult{}, err
	}
	defer func() { _ = conn.Close() }()
	// Calls to the runner end, and the session fails, as soon as the agent pod
	// can no longer run it (D102); otherwise a lost sandbox leaves the event
	// stream waiting for hours.
	rctx, stopRunner := context.WithCancelCause(ctx)
	defer stopRunner(nil)
	if a.Kube != nil && pod.Name != "" {
		go func() {
			if err := watchAgentPod(rctx, a.Kube.CoreV1().Pods(a.Agent.Namespace), pod.Name, cmp.Or(a.SessionPodCheck, sessionPodCheck)); err != nil {
				stopRunner(err)
			}
		}()
	}

	set := a.sessionSettings(run)
	harness, err := runnerHarness(set.harness)
	if err != nil {
		return SessionResult{}, temporal.NewNonRetryableApplicationError(err.Error(), "harness", err)
	}

	var vkey string
	if !progress.Started {
		keyBudget, exhausted, err := a.sessionBudget(ctx, in, run, set)
		if err != nil {
			return SessionResult{}, err
		}
		if exhausted != "" {
			return SessionResult{Status: "failed", Reason: exhausted}, nil
		}
		// Credentials go to the runner over gRPC, never into the pod spec or the
		// workflow history.
		// Aliases are unique in the gateway; each attempt mints its own key.
		alias := fmt.Sprintf("run-%s-step-%s-attempt-%d", run.RunID, run.StepRunID, activity.GetInfo(ctx).Attempt)
		vkey, err = a.Gateway.CreateKey(ctx, gateway.KeyRequest{RunID: run.RunID, Alias: alias,
			Models: []string{set.model}, MaxBudgetUSD: keyBudget, TTL: set.timeout + 10*time.Minute})
		if err != nil {
			return SessionResult{}, err
		}
		progress.KeyAlias, progress.KeyBudget = alias, keyBudget
		started := false
		defer func() {
			if !started { // the session never got the key; do not leave it valid
				if err := a.Gateway.DeleteKey(context.WithoutCancel(ctx), vkey); err != nil {
					a.Log.Warn("delete unused virtual key", "run", run.RunID, "error", err)
				}
			}
		}()
		start, err := a.startPoint(ctx, in, run.Branch)
		if err != nil {
			return SessionResult{}, err
		}
		token, err := a.Signer.Issue(a.sessionGrant(in, run, set), set.timeout+10*time.Minute)
		if err != nil {
			return SessionResult{}, err
		}
		_, err = runner.Start(rctx, a.startRequest(in, run, set, harness, start, token, vkey))
		if ended := podEndedCause(rctx); ended != nil {
			return lostRunner(ended), nil
		}
		// Only "already started" means an earlier attempt got this far; any other
		// failure (e.g. the workspace clone) is the real error.
		if err != nil && !isPrecondition(err, "already started") {
			return SessionResult{}, fmt.Errorf("runner start: %w", err)
		}
		prompt := Prompt(in, run.Branch, start.Continuing)
		if run.Step != nil {
			answers, err := a.issueAnswers(ctx, in.IssueID)
			if err != nil {
				return SessionResult{}, err
			}
			prompt = StepPrompt(in, run.Branch, start.Continuing, *run.Step, answers)
		}
		_, err = runner.Prompt(rctx, &runnerv1.PromptRequest{Text: prompt})
		if ended := podEndedCause(rctx); ended != nil {
			return lostRunner(ended), nil
		}
		if err != nil && !isPrecondition(err, "already prompted") {
			return SessionResult{}, fmt.Errorf("runner prompt: %w", err)
		}
		started = true
		progress.Started = true
		activity.RecordHeartbeat(ctx, progress)
	}

	stream, err := runner.Events(rctx, &runnerv1.EventsRequest{AfterSeq: progress.LastSeq})
	if ended := podEndedCause(rctx); ended != nil {
		return a.lostStartedRunner(ctx, stepID, progress, vkey, ended), nil
	}
	if err != nil {
		return SessionResult{}, err
	}
	beat := &sessionHeartbeat{progress: progress, record: func(p sessionProgress) { activity.RecordHeartbeat(ctx, p) }}
	keepCtx, stopKeepAlive := context.WithCancel(ctx)
	defer stopKeepAlive()
	go beat.keepAlive(keepCtx, sessionKeepAlive)
	for {
		resp, err := stream.Recv()
		if ended := podEndedCause(rctx); ended != nil {
			return a.lostStartedRunner(ctx, stepID, progress, vkey, ended), nil
		}
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
		if typ == domain.EventQuestion {
			if err := a.openQuestion(ctx, stepID, e.GetPayload()); err != nil {
				return SessionResult{}, err
			}
		}
		progress.LastSeq = e.GetSeq()
		if typ == domain.EventTokenUsage {
			progress.Tokens.observe(e.GetPayload())
		}
		beat.set(progress)
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
		// Read the key's spend before revoking it: the gateway forgets a deleted key.
		var spend float64
		if progress.KeyAlias != "" {
			spend = a.recordUsage(ctx, stepID, progress.KeyAlias, progress.Tokens)
		}
		if vkey != "" {
			if err := a.Gateway.DeleteKey(ctx, vkey); err != nil {
				a.Log.Warn("delete virtual key", "run", run.RunID, "error", err)
			}
		}
		if _, err := a.Store.SetStepRunCommit(ctx, stepID, done.CommitSHA); err != nil && done.CommitSHA != "" {
			return SessionResult{}, err
		}
		reason := cmp.Or(budgetReason(done.Status, spend, progress.KeyBudget), done.Reason)
		pushed := done.Pushed
		if !pushed && !set.readOnly && done.Status == "succeeded" {
			carried, err := a.carriedOver(ctx, in, runID, stepID, done.CommitSHA)
			if err != nil {
				return SessionResult{}, err
			}
			if carried {
				a.Log.Info("agent step continues the branch's earlier work", "run", run.RunID, "step", run.StepRunID, "commit", done.CommitSHA)
				pushed = true
			}
		}
		return SessionResult{Status: done.Status, Reason: reason, Result: done.Result, Branch: done.Branch, CommitSHA: done.CommitSHA, Pushed: pushed}, nil
	}
}

// carriedOver reports whether a coding session that pushed nothing ended on
// work an earlier run of the issue left on its branch: no other step of this
// run recorded the commit, and the commit changes files over the base branch.
// A retried run then reviews and gates that work instead of failing because
// its agent found it already done (M5.2). A session that adds nothing to its
// own run's earlier push, after a loop back, still made no changes; a session
// that started from the base branch ends on it, which changes nothing.
func (a *Activities) carriedOver(ctx context.Context, in IssueRunInput, runID, stepID uuid.UUID, sha string) (bool, error) {
	if sha == "" {
		return false, nil
	}
	steps, err := a.Store.ListStepRuns(ctx, runID)
	if err != nil {
		return false, err
	}
	for _, s := range steps {
		if s.ID != stepID && s.CommitSHA == sha {
			return false, nil
		}
	}
	files, _, err := a.GitHub.Compare(ctx, in.Repo, in.BaseBranch, sha)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

// sessionSettings is how one agent session runs: the definition's step when
// there is one (Interpret), else the platform's M2 settings (IssueRun).
type sessionSettings struct {
	model, systemPrompt string
	harness             string
	budget              float64
	timeout             time.Duration
	allowedTools        []string
	readOnly            bool // a reviewer: read-only token and workspace
	askHuman            bool
}

func (a *Activities) sessionSettings(run StartRunResult) sessionSettings {
	set := sessionSettings{model: a.Agent.Model, budget: a.Agent.BudgetUSD, timeout: a.Agent.SessionTimeout, systemPrompt: SystemPrompt()}
	if s := run.Step; s != nil {
		set.model, set.budget, set.timeout = cmp.Or(s.Model, set.model), cmp.Or(s.BudgetUSD, set.budget), cmp.Or(s.Timeout, set.timeout)
		set.systemPrompt, set.allowedTools, set.readOnly = s.SystemPrompt, s.AllowedTools, s.Contract != ""
		set.harness, set.askHuman = s.Harness, s.AskHuman
	}
	return set
}

// sessionGrant is the git proxy grant of the session's token (D21): push to the
// issue's agent branches, or read only for a reviewer.
func (a *Activities) sessionGrant(in IssueRunInput, run StartRunResult, set sessionSettings) gitproxy.Grant {
	return gitproxy.Grant{Repo: in.Repo, IssueKey: in.IssueKey, StepRunID: run.StepRunID, ReadOnly: set.readOnly}
}

// startRequest is the runner's Start for the session. Credentials travel only
// here, over gRPC, never in the pod spec or the workflow history.
func (a *Activities) startRequest(in IssueRunInput, run StartRunResult, set sessionSettings, harness runnerv1.Harness, start StartPoint, token, vkey string) *runnerv1.StartRequest {
	var env map[string]string
	if harness == runnerv1.Harness_HARNESS_CLAUDE_CODE {
		// Runners older than StartRequest.gateway read these; drop them once
		// every toolchain image has a newer runner.
		env = map[string]string{
			"ANTHROPIC_BASE_URL":   a.Agent.GatewayURL,
			"ANTHROPIC_AUTH_TOKEN": vkey,
			// Background tasks must stay on a model the key allows.
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": set.model,
			"ANTHROPIC_SMALL_FAST_MODEL":    set.model,
		}
	}
	return &runnerv1.StartRequest{
		StepRunId: run.StepRunID, Harness: harness, Model: set.model,
		SystemPrompt: set.systemPrompt,
		Env:          env,
		Gateway:      &runnerv1.Gateway{BaseUrl: a.Agent.GatewayURL, ApiKey: vkey, Api: modelAPI(a.Agent.ModelAPIs[set.model])},
		Timeout:      durationpb.New(set.timeout),
		IdleTimeout:  durationpb.New(a.Agent.IdleTimeout),
		Workspace: &runnerv1.Workspace{
			CloneUrl: strings.TrimSuffix(a.Agent.GitProxyURL, "/") + "/git/" + in.Repo + ".git", Token: token,
			BaseRef: start.CloneRef, Branch: run.Branch, CommitMessage: in.IssueKey + ": " + in.Title,
			AuthorName: a.Agent.AuthorName, AuthorEmail: a.Agent.AuthorEmail,
			// Reviewers cannot push: their workspace is read-only as well as their token.
			ReadOnly: set.readOnly, CompareRef: in.BaseBranch,
		},
		AllowedTools: set.allowedTools,
		AskHuman:     set.askHuman,
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
	runnerv1.EventType_EVENT_TYPE_QUESTION:          domain.EventQuestion,
}

func domainEventType(t runnerv1.EventType) domain.EventType {
	if d, ok := domainEventTypes[t]; ok {
		return d
	}
	return domain.EventError
}

// runnerHarness is the runner's harness for a definition's harness name.
func runnerHarness(name string) (runnerv1.Harness, error) {
	switch name {
	case "", "claude-code":
		return runnerv1.Harness_HARNESS_CLAUDE_CODE, nil
	case "opencode":
		return runnerv1.Harness_HARNESS_OPENCODE, nil
	case "pi":
		return runnerv1.Harness_HARNESS_PI, nil
	}
	return runnerv1.Harness_HARNESS_UNSPECIFIED, fmt.Errorf("harness %q is not supported by the runner", name)
}

// modelAPI is the runner's model API; unknown models leave it to the harness's default.
func modelAPI(api config.API) runnerv1.ModelAPI {
	switch api {
	case config.APIAnthropic:
		return runnerv1.ModelAPI_MODEL_API_ANTHROPIC
	case config.APIOpenAI:
		return runnerv1.ModelAPI_MODEL_API_OPENAI
	}
	return runnerv1.ModelAPI_MODEL_API_UNSPECIFIED
}

// runnerConn connects to an agent pod's runner.
func (a *Activities) runnerConn(pod AgentPod) (*grpc.ClientConn, runnerv1.RunnerServiceClient, error) {
	addr := net.JoinHostPort(pod.IP, fmt.Sprint(sandbox.RunnerPort))
	if a.RunnerAddr != nil {
		addr = a.RunnerAddr(pod)
	}
	// Keepalive pings notice a runner whose sandbox vanished while its pod
	// still looks alive, e.g. on a lost node (D102).
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), keepalive.Client())
	if err != nil {
		return nil, nil, fmt.Errorf("runner %s: %w", addr, err)
	}
	return conn, runnerv1.NewRunnerServiceClient(conn), nil
}

// openQuestion records a question the agent asked, for the inbox to show
// until someone answers it (D55). A malformed one is only logged: nobody
// could answer it, and the runner's wall-clock timeout bounds its wait.
func (a *Activities) openQuestion(ctx context.Context, stepRunID uuid.UUID, payload []byte) error {
	var q struct {
		QuestionID string `json:"question_id"`
		Question   string `json:"question"`
	}
	if err := json.Unmarshal(payload, &q); err != nil || q.QuestionID == "" || strings.TrimSpace(q.Question) == "" {
		a.Log.Warn("unusable question event", "step", stepRunID, "payload", string(payload))
		return nil
	}
	if _, err := a.Store.OpenQuestion(ctx, store.NewQuestion{StepRunID: stepRunID, QuestionID: q.QuestionID, Question: q.Question}); err != nil {
		return err
	}
	return a.syncWaiting(ctx, stepRunID)
}

// issueAnswers are the ask-human questions people answered for the issue,
// in any of its runs, for the step's prompt (D56).
func (a *Activities) issueAnswers(ctx context.Context, issueID string) ([]domain.Question, error) {
	id, err := uuid.Parse(issueID)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError("bad issue id", "input", err)
	}
	return a.Store.ListAnsweredQuestions(ctx, id)
}

// podEndedCause is why the runner calls were stopped, when the agent pod ended.
func podEndedCause(rctx context.Context) *agentPodEndedError {
	var ended *agentPodEndedError
	if errors.As(context.Cause(rctx), &ended) {
		return ended
	}
	return nil
}

// lostRunner is the result of a session whose agent pod ended before the agent
// was prompted: the step fails with the pod's reason (D102). The unused
// virtual key is deleted by RunAgentSession's deferred cleanup.
func lostRunner(ended *agentPodEndedError) SessionResult {
	return SessionResult{Status: "failed", Reason: "the sandbox was lost: " + ended.Error()}
}

// lostStartedRunner is lostRunner for a session the agent already worked in:
// its spend so far is recorded and its virtual key revoked, as when a session
// finishes (D102).
func (a *Activities) lostStartedRunner(ctx context.Context, stepID uuid.UUID, progress sessionProgress, vkey string, ended *agentPodEndedError) SessionResult {
	if progress.KeyAlias != "" {
		a.recordUsage(ctx, stepID, progress.KeyAlias, progress.Tokens)
	}
	if vkey != "" {
		if err := a.Gateway.DeleteKey(ctx, vkey); err != nil {
			a.Log.Warn("delete virtual key", "step_run", stepID, "error", err)
		}
	}
	a.Log.Warn("agent session lost its sandbox", "step_run", stepID, "reason", ended.Error())
	return lostRunner(ended)
}

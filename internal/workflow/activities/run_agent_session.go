package activities

import (
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
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gateway"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/sandbox"
	"github.com/misarescu/frankie/internal/store"
)

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

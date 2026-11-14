package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner/workspace"
)

// publisher is the part of a prepared workspace the server uses after a session.
type publisher interface {
	Branch() string
	Publish(ctx context.Context) (workspace.Result, error)
}

func prepareWorkspace(ctx context.Context, cfg workspace.Config) (publisher, error) {
	return workspace.Prepare(ctx, cfg)
}

var protoEventTypes = map[domain.EventType]runnerv1.EventType{
	domain.EventAssistantMessage: runnerv1.EventType_EVENT_TYPE_ASSISTANT_MESSAGE,
	domain.EventToolCall:         runnerv1.EventType_EVENT_TYPE_TOOL_CALL,
	domain.EventToolResult:       runnerv1.EventType_EVENT_TYPE_TOOL_RESULT,
	domain.EventFileDiff:         runnerv1.EventType_EVENT_TYPE_FILE_DIFF,
	domain.EventCommandOutput:    runnerv1.EventType_EVENT_TYPE_COMMAND_OUTPUT,
	domain.EventTokenUsage:       runnerv1.EventType_EVENT_TYPE_TOKEN_USAGE,
	domain.EventError:            runnerv1.EventType_EVENT_TYPE_ERROR,
	domain.EventDone:             runnerv1.EventType_EVENT_TYPE_DONE,
}

// Server implements RunnerService for a single session.
type Server struct {
	runnerv1.UnimplementedRunnerServiceServer

	harnesses map[runnerv1.Harness]Harness
	workdir   string
	now       func() time.Time
	tick      time.Duration // watchdog resolution

	prepare        func(context.Context, workspace.Config) (publisher, error)
	publishTimeout time.Duration

	mu        sync.Mutex
	starting  bool
	sessionID string
	session   Session
	log       *EventLog
	prompted  bool
}

// NewServer returns a runner whose harness works in workdir.
func NewServer(workdir string, harnesses map[runnerv1.Harness]Harness) *Server {
	return &Server{harnesses: harnesses, workdir: workdir, now: time.Now, tick: time.Second,
		prepare: prepareWorkspace, publishTimeout: 10 * time.Minute}
}

// Start launches the harness session.
func (s *Server) Start(ctx context.Context, req *runnerv1.StartRequest) (*runnerv1.StartResponse, error) {
	h, ok := s.harnesses[req.GetHarness()]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported harness %s", req.GetHarness())
	}
	timeout, err := durationArg("timeout", req.GetTimeout())
	if err != nil {
		return nil, err
	}
	idle, err := durationArg("idle_timeout", req.GetIdleTimeout())
	if err != nil {
		return nil, err
	}
	if req.GetMaxTurns() < 0 {
		return nil, status.Error(codes.InvalidArgument, "max_turns must be >= 0")
	}

	// Hold the lock only to claim the runner, not during the clone.
	s.mu.Lock()
	if s.session != nil || s.starting {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "a session was already started on this runner")
	}
	s.starting = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.starting = false
		s.mu.Unlock()
	}()

	var ws publisher
	if w := req.GetWorkspace(); w != nil {
		ws, err = s.prepare(ctx, workspace.Config{
			Dir: s.workdir, CloneURL: w.GetCloneUrl(), Token: w.GetToken(),
			BaseRef: w.GetBaseRef(), Branch: w.GetBranch(), CommitMessage: w.GetCommitMessage(),
			AuthorName: w.GetAuthorName(), AuthorEmail: w.GetAuthorEmail(),
			ReadOnly: w.GetReadOnly(), CompareRef: w.GetCompareRef(),
		})
		if err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "prepare workspace: %v", err)
		}
	}

	log := NewEventLog(s.now)
	emit := func(t domain.EventType, p any) {
		if d, ok := p.(Done); ok && t == domain.EventDone && ws != nil {
			p = s.publish(ws, log, d)
		}
		log.Append(t, p)
	}
	cfg := SessionConfig{
		Workdir:      s.workdir,
		Model:        req.GetModel(),
		SystemPrompt: req.GetSystemPrompt(),
		Env:          req.GetEnv(),
		MaxTurns:     int(req.GetMaxTurns()),
		AllowedTools: req.GetAllowedTools(),
		Gateway:      gatewayConfig(req.GetGateway()),
	}
	// The session outlives this RPC, so it must not inherit its cancellation.
	sess, err := h.Start(context.WithoutCancel(ctx), cfg, emit)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "start harness: %v", err)
	}
	s.mu.Lock()
	s.sessionID = domain.NewID().String()
	s.session, s.log = sess, log
	id := s.sessionID
	s.mu.Unlock()
	go s.watch(sess, log, timeout, idle)
	return &runnerv1.StartResponse{SessionId: id}, nil
}

// publish pushes the workspace of a succeeded session and records the outcome on
// its done event. File diffs are appended first, so done stays the last event.
// Failed, aborted and timed-out sessions push nothing.
func (s *Server) publish(ws publisher, log *EventLog, d Done) Done {
	if d.Status != DoneSucceeded {
		return d
	}
	d.Branch = ws.Branch()
	ctx, cancel := context.WithTimeout(context.Background(), s.publishTimeout)
	defer cancel()
	res, err := ws.Publish(ctx)
	if err != nil {
		log.Append(domain.EventError, Error{Message: "publishing the workspace failed", Detail: err.Error()})
		d.Status, d.Reason = DoneFailed, "push failed: "+err.Error()
		return d
	}
	for _, fd := range res.Diffs {
		log.Append(domain.EventFileDiff, fd)
	}
	if res.Truncated {
		log.Append(domain.EventError, Error{Message: "file diffs truncated", Detail: fmt.Sprintf("only the first %d changed files are reported", len(res.Diffs))})
	}
	d.CommitSHA, d.Pushed = res.CommitSHA, res.Pushed
	return d
}

// durationArg converts an optional, non-negative Duration field; unset means zero.
func durationArg(name string, d *durationpb.Duration) (time.Duration, error) {
	if d == nil {
		return 0, nil
	}
	if err := d.CheckValid(); err != nil || d.AsDuration() < 0 {
		return 0, status.Errorf(codes.InvalidArgument, "invalid %s", name)
	}
	return d.AsDuration(), nil
}

// Prompt sends the task. It is accepted once.
func (s *Server) Prompt(_ context.Context, req *runnerv1.PromptRequest) (*runnerv1.PromptResponse, error) {
	if req.GetText() == "" {
		return nil, status.Error(codes.InvalidArgument, "empty prompt")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.session == nil:
		return nil, status.Error(codes.FailedPrecondition, "no session; call Start first")
	case s.prompted:
		return nil, status.Error(codes.FailedPrecondition, "already prompted; use Steer for further messages")
	}
	if err := sendErr(s.session.Send(req.GetText())); err != nil {
		return nil, err
	}
	s.prompted = true
	return &runnerv1.PromptResponse{}, nil
}

// Steer injects a message into the running session.
func (s *Server) Steer(_ context.Context, req *runnerv1.SteerRequest) (*runnerv1.SteerResponse, error) {
	if req.GetText() == "" {
		return nil, status.Error(codes.InvalidArgument, "empty steer message")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.prompted {
		return nil, status.Error(codes.FailedPrecondition, "session has not been prompted")
	}
	if err := sendErr(s.session.Send(req.GetText())); err != nil {
		return nil, err
	}
	return &runnerv1.SteerResponse{}, nil
}

func sendErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrSessionFinished):
		return status.Error(codes.FailedPrecondition, "session finished")
	default:
		return status.Errorf(codes.Internal, "send to harness: %v", err)
	}
}

// Abort stops the session.
func (s *Server) Abort(_ context.Context, req *runnerv1.AbortRequest) (*runnerv1.AbortResponse, error) {
	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		return nil, status.Error(codes.FailedPrecondition, "no session")
	}
	reason := req.GetReason()
	if reason == "" {
		reason = "aborted by control plane"
	}
	sess.Abort(DoneAborted, reason)
	return &runnerv1.AbortResponse{}, nil
}

// Events streams the session's events from after_seq until done.
func (s *Server) Events(req *runnerv1.EventsRequest, stream runnerv1.RunnerService_EventsServer) error {
	s.mu.Lock()
	log := s.log
	s.mu.Unlock()
	if log == nil {
		return status.Error(codes.FailedPrecondition, "no session")
	}
	return log.Follow(stream.Context(), req.GetAfterSeq(), func(e Event) error {
		return stream.Send(&runnerv1.EventsResponse{Event: &runnerv1.Event{
			Seq:     e.Seq,
			Time:    timestamppb.New(e.Time),
			Type:    protoEventTypes[e.Type],
			Payload: e.Payload,
		}})
	})
}

// watch enforces the wall-clock and idle timeouts (FR-AG-3).
func (s *Server) watch(sess Session, log *EventLog, timeout, idle time.Duration) {
	if timeout <= 0 && idle <= 0 {
		return
	}
	start := s.now()
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		select {
		case <-sess.Done():
			return
		case <-ticker.C:
			now := s.now()
			if timeout > 0 && now.Sub(start) >= timeout {
				sess.Abort(DoneTimeout, fmt.Sprintf("wall-clock timeout after %s", timeout))
				return
			}
			if idle > 0 && now.Sub(log.LastActivity()) >= idle {
				sess.Abort(DoneTimeout, fmt.Sprintf("no harness output for %s", idle))
				return
			}
		}
	}
}

// gatewayConfig converts StartRequest.gateway; nil gives the zero Gateway.
func gatewayConfig(g *runnerv1.Gateway) Gateway {
	out := Gateway{BaseURL: g.GetBaseUrl(), APIKey: g.GetApiKey()}
	switch g.GetApi() {
	case runnerv1.ModelAPI_MODEL_API_ANTHROPIC:
		out.API = ModelAPIAnthropic
	case runnerv1.ModelAPI_MODEL_API_OPENAI:
		out.API = ModelAPIOpenAI
	}
	return out
}

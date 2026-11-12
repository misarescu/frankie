package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/durationpb"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/domain"
)

// fakeHarness answers every message with an assistant_message and only ends
// the session on Abort or finish.
type fakeHarness struct {
	mu       sync.Mutex
	sessions []*fakeSession
	cfg      SessionConfig
}

func (h *fakeHarness) Start(_ context.Context, cfg SessionConfig, emit Emit) (Session, error) {
	s := &fakeSession{emit: emit, done: make(chan struct{})}
	h.mu.Lock()
	h.sessions = append(h.sessions, s)
	h.cfg = cfg
	h.mu.Unlock()
	return s, nil
}

type fakeSession struct {
	emit Emit
	once sync.Once
	done chan struct{}
	mu   sync.Mutex
	sent []string
}

func (s *fakeSession) Send(text string) error {
	select {
	case <-s.done:
		return ErrSessionFinished
	default:
	}
	s.mu.Lock()
	s.sent = append(s.sent, text)
	s.mu.Unlock()
	s.emit(domain.EventAssistantMessage, AssistantMessage{Text: "got: " + text})
	return nil
}

func (s *fakeSession) finish(status DoneStatus, reason string) {
	s.once.Do(func() {
		s.emit(domain.EventDone, Done{Status: status, Reason: reason})
		close(s.done)
	})
}

func (s *fakeSession) Abort(status DoneStatus, reason string) { s.finish(status, reason) }
func (s *fakeSession) Done() <-chan struct{}                  { return s.done }

func newTestClient(t *testing.T, srv *Server) runnerv1.RunnerServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	runnerv1.RegisterRunnerServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return runnerv1.NewRunnerServiceClient(conn)
}

func newTestRunner(t *testing.T) (runnerv1.RunnerServiceClient, *fakeHarness) {
	t.Helper()
	h := &fakeHarness{}
	srv := NewServer("/workspace", map[runnerv1.Harness]Harness{runnerv1.Harness_HARNESS_CLAUDE_CODE: h})
	srv.tick = 10 * time.Millisecond
	return newTestClient(t, srv), h
}

func wantCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func readAll(t *testing.T, c runnerv1.RunnerServiceClient, afterSeq int64) []*runnerv1.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := c.Events(ctx, &runnerv1.EventsRequest{AfterSeq: afterSeq})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var out []*runnerv1.Event
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		out = append(out, resp.GetEvent())
	}
}

func donePayload(t *testing.T, events []*runnerv1.Event) Done {
	t.Helper()
	last := events[len(events)-1]
	if last.GetType() != runnerv1.EventType_EVENT_TYPE_DONE {
		t.Fatalf("last event is %s, want DONE", last.GetType())
	}
	var d Done
	if err := json.Unmarshal(last.GetPayload(), &d); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	return d
}

func TestRunnerLifecycle(t *testing.T) {
	c, h := newTestRunner(t)
	ctx := t.Context()

	_, err := c.Prompt(ctx, &runnerv1.PromptRequest{Text: "x"})
	wantCode(t, err, codes.FailedPrecondition)
	stream, err := c.Events(ctx, &runnerv1.EventsRequest{})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	_, err = stream.Recv() // server-streaming errors surface on the first Recv
	wantCode(t, err, codes.FailedPrecondition)

	_, err = c.Start(ctx, &runnerv1.StartRequest{})
	wantCode(t, err, codes.InvalidArgument) // unspecified harness
	_, err = c.Start(ctx, &runnerv1.StartRequest{Harness: runnerv1.Harness_HARNESS_CLAUDE_CODE,
		Timeout: durationpb.New(-time.Second)})
	wantCode(t, err, codes.InvalidArgument)

	resp, err := c.Start(ctx, &runnerv1.StartRequest{
		StepRunId: "step-1", Harness: runnerv1.Harness_HARNESS_CLAUDE_CODE,
		Model: "opus", SystemPrompt: "you are a coder", MaxTurns: 9,
		Env: map[string]string{"ANTHROPIC_BASE_URL": "http://gateway"},
	})
	if err != nil || resp.GetSessionId() == "" {
		t.Fatalf("Start = %v, %v", resp, err)
	}
	if h.cfg.Workdir != "/workspace" || h.cfg.Model != "opus" || h.cfg.MaxTurns != 9 || h.cfg.Env["ANTHROPIC_BASE_URL"] != "http://gateway" {
		t.Errorf("harness config = %+v", h.cfg)
	}
	_, err = c.Start(ctx, &runnerv1.StartRequest{Harness: runnerv1.Harness_HARNESS_CLAUDE_CODE})
	wantCode(t, err, codes.FailedPrecondition) // one session per runner

	_, err = c.Steer(ctx, &runnerv1.SteerRequest{Text: "early"})
	wantCode(t, err, codes.FailedPrecondition) // steer needs a prompt first
	_, err = c.Prompt(ctx, &runnerv1.PromptRequest{})
	wantCode(t, err, codes.InvalidArgument)

	if _, err := c.Prompt(ctx, &runnerv1.PromptRequest{Text: "build it"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	_, err = c.Prompt(ctx, &runnerv1.PromptRequest{Text: "again"})
	wantCode(t, err, codes.FailedPrecondition)
	if _, err := c.Steer(ctx, &runnerv1.SteerRequest{Text: "and test it"}); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	h.sessions[0].finish(DoneSucceeded, "")
	events := readAll(t, c, 0)
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3: %v", len(events), events)
	}
	for i, e := range events {
		if e.GetSeq() != int64(i+1) || e.GetTime() == nil {
			t.Errorf("event %d: seq %d time %v", i, e.GetSeq(), e.GetTime())
		}
	}
	if d := donePayload(t, events); d.Status != DoneSucceeded {
		t.Errorf("done = %+v", d)
	}

	// Resume after a seen seq replays only what follows.
	resumed := readAll(t, c, 2)
	if len(resumed) != 1 || resumed[0].GetSeq() != 3 {
		t.Errorf("resume after 2 = %v", resumed)
	}
	_, err = c.Steer(ctx, &runnerv1.SteerRequest{Text: "too late"})
	wantCode(t, err, codes.FailedPrecondition)
}

// A follower that connects before events exist receives them live, then the stream ends.
func TestRunnerEventsFollowLive(t *testing.T) {
	c, h := newTestRunner(t)
	ctx := t.Context()
	if _, err := c.Start(ctx, &runnerv1.StartRequest{Harness: runnerv1.Harness_HARNESS_CLAUDE_CODE}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := make(chan []*runnerv1.Event)
	go func() { got <- readAll(t, c, 0) }()
	time.Sleep(50 * time.Millisecond)
	if _, err := c.Prompt(ctx, &runnerv1.PromptRequest{Text: "live"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	h.sessions[0].finish(DoneSucceeded, "")
	select {
	case events := <-got:
		if len(events) != 2 || events[0].GetType() != runnerv1.EventType_EVENT_TYPE_ASSISTANT_MESSAGE {
			t.Errorf("events = %v", events)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live stream did not end after done")
	}
}

func TestRunnerAbort(t *testing.T) {
	c, _ := newTestRunner(t)
	ctx := t.Context()
	_, err := c.Abort(ctx, &runnerv1.AbortRequest{})
	wantCode(t, err, codes.FailedPrecondition)
	if _, err := c.Start(ctx, &runnerv1.StartRequest{Harness: runnerv1.Harness_HARNESS_CLAUDE_CODE}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.Abort(ctx, &runnerv1.AbortRequest{}); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	d := donePayload(t, readAll(t, c, 0))
	if d.Status != DoneAborted || d.Reason != "aborted by control plane" {
		t.Errorf("done = %+v", d)
	}
}

// FR-AG-3: wall-clock and idle timeouts end the session with status timeout.
func TestRunnerTimeouts(t *testing.T) {
	tests := []struct {
		name       string
		req        *runnerv1.StartRequest
		keepBusy   bool
		wantReason string
	}{
		{name: "idle", req: &runnerv1.StartRequest{IdleTimeout: durationpb.New(100 * time.Millisecond)},
			wantReason: "no harness output"},
		{name: "wall-clock despite activity", req: &runnerv1.StartRequest{
			Timeout: durationpb.New(300 * time.Millisecond), IdleTimeout: durationpb.New(time.Hour)},
			keepBusy: true, wantReason: "wall-clock timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestRunner(t)
			ctx := t.Context()
			tt.req.Harness = runnerv1.Harness_HARNESS_CLAUDE_CODE
			if _, err := c.Start(ctx, tt.req); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if _, err := c.Prompt(ctx, &runnerv1.PromptRequest{Text: "go"}); err != nil {
				t.Fatalf("Prompt: %v", err)
			}
			if tt.keepBusy {
				go func() {
					for range 100 {
						if _, err := c.Steer(context.Background(), &runnerv1.SteerRequest{Text: "busy"}); err != nil {
							return
						}
						time.Sleep(20 * time.Millisecond)
					}
				}()
			}
			start := time.Now()
			d := donePayload(t, readAll(t, c, 0))
			if d.Status != DoneTimeout || !strings.Contains(d.Reason, tt.wantReason) {
				t.Errorf("done = %+v; want timeout %q", d, tt.wantReason)
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Errorf("timeout took %s", elapsed)
			}
		})
	}
}

func TestEveryRunnerEventTypeHasAProtoType(t *testing.T) {
	for _, typ := range []domain.EventType{
		domain.EventAssistantMessage, domain.EventToolCall, domain.EventToolResult, domain.EventFileDiff,
		domain.EventCommandOutput, domain.EventTokenUsage, domain.EventError, domain.EventDone,
	} {
		if pt, ok := protoEventTypes[typ]; !ok || pt == runnerv1.EventType_EVENT_TYPE_UNSPECIFIED {
			t.Errorf("%s has no proto event type", typ)
		}
	}
}

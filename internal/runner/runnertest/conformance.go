package runnertest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// Adapter describes a harness adapter for the conformance suite.
type Adapter struct {
	// Binary is the harness executable looked up on PATH.
	Binary string
	// New returns the adapter's harness running bin.
	New func(t *testing.T, bin string) runner.Harness
	// ShellTool is the harness's shell tool, as the model calls it.
	ShellTool string
	// APIs are the gateway APIs the harness speaks.
	APIs []runner.ModelAPI
	// ReadOnlyRole is a reviewer's tools, including bash:readonly.
	ReadOnlyRole []string
	// WriteTools are tool names the model must not be offered with ReadOnlyRole.
	WriteTools []string
	// Env adds to the pod environment, e.g. variables a harness needs to run offline.
	Env map[string]string
}

// Conformance timeouts.
const (
	sessionTimeout = 120 * time.Second
	testModel      = "fake-model"
	testKey        = "sk-conformance"
)

// payloadTypes is the payload each normalized event type carries
// (runner.proto documents their JSON).
var payloadTypes = map[domain.EventType]reflect.Type{
	domain.EventAssistantMessage: reflect.TypeFor[runner.AssistantMessage](),
	domain.EventToolCall:         reflect.TypeFor[runner.ToolCall](),
	domain.EventToolResult:       reflect.TypeFor[runner.ToolResult](),
	domain.EventCommandOutput:    reflect.TypeFor[runner.CommandOutput](),
	domain.EventTokenUsage:       reflect.TypeFor[runner.TokenUsage](),
	domain.EventError:            reflect.TypeFor[runner.Error](),
	domain.EventDone:             reflect.TypeFor[runner.Done](),
}

// RunConformance runs the behaviors every harness adapter must share
// (docs/spec/04: the adapter contract) against the real binary and the fake
// model, once per API the harness speaks: a task through the gateway with a
// shell tool call, a steering message sent mid-turn, an abort, and a
// read-only shell refusing a write.
func RunConformance(t *testing.T, a Adapter) {
	bin := Binary(t, a.Binary)
	for _, api := range a.APIs {
		t.Run(string(api), func(t *testing.T) {
			t.Run("task", func(t *testing.T) { conformTask(t, a, bin, api) })
			t.Run("steer", func(t *testing.T) { conformSteer(t, a, bin, api) })
			t.Run("abort", func(t *testing.T) { conformAbort(t, a, bin, api) })
			t.Run("read-only shell", func(t *testing.T) { conformReadOnly(t, a, bin, api) })
		})
	}
}

// session is one harness session under test.
type session struct {
	runner.Session
	rec   *Recorder
	model *FakeModel
	work  string
}

func start(t *testing.T, a Adapter, bin string, api runner.ModelAPI, f *FakeModel, tools []string) *session {
	t.Helper()
	env := PodEnv(t)
	for k, v := range a.Env {
		env[k] = v
	}
	work := t.TempDir()
	cfg := runner.SessionConfig{
		Workdir: work, Model: testModel, SystemPrompt: "You are a Frankie conformance test agent.", Env: env, AllowedTools: tools,
		Gateway: runner.Gateway{BaseURL: f.URL(), APIKey: testKey, API: api},
	}
	rec := &Recorder{}
	s, err := a.New(t, bin).Start(t.Context(), cfg, rec.Emit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Abort(runner.DoneAborted, "test cleanup")
		<-s.Done()
	})
	return &session{Session: s, rec: rec, model: f, work: work}
}

func (s *session) send(t *testing.T, text string) {
	t.Helper()
	if err := s.Send(text); err != nil {
		t.Fatalf("send %q: %v", text, err)
	}
}

func (s *session) wait(t *testing.T) runner.Done {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(sessionTimeout):
		t.Fatalf("the session did not end; events: %+v", s.rec.All())
	}
	checkStream(t, s.rec.All())
	return s.rec.Done(t)
}

// waitForFirstTaskRequest waits until the model holds the first task request.
func (s *session) waitForFirstTaskRequest(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(sessionTimeout)
	for len(s.model.TaskRequests()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no task request reached the model; events: %+v", s.rec.All())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// reporter is the part of testing.T checkStream reports through.
type reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// checkStream checks invariants of every stream: payloads of the right type
// for their event, exactly one done event and it is last, tool results and
// command output answering an announced tool call, and token usage as
// non-decreasing session totals.
func checkStream(t reporter, events []Event) {
	t.Helper()
	calls := map[string]bool{}
	var last runner.TokenUsage
	for i, e := range events {
		want, ok := payloadTypes[e.Type]
		if !ok {
			t.Errorf("event %d: unknown type %q", i, e.Type)
			continue
		}
		if got := reflect.TypeOf(e.Payload); got != want {
			t.Errorf("event %d (%s): payload %v, want %v", i, e.Type, got, want)
			continue
		}
		switch p := e.Payload.(type) {
		case runner.Done:
			if i != len(events)-1 {
				t.Errorf("done event %d is not the last of %d", i, len(events))
			}
		case runner.ToolCall:
			if p.ID == "" || p.Name == "" {
				t.Errorf("event %d: tool call without id or name: %+v", i, p)
			}
			calls[p.ID] = true
		case runner.ToolResult:
			if !calls[p.ToolCallID] {
				t.Errorf("event %d: tool result for unannounced call %q", i, p.ToolCallID)
			}
		case runner.CommandOutput:
			if !calls[p.ToolCallID] {
				t.Errorf("event %d: command output for unannounced call %q", i, p.ToolCallID)
			}
		case runner.TokenUsage:
			if p.InputTokens < last.InputTokens || p.OutputTokens < last.OutputTokens || p.Turns < last.Turns || p.CostUSD < last.CostUSD {
				t.Errorf("event %d: token usage went down: %+v after %+v", i, p, last)
			}
			last = p
		}
	}
	if n := countType(events, domain.EventDone); n != 1 {
		t.Errorf("%d done events, want 1", n)
	}
}

func countType(events []Event, typ domain.EventType) int {
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func payloads[T any](events []Event) []T {
	var out []T
	for _, e := range events {
		if p, ok := e.Payload.(T); ok {
			out = append(out, p)
		}
	}
	return out
}

func conformTask(t *testing.T, a Adapter, bin string, api runner.ModelAPI) {
	s := start(t, a, bin, api, NewFakeModel(t, a.ShellTool), nil)
	s.send(t, "Create hello.txt")
	d := s.wait(t)
	events := s.rec.All()

	if d.Status != runner.DoneSucceeded || d.ExitCode != 0 || d.HarnessSessionID == "" || !strings.HasPrefix(d.Result, FinalPrefix) {
		t.Errorf("done = %+v", d)
	}
	if errs := payloads[runner.Error](events); len(errs) > 0 {
		t.Errorf("a clean session reported errors: %+v", errs)
	}
	if b, err := os.ReadFile(filepath.Join(s.work, "hello.txt")); err != nil || string(b) != "hello\n" {
		t.Errorf("the shell call did not run: hello.txt = %q, %v", b, err)
	}
	texts := payloads[runner.AssistantMessage](events)
	if !slices.ContainsFunc(texts, func(m runner.AssistantMessage) bool { return m.Text == FirstAnswer }) {
		t.Errorf("assistant messages %+v lack %q", texts, FirstAnswer)
	}
	calls := payloads[runner.ToolCall](events)
	if !slices.ContainsFunc(calls, func(c runner.ToolCall) bool {
		return c.ID == ToolCallID && c.Name == a.ShellTool && shellCommand(c.Input) == ShellCommand
	}) {
		t.Errorf("tool calls %+v lack the shell call", calls)
	}
	outs := payloads[runner.CommandOutput](events)
	if !slices.ContainsFunc(outs, func(o runner.CommandOutput) bool { return o.ToolCallID == ToolCallID && !o.IsError }) {
		t.Errorf("command outputs %+v lack the shell call's", outs)
	}
	usage := payloads[runner.TokenUsage](events)
	if len(usage) == 0 {
		t.Fatal("no token usage")
	}
	// Two task answers of 100 input and 20 output tokens each; a harness may
	// add side requests (titles), never fewer.
	if u := usage[len(usage)-1]; u.InputTokens < 200 || u.OutputTokens < 40 || u.Turns < 1 {
		t.Errorf("final usage = %+v", u)
	}

	reqs := s.model.Requests()
	for _, r := range reqs {
		if r.API != string(api) || r.Model != testModel || !strings.Contains(r.Auth, testKey) {
			t.Errorf("request reached the gateway as api %q model %q auth %q", r.API, r.Model, r.Auth)
		}
	}
	if task := s.model.TaskRequests(); len(task) < 2 || !slices.Contains(task[0].Tools, a.ShellTool) {
		t.Errorf("task requests = %+v", task)
	}
	if err := s.Send("more"); !errors.Is(err, runner.ErrSessionFinished) {
		t.Errorf("Send after the end = %v, want ErrSessionFinished", err)
	}
}

func conformSteer(t *testing.T, a Adapter, bin string, api runner.ModelAPI) {
	f := NewFakeModel(t, a.ShellTool)
	f.Hold = make(chan struct{})
	s := start(t, a, bin, api, f, nil)
	s.send(t, "Create hello.txt")
	s.waitForFirstTaskRequest(t)
	const steer = "Also mention bananas"
	s.send(t, steer)
	close(f.Hold)
	d := s.wait(t)

	if d.Status != runner.DoneSucceeded {
		t.Fatalf("done = %+v", d)
	}
	task := f.TaskRequests()
	if task[0].SawUser(steer) {
		t.Error("the steer reached the request it was sent during")
	}
	if !task[len(task)-1].SawUser(steer) || !strings.Contains(d.Result, steer) {
		t.Errorf("the steer did not reach the session: last request %+v, result %q", task[len(task)-1], d.Result)
	}
}

func conformAbort(t *testing.T, a Adapter, bin string, api runner.ModelAPI) {
	f := NewFakeModel(t, a.ShellTool)
	f.Hold = make(chan struct{})
	defer close(f.Hold)
	s := start(t, a, bin, api, f, nil)
	s.send(t, "Create hello.txt")
	s.waitForFirstTaskRequest(t)
	s.Abort(runner.DoneAborted, "operator abort")
	if d := s.wait(t); d.Status != runner.DoneAborted || d.Reason != "operator abort" {
		t.Fatalf("done = %+v", d)
	}
	if err := s.Send("late"); !errors.Is(err, runner.ErrSessionFinished) {
		t.Errorf("Send after abort = %v, want ErrSessionFinished", err)
	}
	if _, err := os.Stat(filepath.Join(s.work, "hello.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an aborted session ran its tool call: %v", err)
	}
}

func conformReadOnly(t *testing.T, a Adapter, bin string, api runner.ModelAPI) {
	s := start(t, a, bin, api, NewFakeModel(t, a.ShellTool), a.ReadOnlyRole)
	s.send(t, "Create hello.txt")
	d := s.wait(t)
	if d.Status != runner.DoneSucceeded {
		t.Fatalf("done = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(s.work, "hello.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a read-only shell wrote hello.txt: %v", err)
	}
	outs := payloads[runner.CommandOutput](s.rec.All())
	if !slices.ContainsFunc(outs, func(o runner.CommandOutput) bool { return o.ToolCallID == ToolCallID && o.IsError }) {
		t.Errorf("command outputs %+v lack a refused shell call", outs)
	}
	task := s.model.TaskRequests()
	if len(task) == 0 {
		t.Fatal("no task request")
	}
	for _, w := range a.WriteTools {
		if slices.Contains(task[0].Tools, w) {
			t.Errorf("a read-only role was offered %s: %v", w, task[0].Tools)
		}
	}
}

// shellCommand is the command of a shell tool call's input, whatever its Go type.
func shellCommand(input any) string {
	b, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(b, &in)
	return in.Command
}

package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// These tests run the real opencode binary (pinned in mise.toml) against a
// fake OpenAI-compatible model, so they spend no model usage. Without the
// binary they skip locally and fail in CI.

const (
	testModel  = "openai/gpt-test"
	deadProxy  = "http://127.0.0.1:9"
	podNoProxy = "localhost,127.0.0.1,.svc,.cluster.local"
)

// fakeModel is a scripted chat completions endpoint: a first answer calls the
// shell tool, any answer after a tool result reports the user messages it saw.
type fakeModel struct {
	srv *httptest.Server
	// hold, when set, blocks the first answer until it is closed, so a test
	// can act while the agent loop is mid-turn.
	hold chan struct{}

	mu       sync.Mutex
	requests []fakeRequest
}

type fakeRequest struct {
	Auth  string
	Model string
	Tools []string
	Users []string
}

func newFakeModel(t *testing.T) *fakeModel {
	t.Helper()
	f := &fakeModel{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeModel) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := fakeRequest{Auth: r.Header.Get("Authorization"), Model: body.Model}
	for _, t := range body.Tools {
		req.Tools = append(req.Tools, t.Function.Name)
	}
	toolResult := false
	for _, m := range body.Messages {
		switch m.Role {
		case "user":
			req.Users = append(req.Users, fmt.Sprint(m.Content))
		case "tool":
			toolResult = true
		}
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	first := len(f.requests) == 1
	f.mu.Unlock()
	if first && f.hold != nil {
		<-f.hold
	}

	w.Header().Set("Content-Type", "text/event-stream")
	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
	choice := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": body.Model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	if !toolResult {
		send(choice(map[string]any{"role": "assistant", "content": "Writing the file."}, nil))
		send(choice(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
			"function": map[string]any{"name": "shell", "arguments": `{"command":"echo hello > hello.txt","description":"Write hello.txt"}`}}}}, nil))
		send(choice(map[string]any{}, "tool_calls"))
	} else {
		send(choice(map[string]any{"role": "assistant", "content": "Done. Users: " + strings.Join(req.Users, " | ")}, nil))
		send(choice(map[string]any{}, "stop"))
	}
	send(map[string]any{"id": "c1", "object": "chat.completion.chunk", "choices": []any{},
		"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func (f *fakeModel) seen() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// recorder collects the normalized events of a session.
type recorder struct {
	mu     sync.Mutex
	events []recorded
}

type recorded struct {
	typ     domain.EventType
	payload any
}

func (r *recorder) emit(t domain.EventType, p any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recorded{t, p})
}

func (r *recorder) all() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func (r *recorder) done(t *testing.T) runner.Done {
	t.Helper()
	ev := r.all()
	if len(ev) == 0 || ev[len(ev)-1].typ != domain.EventDone {
		t.Fatalf("last event is not done: %+v", ev)
	}
	return ev[len(ev)-1].payload.(runner.Done)
}

func opencodeBinary(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("opencode")
	if err == nil {
		return bin
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("opencode not on PATH (required in CI): %v", err)
	}
	t.Skipf("opencode not on PATH; run through mise (mise run test): %v", err)
	return ""
}

// startSession starts a session in a fresh workdir, isolated from the
// developer's opencode data and configuration.
func startSession(t *testing.T, f *fakeModel, cfg runner.SessionConfig) (runner.Session, *recorder, string) {
	t.Helper()
	bin := opencodeBinary(t)
	home := t.TempDir()
	work := t.TempDir()
	cfg.Workdir = work
	cfg.Model = testModel
	cfg.Gateway = runner.Gateway{BaseURL: f.srv.URL, APIKey: "sk-test", API: runner.ModelAPIOpenAI}
	cfg.Env = map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME": filepath.Join(home, ".local/share"), "XDG_STATE_HOME": filepath.Join(home, ".local/state"),
		"XDG_CACHE_HOME": filepath.Join(home, ".cache"),
		// As in an agent pod (internal/sandbox): everything through the egress
		// proxy, here a dead one, except cluster-internal names and loopback.
		"HTTPS_PROXY": deadProxy, "https_proxy": deadProxy, "HTTP_PROXY": deadProxy, "http_proxy": deadProxy,
		"NO_PROXY": podNoProxy, "no_proxy": podNoProxy}
	rec := &recorder{}
	h := &Harness{Binary: bin, TempDir: t.TempDir(), KillGrace: 2 * time.Second}
	s, err := h.Start(t.Context(), cfg, rec.emit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Abort(runner.DoneAborted, "test cleanup")
		<-s.Done()
	})
	return s, rec, work
}

func waitDone(t *testing.T, s runner.Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(90 * time.Second):
		t.Fatal("session did not end")
	}
}

func TestSessionRunsTheTaskThroughTheGateway(t *testing.T) {
	f := newFakeModel(t)
	s, rec, work := startSession(t, f, runner.SessionConfig{SystemPrompt: "You are the Frankie coder."})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)

	d := rec.done(t)
	if d.Status != runner.DoneSucceeded || d.ExitCode != 0 || d.HarnessSessionID == "" || !strings.HasPrefix(d.Result, "Done.") {
		t.Fatalf("done = %+v", d)
	}
	if b, err := os.ReadFile(filepath.Join(work, "hello.txt")); err != nil || string(b) != "hello\n" {
		t.Errorf("hello.txt = %q, %v", b, err)
	}
	var types []domain.EventType
	var call runner.ToolCall
	var out runner.CommandOutput
	var usage runner.TokenUsage
	for _, e := range rec.all() {
		types = append(types, e.typ)
		switch p := e.payload.(type) {
		case runner.ToolCall:
			call = p
		case runner.CommandOutput:
			out = p
		case runner.TokenUsage:
			usage = p
		}
	}
	for _, want := range []domain.EventType{domain.EventAssistantMessage, domain.EventToolCall, domain.EventCommandOutput, domain.EventTokenUsage} {
		if !slices.Contains(types, want) {
			t.Errorf("no %s event in %v", want, types)
		}
	}
	if call.Name != "shell" || call.ID != "call_1" || out.ToolCallID != "call_1" || out.IsError {
		t.Errorf("tool call %+v, output %+v", call, out)
	}
	if usage.InputTokens != 200 || usage.OutputTokens != 40 || usage.Turns != 2 {
		t.Errorf("usage = %+v (two model calls, no title call)", usage)
	}
	reqs := f.seen()
	if len(reqs) != 2 {
		t.Fatalf("model calls = %d, want 2 (the session title costs none): %+v", len(reqs), reqs)
	}
	for _, r := range reqs {
		if r.Auth != "Bearer sk-test" || r.Model != testModel {
			t.Errorf("request auth %q model %q", r.Auth, r.Model)
		}
	}
	if err := s.Send("more"); !errors.Is(err, runner.ErrSessionFinished) {
		t.Errorf("Send after the end = %v, want ErrSessionFinished", err)
	}
}

func TestSteerReachesTheRunningTurn(t *testing.T) {
	f := newFakeModel(t)
	f.hold = make(chan struct{})
	s, rec, _ := startSession(t, f, runner.SessionConfig{})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.seen()) == 1 })
	if err := s.Send("Also mention bananas"); err != nil {
		t.Fatal(err)
	}
	close(f.hold)
	waitDone(t, s)

	d := rec.done(t)
	if d.Status != runner.DoneSucceeded {
		t.Fatalf("done = %+v", d)
	}
	reqs := f.seen()
	last := reqs[len(reqs)-1]
	if !slices.Contains(last.Users, "Also mention bananas") || !strings.Contains(d.Result, "bananas") {
		t.Errorf("the steer did not reach the turn: requests %+v, result %q", reqs, d.Result)
	}
}

func TestAbortStopsTheServer(t *testing.T) {
	f := newFakeModel(t)
	f.hold = make(chan struct{})
	defer close(f.hold)
	s, rec, _ := startSession(t, f, runner.SessionConfig{})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.seen()) == 1 })
	s.Abort(runner.DoneAborted, "operator abort")
	waitDone(t, s)
	if d := rec.done(t); d.Status != runner.DoneAborted || d.Reason != "operator abort" {
		t.Fatalf("done = %+v", d)
	}
	if err := s.Send("late"); !errors.Is(err, runner.ErrSessionFinished) {
		t.Errorf("Send after abort = %v", err)
	}
}

func TestReadOnlyShellRefusesWrites(t *testing.T) {
	f := newFakeModel(t)
	s, rec, work := startSession(t, f, runner.SessionConfig{AllowedTools: []string{"read", "grep", "bash:readonly"}})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if d := rec.done(t); d.Status != runner.DoneSucceeded {
		t.Fatalf("done = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(work, "hello.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a read-only shell wrote hello.txt: %v", err)
	}
	var out runner.CommandOutput
	for _, e := range rec.all() {
		if p, ok := e.payload.(runner.CommandOutput); ok {
			out = p
		}
	}
	if !out.IsError || !strings.Contains(out.Output, "Permission denied") {
		t.Errorf("command output = %+v, want a refusal", out)
	}
	tools := f.seen()[0].Tools
	for _, offered := range []string{"edit", "write", "webfetch", "question"} {
		if slices.Contains(tools, offered) {
			t.Errorf("the model was offered %s: %v", offered, tools)
		}
	}
}

func TestStartFailsWithoutGateway(t *testing.T) {
	h := &Harness{Binary: "/does/not/matter"}
	_, err := h.Start(t.Context(), runner.SessionConfig{Workdir: t.TempDir(), Model: "m"}, func(domain.EventType, any) {})
	if err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartFailsWhenTheServerCannotStart(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho broken >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &Harness{Binary: bin, TempDir: t.TempDir()}
	cfg := runner.SessionConfig{Workdir: t.TempDir(), Model: "m", Gateway: runner.Gateway{BaseURL: "http://127.0.0.1:1", APIKey: "k"}}
	_, err := h.Start(t.Context(), cfg, func(domain.EventType, any) {})
	if err == nil || !strings.Contains(err.Error(), "exited during startup") || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(h.TempDir)
	if len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

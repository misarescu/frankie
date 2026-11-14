package pi

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
	"github.com/misarescu/frankie/internal/runner/runnertest"
)

// These tests run the real pi binary (pinned in mise.toml) against a fake
// OpenAI-compatible model, so they spend no model usage. Without the binary
// they skip locally and fail in CI.

const testModel = "openai/gpt-test"

func startSession(t *testing.T, f *runnertest.FakeModel, cfg runner.SessionConfig) (runner.Session, *runnertest.Recorder, string) {
	t.Helper()
	bin := runnertest.Binary(t, "pi")
	work := t.TempDir()
	cfg.Workdir = work
	cfg.Model = testModel
	cfg.Gateway = runner.Gateway{BaseURL: f.URL(), APIKey: "sk-test", API: runner.ModelAPIOpenAI}
	cfg.Env = runnertest.PodEnv(t)
	rec := &runnertest.Recorder{}
	h := &Harness{Binary: bin, TempDir: t.TempDir(), KillGrace: 2 * time.Second}
	s, err := h.Start(t.Context(), cfg, rec.Emit)
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

func TestSessionRunsTheTaskThroughTheGateway(t *testing.T) {
	f := runnertest.NewFakeModel(t, "bash")
	s, rec, work := startSession(t, f, runner.SessionConfig{SystemPrompt: "You are the Frankie coder."})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)

	d := rec.Done(t)
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
	for _, e := range rec.All() {
		types = append(types, e.Type)
		switch p := e.Payload.(type) {
		case runner.ToolCall:
			call = p
		case runner.CommandOutput:
			out = p
		case runner.TokenUsage:
			usage = p
		}
	}
	if slices.Contains(types, domain.EventError) {
		t.Errorf("a clean session emitted errors: %+v", rec.All())
	}
	for _, want := range []domain.EventType{domain.EventAssistantMessage, domain.EventToolCall, domain.EventCommandOutput, domain.EventTokenUsage} {
		if !slices.Contains(types, want) {
			t.Errorf("no %s event in %v", want, types)
		}
	}
	if call.Name != "bash" || call.ID != "call_1" || out.ToolCallID != "call_1" || out.IsError {
		t.Errorf("tool call %+v, output %+v", call, out)
	}
	if usage.InputTokens != 200 || usage.OutputTokens != 40 || usage.Turns != 2 {
		t.Errorf("usage = %+v", usage)
	}
	reqs := f.Requests()
	if len(reqs) != 2 {
		t.Fatalf("model calls = %d, want 2: %+v", len(reqs), reqs)
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
	f := runnertest.NewFakeModel(t, "bash")
	f.Hold = make(chan struct{})
	s, rec, _ := startSession(t, f, runner.SessionConfig{})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.Requests()) == 1 })
	if err := s.Send("Also mention bananas"); err != nil {
		t.Fatal(err)
	}
	close(f.Hold)
	waitDone(t, s)

	d := rec.Done(t)
	if d.Status != runner.DoneSucceeded {
		t.Fatalf("done = %+v", d)
	}
	reqs := f.Requests()
	last := reqs[len(reqs)-1]
	if len(reqs) != 2 || !slices.Contains(last.Users, "Also mention bananas") || !strings.Contains(d.Result, "bananas") {
		t.Errorf("the steer did not reach the running turn: requests %+v, result %q", reqs, d.Result)
	}
}

func TestAbortStopsPi(t *testing.T) {
	f := runnertest.NewFakeModel(t, "bash")
	f.Hold = make(chan struct{})
	defer close(f.Hold)
	s, rec, _ := startSession(t, f, runner.SessionConfig{})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.Requests()) == 1 })
	s.Abort(runner.DoneAborted, "operator abort")
	waitDone(t, s)
	if d := rec.Done(t); d.Status != runner.DoneAborted || d.Reason != "operator abort" {
		t.Fatalf("done = %+v", d)
	}
	if err := s.Send("late"); !errors.Is(err, runner.ErrSessionFinished) {
		t.Errorf("Send after abort = %v", err)
	}
}

func TestReadOnlyShellRefusesWrites(t *testing.T) {
	f := runnertest.NewFakeModel(t, "bash")
	s, rec, work := startSession(t, f, runner.SessionConfig{AllowedTools: []string{"read", "grep", "glob", "bash:readonly"}})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if d := rec.Done(t); d.Status != runner.DoneSucceeded {
		t.Fatalf("done = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(work, "hello.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a read-only shell wrote hello.txt: %v", err)
	}
	var out runner.CommandOutput
	for _, e := range rec.All() {
		if p, ok := e.Payload.(runner.CommandOutput); ok {
			out = p
		}
	}
	if !out.IsError || !strings.Contains(out.Output, "Only read-only commands") {
		t.Errorf("command output = %+v, want a refusal", out)
	}
	tools := f.Requests()[0].Tools
	slices.Sort(tools)
	if want := []string{"bash", "find", "grep", "ls", "read"}; !slices.Equal(tools, want) {
		t.Errorf("tools offered = %v, want %v", tools, want)
	}
}

func TestStartFails(t *testing.T) {
	gw := runner.Gateway{BaseURL: "http://127.0.0.1:1", APIKey: "k"}
	t.Run("without a gateway", func(t *testing.T) {
		_, err := (&Harness{Binary: "/does/not/matter", TempDir: t.TempDir()}).Start(t.Context(), runner.SessionConfig{Workdir: t.TempDir(), Model: "m"}, func(domain.EventType, any) {})
		if err == nil || !strings.Contains(err.Error(), "gateway") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("with a tool pi lacks", func(t *testing.T) {
		cfg := runner.SessionConfig{Workdir: t.TempDir(), Model: "m", Gateway: gw, AllowedTools: []string{"web-fetch"}}
		_, err := (&Harness{Binary: "/does/not/matter", TempDir: t.TempDir()}).Start(t.Context(), cfg, func(domain.EventType, any) {})
		if err == nil || !strings.Contains(err.Error(), "web-fetch") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("when pi exits at once", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "pi")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho broken >&2\nexit 3\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		h := &Harness{Binary: bin, TempDir: t.TempDir()}
		_, err := h.Start(t.Context(), runner.SessionConfig{Workdir: t.TempDir(), Model: "m", Gateway: gw}, func(domain.EventType, any) {})
		if err == nil || !strings.Contains(err.Error(), "pi exited") || !strings.Contains(err.Error(), "broken") {
			t.Fatalf("err = %v", err)
		}
		if entries, _ := os.ReadDir(h.TempDir); len(entries) != 0 {
			t.Errorf("temp files left behind: %v", entries)
		}
	})
}

package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
	"github.com/misarescu/frankie/internal/runner/runnertest"
)

// Tests that run the real opencode binary (pinned in mise.toml) use a fake
// model, so they spend no model usage. Without the binary they skip locally
// and fail in CI.

const testModel = "openai/gpt-test"

// startSession starts a session in a fresh workdir, isolated from the
// developer's opencode data and configuration.
func startSession(t *testing.T, f *runnertest.FakeModel, cfg runner.SessionConfig) (runner.Session, *runnertest.Recorder, string) {
	t.Helper()
	bin := runnertest.Binary(t, "opencode")
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

// The behaviors every adapter shares are in conformance_test.go; these are
// opencode's own.

// The session is created with a title, so opencode does not spend a model
// call naming it: the only requests are the task's.
func TestSessionTitleCostsNoModelCall(t *testing.T) {
	f := runnertest.NewFakeModel(t, shellTool)
	s, rec, _ := startSession(t, f, runner.SessionConfig{})
	if err := s.Send("Create hello.txt"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(90 * time.Second):
		t.Fatal("session did not end")
	}
	if d := rec.Done(t); d.Status != runner.DoneSucceeded {
		t.Fatalf("done = %+v", d)
	}
	if all, task := f.Requests(), f.TaskRequests(); len(all) != 2 || len(task) != 2 {
		t.Errorf("model calls = %d (%d with tools), want only the task's 2: %+v", len(all), len(task), all)
	}
}

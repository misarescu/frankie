package runnertest

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// Smoke tests against a real model, through OpenRouter in place of the LLM
// gateway (both serve the OpenAI-compatible API). They spend real usage, so
// adapters run them only under the smoke build tag (mise run
// test:smoke-<harness>), and they skip without OPENROUTER_API_KEY.
const (
	// SmokeGateway is OpenRouter's API root; adapters append /v1 as for the gateway.
	SmokeGateway = "https://openrouter.ai/api"
	// SmokeModel is a cheap model with tool calling; FRANKIE_SMOKE_MODEL overrides it.
	SmokeModel = "openai/gpt-5.6-luna"
	smokeWait  = 4 * time.Minute
)

// RunSmoke checks an adapter against a real model: the model calls the shell
// tool, and a message sent while a command runs changes what the session
// does (docs/spec/10, M4: "a steer message changes the behavior of a running
// session in each").
func RunSmoke(t *testing.T, a Adapter) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	bin := Binary(t, a.Binary)
	model := cmp.Or(os.Getenv("FRANKIE_SMOKE_MODEL"), SmokeModel)
	start := func(t *testing.T) (*session, string) {
		t.Helper()
		env := HomeEnv(t)
		for k, v := range a.Env {
			env[k] = v
		}
		work := t.TempDir()
		rec := &Recorder{}
		s, err := a.New(t, bin).Start(t.Context(), runner.SessionConfig{
			Workdir: work, Model: model, Env: env,
			SystemPrompt: "You are a test agent. Do exactly what you are asked, using your shell tool for commands. Be brief.",
			Gateway:      runner.Gateway{BaseURL: SmokeGateway, APIKey: key, API: runner.ModelAPIOpenAI},
		}, rec.Emit)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.Abort(runner.DoneAborted, "test cleanup")
			<-s.Done()
		})
		return &session{Session: s, rec: rec, work: work}, work
	}
	wait := func(t *testing.T, s *session) runner.Done {
		t.Helper()
		select {
		case <-s.Done():
		case <-time.After(smokeWait):
			t.Fatalf("the session did not end; events: %+v", s.rec.All())
		}
		for _, e := range s.rec.All() {
			t.Logf("%-18s %+v", e.Type, e.Payload)
		}
		checkStream(t, s.rec.All())
		return s.rec.Done(t)
	}

	t.Run("task", func(t *testing.T) {
		s, work := start(t)
		s.send(t, "Run this shell command: echo frankie > smoke.txt\nThen reply with the single word DONE.")
		if d := wait(t, s); d.Status != runner.DoneSucceeded {
			t.Fatalf("done = %+v", d)
		}
		if b, err := os.ReadFile(filepath.Join(work, "smoke.txt")); err != nil || strings.TrimSpace(string(b)) != "frankie" {
			t.Errorf("smoke.txt = %q, %v", b, err)
		}
		usage := payloads[runner.TokenUsage](s.rec.All())
		if len(usage) == 0 || usage[len(usage)-1].InputTokens == 0 || usage[len(usage)-1].OutputTokens == 0 {
			t.Errorf("token usage = %+v", usage)
		}
	})

	t.Run("steer changes the running session", func(t *testing.T) {
		s, work := start(t)
		s.send(t, "Run this shell command: sleep 10 && echo one > first.txt\nThen reply with the single word DONE.")
		// Steer while the command runs: after the tool call, before its output.
		deadline := time.Now().Add(smokeWait)
		for !slices.ContainsFunc(s.rec.All(), func(e Event) bool { return e.Type == domain.EventToolCall }) {
			if time.Now().After(deadline) {
				t.Fatalf("the model never called a tool; events: %+v", s.rec.All())
			}
			time.Sleep(100 * time.Millisecond)
		}
		s.send(t, "Before you reply, also run this shell command: echo two > second.txt")
		if d := wait(t, s); d.Status != runner.DoneSucceeded {
			t.Fatalf("done = %+v", d)
		}
		for _, f := range []string{"first.txt", "second.txt"} {
			if _, err := os.Stat(filepath.Join(work, f)); err != nil {
				t.Errorf("%s: %v (the steer did not change the session)", f, err)
			}
		}
	})
}

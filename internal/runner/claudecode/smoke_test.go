//go:build smoke

// Smoke test against the real Claude Code CLI. It spends real model usage, so
// it only runs on demand: mise run test:smoke-claude
package claudecode

import (
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// Verifies the two protocol assumptions the adapter relies on:
//  1. a message sent while a turn is running gets its own result line;
//  2. closing stdin after the last result makes claude exit cleanly.
func TestSmokeRealClaude(t *testing.T) {
	h := &Harness{}
	rec := &recorder{}
	s, err := h.Start(t.Context(), runner.SessionConfig{
		Workdir:  t.TempDir(),
		Model:    "haiku",
		MaxTurns: 3,
	}, rec.emit)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Abort(runner.DoneAborted, "test cleanup"); <-s.Done() })

	if err := s.Send("Reply with exactly the word ALPHA and nothing else. Do not use any tools."); err != nil {
		t.Fatalf("Send prompt: %v", err)
	}
	// Sent before the first turn finishes: must be queued and answered, not merged or dropped.
	if err := s.Send("Now reply with exactly the word BETA and nothing else. Do not use any tools."); err != nil {
		t.Fatalf("Send steer: %v", err)
	}

	select {
	case <-s.Done():
	case <-time.After(3 * time.Minute):
		t.Fatalf("session did not end; events so far: %+v", rec.all())
	}

	for _, e := range rec.all() {
		t.Logf("%-18s %v", e.typ, e.payload)
	}
	done := rec.done(t)
	if done["status"] != "succeeded" {
		t.Fatalf("done = %v", done)
	}
	var results int
	var text strings.Builder
	for _, e := range rec.all() {
		switch e.typ {
		case domain.EventTokenUsage:
			results++
		case domain.EventAssistantMessage:
			text.WriteString(e.payload["text"].(string) + " ")
		}
	}
	if results != 2 {
		t.Errorf("turn results = %d, want 2 (one per message)", results)
	}
	if !strings.Contains(text.String(), "ALPHA") || !strings.Contains(text.String(), "BETA") {
		t.Errorf("assistant text %q missing ALPHA or BETA", text.String())
	}
}

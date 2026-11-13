//go:build smoke

// Smoke test against the real Claude Code CLI. It spends real model usage, so
// it only runs on demand: mise run test:smoke-claude
package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
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

// Verifies the restricted tool flags a reviewer runs with (tools.go) against
// the real CLI: a read-only shell command runs, and files cannot be written,
// through the Write tool or the shell. Run it with
// mise exec -- go test -tags smoke -count=1 -v -run '^TestSmokeRealClaudeReadOnlyTools$' ./internal/runner/claudecode/
func TestSmokeRealClaudeReadOnlyTools(t *testing.T) {
	readonly := []string{"read", "grep", "glob", "bash:readonly"}
	session := func(t *testing.T, dir, prompt string) []emitted {
		t.Helper()
		h := &Harness{}
		rec := &recorder{}
		s, err := h.Start(t.Context(), runner.SessionConfig{Workdir: dir, Model: "haiku", MaxTurns: 8, AllowedTools: readonly}, rec.emit)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() { s.Abort(runner.DoneAborted, "test cleanup"); <-s.Done() })
		if err := s.Send(prompt); err != nil {
			t.Fatalf("Send: %v", err)
		}
		select {
		case <-s.Done():
		case <-time.After(3 * time.Minute):
			t.Fatalf("session did not end; events so far: %+v", rec.all())
		}
		for _, e := range rec.all() {
			t.Logf("%-18s %v", e.typ, e.payload)
		}
		if done := rec.done(t); done["status"] != "succeeded" {
			t.Fatalf("done = %v", done)
		}
		return rec.all()
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("The secret word is OMEGA.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("a read-only shell command runs", func(t *testing.T) {
		ran := false
		for _, e := range session(t, dir, "Run the shell command `ls` (exactly that, nothing else) and report its output.") {
			if e.typ == domain.EventCommandOutput && e.payload["is_error"] != true && strings.Contains(fmt.Sprint(e.payload["output"]), "README.md") {
				ran = true
			}
		}
		if !ran {
			t.Error("`ls` did not run")
		}
	})
	t.Run("files cannot be written", func(t *testing.T) {
		session(t, dir, "Create a file notes.txt containing OMEGA, first with your file writing tool if you have one, "+
			"then with the shell command `echo OMEGA > notes.txt`. Report what happened.")
		if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err == nil {
			t.Error("notes.txt was written despite the read-only tools")
		}
	})
}

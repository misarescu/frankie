package claudecode

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// TestFakeClaude is not a test: it is the fake claude binary used by the session
// tests, run as a child process of the test binary when FAKE_CLAUDE_MODE is set.
func TestFakeClaude(t *testing.T) {
	mode := os.Getenv("FAKE_CLAUDE_MODE")
	if mode == "" {
		t.Skip("helper process")
	}
	os.Exit(fakeClaude(mode))
}

func fakeClaude(mode string) int {
	args := os.Args[slices.Index(os.Args, "--")+1:]
	if !slices.Contains(args, "stream-json") || !slices.Contains(args, "-p") {
		fmt.Fprintln(os.Stderr, "fake claude: not started in stream-json print mode:", args)
		return 2
	}
	if mode == "trap" {
		signal.Ignore(syscall.SIGTERM)
	}
	out := json.NewEncoder(os.Stdout)
	emit := func(v map[string]any) { _ = out.Encode(v) }
	cwd, _ := os.Getwd()
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": "fake-session"})

	if mode == "exit0" {
		return 0
	}
	in := bufio.NewScanner(os.Stdin)
	n := 0
	for in.Scan() {
		var msg struct {
			Message struct {
				Content []struct{ Text string } `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(in.Bytes(), &msg); err != nil || len(msg.Message.Content) == 0 {
			fmt.Fprintln(os.Stderr, "fake claude: bad input line:", in.Text())
			return 2
		}
		n++
		text := msg.Message.Content[0].Text
		switch mode {
		case "crash":
			fmt.Fprintln(os.Stderr, "boom")
			return 3
		case "hang", "trap":
			time.Sleep(time.Hour)
		case "slow":
			time.Sleep(150 * time.Millisecond)
		}
		emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{
			{"type": "text", "text": fmt.Sprintf("ack %d: %s | cwd=%s probe=%s", n, text, cwd, os.Getenv("FRANKIE_PROBE"))},
			{"type": "tool_use", "id": fmt.Sprintf("tu_%d", n), "name": "Bash", "input": map[string]string{"command": "true"}},
		}}})
		emit(map[string]any{"type": "user", "message": map[string]any{"content": []map[string]any{
			{"type": "tool_result", "tool_use_id": fmt.Sprintf("tu_%d", n), "content": "done"},
		}}})
		isErr := mode == "error_result"
		subtype := "success"
		if isErr {
			subtype = "error_max_turns"
		}
		emit(map[string]any{"type": "result", "subtype": subtype, "is_error": isErr, "result": "final " + text,
			"num_turns": n, "session_id": "fake-session", "usage": map[string]int{"input_tokens": 10 * n, "output_tokens": n}})
	}
	return 0
}

type recorder struct {
	mu     sync.Mutex
	events []emitted
}

func (r *recorder) emit(typ domain.EventType, p any) {
	raw, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, emitted{typ, m})
}

func (r *recorder) all() []emitted {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func (r *recorder) done(t *testing.T) map[string]any {
	t.Helper()
	evs := r.all()
	if len(evs) == 0 || evs[len(evs)-1].typ != domain.EventDone {
		t.Fatalf("stream does not end with done: %+v", evs)
	}
	for _, e := range evs[:len(evs)-1] {
		if e.typ == domain.EventDone {
			t.Fatalf("done emitted more than once: %+v", evs)
		}
	}
	return evs[len(evs)-1].payload
}

func (r *recorder) texts() []string {
	var out []string
	for _, e := range r.all() {
		if e.typ == domain.EventAssistantMessage {
			out = append(out, e.payload["text"].(string))
		}
	}
	return out
}

func startFake(t *testing.T, mode string, cfg runner.SessionConfig) (runner.Session, *recorder) {
	t.Helper()
	t.Setenv("FAKE_CLAUDE_MODE", mode)
	h := &Harness{Binary: os.Args[0], KillGrace: 300 * time.Millisecond,
		prefixArgs: []string{"-test.run=^TestFakeClaude$", "--"}}
	rec := &recorder{}
	s, err := h.Start(t.Context(), cfg, rec.emit)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Abort(runner.DoneAborted, "test cleanup"); <-s.Done() })
	return s, rec
}

func waitDone(t *testing.T, s runner.Session, within time.Duration) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(within):
		t.Fatalf("session not done within %s", within)
	}
}

func TestSessionRunsPromptToCompletion(t *testing.T) {
	dir := t.TempDir()
	s, rec := startFake(t, "normal", runner.SessionConfig{Workdir: dir, Env: map[string]string{"FRANKIE_PROBE": "virtual-key"}})
	if err := s.Send("fix the bug"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitDone(t, s, 10*time.Second)

	done := rec.done(t)
	if done["status"] != "succeeded" || done["harness_session_id"] != "fake-session" || done["result"] != "final fix the bug" {
		t.Errorf("done = %v", done)
	}
	texts := rec.texts()
	if len(texts) != 1 || !strings.Contains(texts[0], "fix the bug") {
		t.Fatalf("assistant messages = %q", texts)
	}
	// Session config reaches the process: working directory and environment.
	wantDir, _ := filepath.EvalSymlinks(dir) // macOS temp dirs live behind a symlink
	if !strings.Contains(texts[0], "cwd="+wantDir+" ") {
		t.Errorf("harness did not run in workdir %s: %q", wantDir, texts[0])
	}
	if !strings.Contains(texts[0], "probe=virtual-key") {
		t.Errorf("env not passed: %q", texts[0])
	}
	var types []domain.EventType
	for _, e := range rec.all() {
		types = append(types, e.typ)
	}
	want := []domain.EventType{domain.EventAssistantMessage, domain.EventToolCall, domain.EventCommandOutput, domain.EventTokenUsage, domain.EventDone}
	if !slices.Equal(types, want) {
		t.Errorf("event types = %v, want %v", types, want)
	}
	if err := s.Send("too late"); !errors.Is(err, runner.ErrSessionFinished) {
		t.Errorf("Send after done: err = %v, want ErrSessionFinished", err)
	}
}

// A steer message sent while a turn runs is answered before the session ends.
func TestSessionSteerIsAnsweredBeforeExit(t *testing.T) {
	s, rec := startFake(t, "slow", runner.SessionConfig{Workdir: t.TempDir()})
	if err := s.Send("task"); err != nil {
		t.Fatalf("Send task: %v", err)
	}
	if err := s.Send("also add a test"); err != nil {
		t.Fatalf("Send steer: %v", err)
	}
	waitDone(t, s, 10*time.Second)
	if done := rec.done(t); done["status"] != "succeeded" {
		t.Errorf("done = %v", done)
	}
	texts := rec.texts()
	if len(texts) != 2 || !strings.Contains(texts[1], "also add a test") {
		t.Errorf("assistant messages = %q; want both messages answered", texts)
	}
}

func TestSessionFailures(t *testing.T) {
	tests := []struct {
		mode       string
		send       bool
		wantReason string
		wantStderr string
		wantExit   float64
	}{
		{mode: "error_result", send: true, wantReason: "error_max_turns"},
		{mode: "crash", send: true, wantReason: "harness exited", wantStderr: "boom", wantExit: 3},
		{mode: "exit0", send: false, wantReason: "before receiving a prompt"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			s, rec := startFake(t, tt.mode, runner.SessionConfig{Workdir: t.TempDir()})
			if tt.send {
				if err := s.Send("go"); err != nil && !errors.Is(err, runner.ErrSessionFinished) {
					t.Fatalf("Send: %v", err)
				}
			}
			waitDone(t, s, 10*time.Second)
			done := rec.done(t)
			if done["status"] != "failed" || !strings.Contains(done["reason"].(string), tt.wantReason) {
				t.Errorf("done = %v; want failed with reason containing %q", done, tt.wantReason)
			}
			if done["exit_code"] != tt.wantExit {
				t.Errorf("exit_code = %v, want %v", done["exit_code"], tt.wantExit)
			}
			if tt.wantStderr != "" {
				found := false
				for _, e := range rec.all() {
					if e.typ == domain.EventError && strings.Contains(e.payload["detail"].(string), tt.wantStderr) {
						found = true
					}
				}
				if !found {
					t.Errorf("no error event with stderr %q: %+v", tt.wantStderr, rec.all())
				}
			}
		})
	}
}

func TestSessionAbort(t *testing.T) {
	for _, mode := range []string{"hang", "trap"} { // trap ignores SIGTERM, forcing SIGKILL
		t.Run(mode, func(t *testing.T) {
			s, rec := startFake(t, mode, runner.SessionConfig{Workdir: t.TempDir()})
			if err := s.Send("work forever"); err != nil {
				t.Fatalf("Send: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
			s.Abort(runner.DoneTimeout, "idle")
			s.Abort(runner.DoneAborted, "second abort is ignored")
			waitDone(t, s, 5*time.Second)
			if done := rec.done(t); done["status"] != "timeout" || done["reason"] != "idle" {
				t.Errorf("done = %v", done)
			}
		})
	}
}

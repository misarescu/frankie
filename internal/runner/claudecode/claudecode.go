// Package claudecode drives Claude Code headlessly through its stream-json
// protocol on stdin/stdout and normalizes its output for the runner.
package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// Harness starts Claude Code sessions. The zero value runs "claude" from PATH.
//
// Permission prompts are disabled (--dangerously-skip-permissions): the agent pod
// is the sandbox (docs/spec/05-sandbox-and-toolchains.md). Claude Code refuses
// that flag as root, which matches the non-root pod requirement (FR-SB-1). A
// role's allowed_tools narrows the tools instead (tools.go).
type Harness struct {
	// Binary is the claude executable; default "claude".
	Binary string
	// KillGrace is how long Abort waits after SIGTERM before SIGKILL; default 5s.
	KillGrace time.Duration

	prefixArgs []string // tests: arguments placed before the claude flags
}

var _ runner.Harness = (*Harness)(nil)

// Args returns the claude command-line flags for a session.
func Args(cfg runner.SessionConfig) ([]string, error) {
	tools, err := toolArgs(cfg.AllowedTools)
	if err != nil {
		return nil, err
	}
	args := append([]string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}, tools...)
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", cfg.SystemPrompt)
	}
	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(cfg.MaxTurns))
	}
	return args, nil
}

// Start launches claude and begins translating its output.
func (h *Harness) Start(ctx context.Context, cfg runner.SessionConfig, emit runner.Emit) (runner.Session, error) {
	bin := h.Binary
	if bin == "" {
		bin = "claude"
	}
	grace := h.KillGrace
	if grace <= 0 {
		grace = 5 * time.Second
	}
	args, err := Args(cfg)
	if err != nil {
		return nil, err
	}
	// ctx is the session context; it is not canceled, Abort stops the process.
	cmd := exec.CommandContext(ctx, bin, append(slices.Clone(h.prefixArgs), args...)...)
	cmd.Dir = cfg.Workdir
	cmd.Env = os.Environ()
	for _, k := range slices.Sorted(maps.Keys(cfg.Env)) {
		cmd.Env = append(cmd.Env, k+"="+cfg.Env[k])
	}
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claudecode: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claudecode: stdout: %w", err)
	}
	stderr := &tailBuffer{limit: 8 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claudecode: start %s: %w", bin, err)
	}

	s := &session{cmd: cmd, stdin: stdin, emit: emit, tr: newTranslator(emit), stderr: stderr, grace: grace, done: make(chan struct{})}
	readDone := make(chan struct{})
	go s.read(stdout, readDone)
	go s.wait(readDone)
	return s, nil
}

type session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	emit   runner.Emit
	tr     *translator
	stderr *tailBuffer
	grace  time.Duration
	done   chan struct{}

	mu          sync.Mutex
	written     int // user messages sent
	answered    int // turns finished (result lines)
	inputClosed bool
	abortStatus runner.DoneStatus
	abortReason string
}

// userMessage is one stream-json input line.
func userMessage(text string) ([]byte, error) {
	line, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]string{{"type": "text", "text": text}},
		},
	})
	return append(line, '\n'), err
}

func (s *session) Send(text string) error {
	line, err := userMessage(text)
	if err != nil {
		return fmt.Errorf("claudecode: encode message: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputClosed {
		return runner.ErrSessionFinished
	}
	if _, err := s.stdin.Write(line); err != nil {
		return fmt.Errorf("claudecode: write message: %w", err)
	}
	s.written++
	return nil
}

// turnEnded closes stdin once every message has been answered, which lets claude exit.
func (s *session) turnEnded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answered++
	if s.answered >= s.written && !s.inputClosed {
		s.inputClosed = true
		_ = s.stdin.Close()
	}
}

func (s *session) Abort(status runner.DoneStatus, reason string) {
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return
	default:
	}
	if s.abortStatus != "" {
		s.mu.Unlock()
		return
	}
	s.abortStatus, s.abortReason = status, reason
	if !s.inputClosed {
		s.inputClosed = true
		_ = s.stdin.Close()
	}
	s.mu.Unlock()

	signalGroup(s.cmd, sigTerm)
	go func() {
		select {
		case <-s.done:
		case <-time.After(s.grace):
			signalGroup(s.cmd, sigKill)
		}
	}()
}

func (s *session) Done() <-chan struct{} { return s.done }

func (s *session) read(stdout io.Reader, readDone chan<- struct{}) {
	defer close(readDone)
	r := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && s.tr.handle(line) {
			s.turnEnded()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.emit(domain.EventError, runner.Error{Message: "reading harness output", Detail: err.Error()})
			}
			return
		}
	}
}

// wait reaps the process and emits the final done event.
func (s *session) wait(readDone <-chan struct{}) {
	<-readDone
	waitErr := s.cmd.Wait()
	exitCode := s.cmd.ProcessState.ExitCode()

	s.mu.Lock()
	s.inputClosed = true
	done := runner.Done{ExitCode: exitCode, HarnessSessionID: s.tr.sessionID}
	if s.tr.last != nil {
		done.Result = s.tr.last.text
	}
	switch {
	case s.abortStatus != "":
		done.Status, done.Reason = s.abortStatus, s.abortReason
	case waitErr != nil:
		done.Status, done.Reason = runner.DoneFailed, fmt.Sprintf("harness exited: %v", waitErr)
	case s.written == 0:
		done.Status, done.Reason = runner.DoneFailed, "harness exited before receiving a prompt"
	case s.answered < s.written:
		done.Status, done.Reason = runner.DoneFailed, fmt.Sprintf("harness exited with %d unanswered message(s)", s.written-s.answered)
	case s.tr.last != nil && s.tr.last.isError:
		done.Status, done.Reason = runner.DoneFailed, "harness reported an error result: "+s.tr.last.subtype
	default:
		done.Status = runner.DoneSucceeded
	}
	s.mu.Unlock()

	if done.Status == runner.DoneFailed {
		if tail := s.stderr.String(); tail != "" {
			s.emit(domain.EventError, runner.Error{Message: "harness stderr", Detail: tail})
		}
	}
	s.emit(domain.EventDone, done)
	close(s.done)
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

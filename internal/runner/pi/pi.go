// Package pi drives pi headlessly through its RPC mode (JSONL commands on
// stdin, responses and session events on stdout) and normalizes its events
// for the runner.
//
// The task and steering messages are prompt commands with streamingBehavior
// "steer": pi delivers a message sent mid-run after the current turn's tool
// calls, before its next model call. When pi settles (no retries, recovery or
// queued messages left) and get_state confirms it is idle with nothing
// pending, the session ends: the runner closes stdin and pi exits.
package pi

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// Harness starts pi sessions. The zero value runs "pi" from PATH.
type Harness struct {
	// Binary is the pi executable; default "pi".
	Binary string
	// KillGrace is how long a stop waits after SIGTERM before SIGKILL; default 5s.
	KillGrace time.Duration
	// ShutdownGrace is how long pi may take to exit after its stdin closes
	// before it is terminated; default 30s.
	ShutdownGrace time.Duration
	// StartTimeout bounds pi's startup; default 60s.
	StartTimeout time.Duration
	// TempDir holds pi's agent directory, outside the workspace so it is never
	// committed; default os.TempDir().
	TempDir string
}

var _ runner.Harness = (*Harness)(nil)

// callTimeout bounds a command's response.
const callTimeout = 30 * time.Second

// Start launches pi in RPC mode and checks it answers.
func (h *Harness) Start(ctx context.Context, cfg runner.SessionConfig, emit runner.Emit) (runner.Session, error) {
	bin := cmp.Or(h.Binary, "pi")
	dir, err := os.MkdirTemp(h.TempDir, "frankie-pi-")
	if err != nil {
		return nil, fmt.Errorf("pi: temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	args, env, err := prepare(cfg, dir)
	if err != nil {
		cleanup()
		return nil, err
	}

	// ctx is the session context; it is not canceled, Abort stops the process.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cfg.Workdir
	cmd.Env = os.Environ()
	for _, k := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	runner.SetProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("pi: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("pi: stdout: %w", err)
	}
	stderr := runner.NewTailBuffer(8 << 10)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("pi: start %s: %w", bin, err)
	}

	s := &session{
		cmd: cmd, stdin: stdin, emit: emit, tr: newTranslator(emit), stderr: stderr, cleanup: cleanup,
		killGrace: cmp.Or(h.KillGrace, 5*time.Second), shutdownGrace: cmp.Or(h.ShutdownGrace, 30*time.Second),
		pending: map[string]chan record{}, readDone: make(chan struct{}), exited: make(chan struct{}), done: make(chan struct{}),
	}
	go s.read(stdout)
	go s.reap()

	// get_state doubles as the readiness check: pi answers once it is set up.
	startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cmp.Or(h.StartTimeout, 60*time.Second))
	defer cancel()
	var state struct {
		SessionID string `json:"sessionId"`
	}
	if err := s.call(startCtx, map[string]any{"type": "get_state"}, &state); err != nil {
		s.terminate()
		<-s.exited
		<-s.readDone
		cleanup()
		if tail := stderr.String(); tail != "" {
			err = fmt.Errorf("%w; stderr: %s", err, truncate(tail, 2048))
		}
		return nil, fmt.Errorf("pi: start: %w", err)
	}
	s.sessionID = state.SessionID
	go s.wait()
	return s, nil
}

// prepare writes the agent directory and returns pi's arguments and environment.
func prepare(cfg runner.SessionConfig, dir string) ([]string, map[string]string, error) {
	models, err := Models(cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), models, 0o600); err != nil {
		return nil, nil, fmt.Errorf("pi: models.json: %w", err)
	}
	promptFile := ""
	if cfg.SystemPrompt != "" {
		promptFile = filepath.Join(dir, "role.md")
		if err := os.WriteFile(promptFile, []byte(cfg.SystemPrompt), 0o600); err != nil {
			return nil, nil, fmt.Errorf("pi: role prompt: %w", err)
		}
	}
	_, readonly, err := tools(cfg.AllowedTools)
	if err != nil {
		return nil, nil, err
	}
	extensionFile := ""
	if readonly {
		extensionFile = filepath.Join(dir, "frankie-readonly.ts")
		if err := os.WriteFile(extensionFile, readonlyExtension, 0o600); err != nil {
			return nil, nil, fmt.Errorf("pi: read-only extension: %w", err)
		}
	}
	args, err := Args(cfg, promptFile, extensionFile)
	if err != nil {
		return nil, nil, err
	}
	env := maps.Clone(cfg.Env)
	if env == nil {
		env = map[string]string{}
	}
	own, err := environment(cfg, dir)
	if err != nil {
		return nil, nil, err
	}
	maps.Copy(env, own)
	return args, env, nil
}

type session struct {
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	emit          runner.Emit
	tr            *translator // read goroutine only, until readDone
	stderr        *runner.TailBuffer
	cleanup       func()
	killGrace     time.Duration
	shutdownGrace time.Duration
	sessionID     string
	readDone      chan struct{}
	exited        chan struct{} // closed once the process was reaped
	done          chan struct{}
	waitErr       error

	// Commands and their responses.
	writeMu sync.Mutex
	pendMu  sync.Mutex
	nextID  int
	pending map[string]chan record

	mu          sync.Mutex
	sent        int  // prompts accepted
	finished    bool // no more input; pi is shutting down
	succeeded   bool
	failReason  string
	abortStatus runner.DoneStatus
	abortReason string
	stopOnce    sync.Once
}

// Send hands a message to pi. The first is the task; later ones steer the
// running agent at its next model call.
func (s *session) Send(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return runner.ErrSessionFinished
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := s.call(ctx, map[string]any{"type": "prompt", "message": text, "streamingBehavior": "steer"}, nil); err != nil {
		return err
	}
	s.sent++
	return nil
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
	s.finished = true
	s.mu.Unlock()
	_ = s.stdin.Close()
	s.terminate()
}

func (s *session) Done() <-chan struct{} { return s.done }

// call sends a command and waits for its response; out, if given, receives its data.
func (s *session) call(ctx context.Context, cmd map[string]any, out any) error {
	s.pendMu.Lock()
	s.nextID++
	id := "frankie-" + strconv.Itoa(s.nextID)
	ch := make(chan record, 1)
	s.pending[id] = ch
	s.pendMu.Unlock()
	defer func() {
		s.pendMu.Lock()
		delete(s.pending, id)
		s.pendMu.Unlock()
	}()

	cmd["id"] = id
	line, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	_, err = s.stdin.Write(append(line, '\n'))
	s.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("pi: %s: %w", cmd["type"], err)
	}
	select {
	case r := <-ch:
		if r.Success == nil || !*r.Success {
			return fmt.Errorf("pi: %s refused: %s", cmd["type"], r.Error)
		}
		if out != nil && len(r.Data) > 0 {
			return json.Unmarshal(r.Data, out)
		}
		return nil
	case <-s.readDone:
		return fmt.Errorf("pi: %s: pi exited", cmd["type"])
	case <-ctx.Done():
		return fmt.Errorf("pi: %s: no response: %w", cmd["type"], ctx.Err())
	}
}

// read dispatches responses to their callers and events to the translator.
func (s *session) read(stdout io.Reader) {
	defer close(s.readDone)
	r := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			s.handle(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.emit(domain.EventError, runner.Error{Message: "reading harness output", Detail: err.Error()})
			}
			return
		}
	}
}

func (s *session) handle(line []byte) {
	var r record
	if err := json.Unmarshal(line, &r); err != nil {
		s.emit(domain.EventError, runner.Error{Message: "unparseable harness output", Detail: truncate(string(line), 512)})
		return
	}
	if r.Type == "response" {
		s.pendMu.Lock()
		ch, ok := s.pending[r.ID]
		s.pendMu.Unlock()
		switch {
		case ok:
			ch <- r
		case r.Success == nil || !*r.Success:
			s.emit(domain.EventError, runner.Error{Message: "harness refused a command: " + r.Command, Detail: r.Error})
		}
		return
	}
	if settled, aborted := s.tr.handle(r); settled {
		// Checking needs a response from this reader, so it runs apart.
		go s.settled(aborted, s.tr.lastError)
	}
}

// settled ends the session once pi is idle with nothing pending. Send holds
// the same lock while its prompt is accepted, so a message is either seen
// here as streaming or pending work, or refused with ErrSessionFinished.
func (s *session) settled(aborted bool, lastError string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var state struct {
		IsStreaming         bool `json:"isStreaming"`
		IsCompacting        bool `json:"isCompacting"`
		PendingMessageCount int  `json:"pendingMessageCount"`
	}
	if err := s.call(ctx, map[string]any{"type": "get_state"}, &state); err != nil {
		s.finishLocked(false, "reading the harness state: "+err.Error())
		return
	}
	if state.IsStreaming || state.IsCompacting || state.PendingMessageCount > 0 {
		return
	}
	switch {
	case aborted:
		s.finishLocked(false, "the harness aborted the run")
	case lastError != "":
		s.finishLocked(false, "model request failed: "+lastError)
	default:
		s.finishLocked(true, "")
	}
}

// finishLocked closes pi's stdin, which asks it to exit once its work is
// disposed, and terminates it if it takes too long.
func (s *session) finishLocked(ok bool, reason string) {
	if s.finished {
		return
	}
	s.finished, s.succeeded, s.failReason = true, ok, reason
	_ = s.stdin.Close()
	go func() {
		select {
		case <-s.exited:
		case <-time.After(s.shutdownGrace):
			s.terminate()
		}
	}()
}

// terminate stops pi's process group, then kills it after the grace period.
func (s *session) terminate() {
	s.stopOnce.Do(func() {
		runner.TerminateGroup(s.cmd)
		go func() {
			select {
			case <-s.exited:
			case <-time.After(s.killGrace):
				runner.KillGroup(s.cmd)
			}
		}()
	})
}

// reap waits for the process once its output is drained.
func (s *session) reap() {
	<-s.readDone
	s.waitErr = s.cmd.Wait()
	close(s.exited)
}

// wait emits the final done event once pi exited.
func (s *session) wait() {
	<-s.exited

	s.mu.Lock()
	s.finished = true
	done := runner.Done{ExitCode: s.cmd.ProcessState.ExitCode(), HarnessSessionID: s.sessionID, Result: s.tr.lastText}
	switch {
	case s.abortStatus != "":
		done.Status, done.Reason = s.abortStatus, s.abortReason
	case s.succeeded && s.waitErr == nil:
		done.Status = runner.DoneSucceeded
	case s.succeeded:
		// It did the work but did not exit cleanly; the work is still pushed.
		done.Status = runner.DoneSucceeded
		s.emit(domain.EventError, runner.Error{Message: "harness exited uncleanly after finishing", Detail: s.waitErr.Error()})
	case s.failReason != "":
		done.Status, done.Reason = runner.DoneFailed, s.failReason
	case s.sent == 0:
		done.Status, done.Reason = runner.DoneFailed, "harness exited before receiving a prompt"
	default:
		done.Status, done.Reason = runner.DoneFailed, fmt.Sprintf("harness exited: %v", s.waitErr)
	}
	s.mu.Unlock()

	if done.Status == runner.DoneFailed {
		if tail := s.stderr.String(); tail != "" {
			s.emit(domain.EventError, runner.Error{Message: "harness stderr", Detail: tail})
		}
	}
	s.cleanup()
	s.emit(domain.EventDone, done)
	close(s.done)
}

// Package opencode drives opencode headlessly through its HTTP server
// (opencode serve) and normalizes its events for the runner.
//
// Each session starts a private server on a loopback port with a random
// password, configured through OPENCODE_CONFIG_CONTENT (config.go). The runner
// creates one opencode session, sends the task and steering messages as
// prompts with delivery "steer" (opencode hands them to the running agent loop
// at its next model call), and follows the server's event stream. The session
// ends when an execution ends with nothing left in the opencode session's
// inbox; the runner then stops the server.
package opencode

import (
	"bufio"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// Harness starts opencode sessions. The zero value runs "opencode" from PATH.
type Harness struct {
	// Binary is the opencode executable; default "opencode".
	Binary string
	// KillGrace is how long a stop waits after SIGTERM before SIGKILL; default 5s.
	KillGrace time.Duration
	// StartTimeout bounds the server's startup; default 60s.
	StartTimeout time.Duration
	// TempDir holds the role prompt file, outside the workspace so it is never
	// committed; default os.TempDir().
	TempDir string
}

var _ runner.Harness = (*Harness)(nil)

// listeningRE matches the server's startup line on stdout.
var listeningRE = regexp.MustCompile(`server listening on (http://\S+)`)

// Start launches a private opencode server and creates the session on it.
func (h *Harness) Start(ctx context.Context, cfg runner.SessionConfig, emit runner.Emit) (runner.Session, error) {
	bin := cmp.Or(h.Binary, "opencode")
	grace := cmp.Or(h.KillGrace, 5*time.Second)
	startTimeout := cmp.Or(h.StartTimeout, 60*time.Second)

	dir, err := os.MkdirTemp(h.TempDir, "frankie-opencode-")
	if err != nil {
		return nil, fmt.Errorf("opencode: temp dir: %w", err)
	}
	promptFile := ""
	if cfg.SystemPrompt != "" {
		promptFile = filepath.Join(dir, "role.md")
		if err := os.WriteFile(promptFile, []byte(cfg.SystemPrompt), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("opencode: role prompt: %w", err)
		}
	}
	config, err := Config(cfg, promptFile)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	password, err := randomPassword()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	// ctx is the session context; it is not canceled, Abort stops the process.
	cmd := exec.CommandContext(ctx, bin, "serve", "--hostname", "127.0.0.1", "--port", "0")
	cmd.Dir = cfg.Workdir
	env := maps.Clone(cfg.Env)
	if env == nil {
		env = map[string]string{}
	}
	maps.Copy(env, environment(cfg, config, password))
	cmd.Env = os.Environ()
	for _, k := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	runner.SetProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("opencode: stdout: %w", err)
	}
	stderr := runner.NewTailBuffer(8 << 10)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("opencode: start %s: %w", bin, err)
	}
	s := &session{cmd: cmd, emit: emit, stderr: stderr, grace: grace, tempDir: dir, done: make(chan struct{}), exited: make(chan struct{})}
	go s.reap()

	fail := func(err error) (runner.Session, error) {
		s.stop()
		<-s.exited
		_ = os.RemoveAll(dir)
		if tail := stderr.String(); tail != "" {
			err = fmt.Errorf("%w; stderr: %s", err, truncate(tail, 2048))
		}
		return nil, err
	}
	base, err := waitListening(stdout, startTimeout, s.exited)
	if err != nil {
		return fail(err)
	}
	s.c = newClient(base, password)
	if err := s.c.ready(ctx, startTimeout, s.exited); err != nil {
		return fail(err)
	}

	var created struct {
		ID string `json:"id"`
	}
	err = s.c.do(ctx, "POST", "/api/session", map[string]any{
		// A title spares a model call to name the session.
		"title":    "frankie",
		"location": map[string]string{"directory": cfg.Workdir},
		"model":    map[string]string{"providerID": providerID, "id": cfg.Model},
	}, &created)
	if err != nil {
		return fail(err)
	}
	s.id = created.ID
	s.tr = newTranslator(emit, s.id)

	evCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stream, err := s.c.events(evCtx)
	if err != nil {
		cancel()
		return fail(err)
	}
	s.cancelEvents = cancel
	s.readDone = make(chan struct{})
	go s.read(stream)
	go s.wait()
	return s, nil
}

// waitListening reads the server's address from its startup line, then keeps
// draining stdout so the server never blocks on it.
func waitListening(stdout io.Reader, timeout time.Duration, exited <-chan struct{}) (string, error) {
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sent := false
		for sc.Scan() {
			if m := listeningRE.FindStringSubmatch(sc.Text()); m != nil && !sent {
				found <- m[1]
				sent = true
			}
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case base := <-found:
		return base, nil
	case <-exited:
		return "", errors.New("opencode: server exited during startup")
	case <-time.After(timeout):
		return "", fmt.Errorf("opencode: server not listening after %s", timeout)
	}
}

type session struct {
	cmd          *exec.Cmd
	c            *client
	id           string
	emit         runner.Emit
	tr           *translator
	stderr       *runner.TailBuffer
	grace        time.Duration
	tempDir      string
	cancelEvents context.CancelFunc
	readDone     chan struct{}
	exited       chan struct{} // closed once the process was reaped
	done         chan struct{}
	waitErr      error

	mu          sync.Mutex
	sent        int  // prompts sent
	finished    bool // no more input; the server is being stopped
	succeeded   bool
	failReason  string
	abortStatus runner.DoneStatus
	abortReason string
	stopOnce    sync.Once
}

// Send hands a message to the running agent loop. The first is the task;
// later ones steer it at its next model call.
func (s *session) Send(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return runner.ErrSessionFinished
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.c.do(ctx, "POST", "/api/session/"+s.id+"/prompt", map[string]any{"text": text, "delivery": "steer"}, nil); err != nil {
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
	s.stop()
}

func (s *session) Done() <-chan struct{} { return s.done }

// stop terminates the server's process group, then kills it after the grace period.
func (s *session) stop() {
	s.stopOnce.Do(func() {
		runner.TerminateGroup(s.cmd)
		go func() {
			select {
			case <-s.exited:
			case <-time.After(s.grace):
				runner.KillGroup(s.cmd)
			}
		}()
	})
}

// read follows the event stream until it ends.
func (s *session) read(stream io.ReadCloser) {
	defer close(s.readDone)
	defer func() { _ = stream.Close() }()
	err := readSSE(stream, func(data []byte) {
		out := s.tr.handle(data)
		if out.permission != "" {
			s.refuse(out.permission)
		}
		switch out.ended {
		case executionSucceeded:
			s.executionSucceeded()
		case executionFailed, executionInterrupted:
			s.finish(false, out.reason)
		}
	})
	s.mu.Lock()
	finished := s.finished
	s.mu.Unlock()
	if !finished {
		// The server went away or dropped the stream mid-session.
		detail := "stream ended"
		if err != nil {
			detail = err.Error()
		}
		s.emit(domain.EventError, runner.Error{Message: "lost the harness event stream", Detail: detail})
		s.finish(false, "lost the harness event stream")
	}
}

// refuse answers a permission request no rule decided. The configured rules
// always end in a catch-all, so this is a safety net for actions opencode adds.
func (s *session) refuse(requestID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := s.c.do(ctx, "POST", "/api/session/"+s.id+"/permission/"+requestID+"/reply",
		map[string]any{"decision": "reject", "message": "Not permitted in this sandbox."}, nil)
	if err != nil {
		s.emit(domain.EventError, runner.Error{Message: "refusing a permission request failed", Detail: err.Error()})
	}
}

// executionSucceeded ends the session unless more messages wait in the
// opencode session's inbox, in which case opencode starts the next execution.
// Send holds the same lock while posting, so a message is either in the inbox
// here or refused with ErrSessionFinished.
func (s *session) executionSucceeded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var inbox []struct{}
	if err := s.c.do(ctx, "GET", "/api/session/"+s.id+"/inbox", nil, &inbox); err != nil {
		s.finishLocked(false, "reading the harness inbox: "+err.Error())
		return
	}
	if len(inbox) == 0 {
		s.finishLocked(true, "")
	}
}

func (s *session) finish(ok bool, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishLocked(ok, reason)
}

func (s *session) finishLocked(ok bool, reason string) {
	if s.finished {
		return
	}
	s.finished, s.succeeded, s.failReason = true, ok, reason
	s.stop()
}

// reap waits for the process to exit.
func (s *session) reap() {
	s.waitErr = s.cmd.Wait()
	close(s.exited)
}

// wait emits the final done event once the process exited and the event stream ended.
func (s *session) wait() {
	<-s.exited
	s.cancelEvents()
	<-s.readDone

	s.mu.Lock()
	s.finished = true
	done := runner.Done{ExitCode: s.cmd.ProcessState.ExitCode(), HarnessSessionID: s.id, Result: s.tr.lastText}
	switch {
	case s.abortStatus != "":
		done.Status, done.Reason = s.abortStatus, s.abortReason
	case s.succeeded:
		// The runner stopped the server; that is its normal end.
		done.Status, done.ExitCode = runner.DoneSucceeded, 0
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
	_ = os.RemoveAll(s.tempDir)
	s.emit(domain.EventDone, done)
	close(s.done)
}

func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("opencode: password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

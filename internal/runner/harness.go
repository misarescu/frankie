// Package runner implements the in-pod runner: one agent session per pod, driven
// over the RunnerService gRPC contract, with harness output normalized into a
// single event stream (docs/spec/04-agents-harnesses-models.md). Adding a harness
// means adding an implementation of Harness; nothing outside the runner changes.
package runner

import (
	"context"
	"errors"

	"github.com/misarescu/frankie/internal/domain"
)

// ErrSessionFinished is returned when a message is sent after the session stopped accepting input.
var ErrSessionFinished = errors.New("runner: session finished")

// DoneStatus is the outcome reported in the final done event.
type DoneStatus string

// Done statuses.
const (
	DoneSucceeded DoneStatus = "succeeded"
	DoneFailed    DoneStatus = "failed"
	DoneAborted   DoneStatus = "aborted"
	DoneTimeout   DoneStatus = "timeout"
)

// SessionConfig is what a harness needs to start a session.
type SessionConfig struct {
	Workdir      string
	Model        string
	SystemPrompt string
	Env          map[string]string
	MaxTurns     int
}

// Emit appends a normalized event to the session's stream. A harness must emit
// exactly one domain.EventDone, as its last event, when the session ends.
type Emit func(typ domain.EventType, payload any)

// Harness starts sessions for one agent CLI.
type Harness interface {
	Start(ctx context.Context, cfg SessionConfig, emit Emit) (Session, error)
}

// Session is a running harness session.
type Session interface {
	// Send delivers a user message. The first message is the task; later ones
	// steer the session at its next turn boundary. Returns ErrSessionFinished
	// once the session no longer accepts input.
	Send(text string) error
	// Abort stops the session; the done event carries status and reason.
	// Aborting a finished session does nothing.
	Abort(status DoneStatus, reason string)
	// Done is closed after the done event was emitted.
	Done() <-chan struct{}
}

// Payloads of the normalized events (schemas documented in runner.proto).
type (
	AssistantMessage struct {
		Text string `json:"text"`
	}
	ToolCall struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input any    `json:"input"`
	}
	ToolResult struct {
		ToolCallID string `json:"tool_call_id"`
		Name       string `json:"name"`
		Content    string `json:"content"`
		IsError    bool   `json:"is_error"`
	}
	CommandOutput struct {
		ToolCallID string `json:"tool_call_id"`
		Output     string `json:"output"`
		IsError    bool   `json:"is_error"`
	}
	TokenUsage struct {
		InputTokens              int64   `json:"input_tokens"`
		OutputTokens             int64   `json:"output_tokens"`
		CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
		CostUSD                  float64 `json:"cost_usd"`
		Turns                    int     `json:"turns"`
	}
	Error struct {
		Message string `json:"message"`
		Detail  string `json:"detail,omitempty"`
	}
	Done struct {
		Status           DoneStatus `json:"status"`
		Reason           string     `json:"reason,omitempty"`
		Result           string     `json:"result,omitempty"`
		HarnessSessionID string     `json:"harness_session_id,omitempty"`
		ExitCode         int        `json:"exit_code"`
		// Set when the session had a workspace and succeeded.
		Branch    string `json:"branch,omitempty"`
		CommitSHA string `json:"commit_sha,omitempty"`
		Pushed    bool   `json:"pushed,omitempty"`
	}
)
